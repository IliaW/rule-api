package handler

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/IliaW/rule-api/config"
	cacheClient "github.com/IliaW/rule-api/internal/cache"
	"github.com/IliaW/rule-api/internal/model"
	"github.com/IliaW/rule-api/internal/persistence"
	"github.com/IliaW/rule-api/internal/telemetry"
	"github.com/IliaW/rule-api/util"
	"github.com/gin-gonic/gin"
	"github.com/jimsmart/grobotstxt"
)

type RuleApiHandler struct {
	cfg        *config.Config
	cache      cacheClient.CachedClient
	ruleRepo   persistence.RuleStorage
	httpClient *http.Client
	metrics    *telemetry.ApiMetrics
}

func NewRuleApiHandler(cfg *config.Config, cache cacheClient.CachedClient, ruleRepo persistence.RuleStorage,
	httpClient *http.Client, metrics *telemetry.ApiMetrics) *RuleApiHandler {
	return &RuleApiHandler{
		cfg:        cfg,
		cache:      cache,
		ruleRepo:   ruleRepo,
		httpClient: httpClient,
		metrics:    metrics,
	}
}

// GetAllowedCrawl godoc
// @Summary Check if crawling is allowed for a specific user agent and URL
// @Description Check if the given user agent is allowed to crawl the specified URL based on the robots.txt rules
// @Tags Crawling
// @Produce json
// @Param url query string true "URL to check"
// @Param user_agent query string true "User agent to check"
// @Success 200 {object} model.AllowedCrawlResponse "Response object"
// @Router /crawl-allowed [get]
func (h *RuleApiHandler) GetAllowedCrawl(c *gin.Context) {
	url := c.Query("url")
	if url == "" {
		c.JSON(http.StatusBadRequest, model.AllowedCrawlResponse{
			IsAllowed:  false,
			Blocked:    false,
			StatusCode: http.StatusBadRequest,
			Error:      "'url' query parameter is required",
		})
		h.metrics.ErrorResponseCounter(1)
		return
	}
	userAgent := c.Query("user_agent")
	if userAgent == "" {
		c.JSON(http.StatusBadRequest, model.AllowedCrawlResponse{
			IsAllowed:  false,
			Blocked:    false,
			StatusCode: http.StatusBadRequest,
			Error:      "'user_agent' query parameter is required",
		})
		h.metrics.ErrorResponseCounter(1)
		return
	}

	var robotsTxt string
	var targetResponseStatusCode int
	blocked := false

	// check the custom rule for the given url in database
	rule, err := h.ruleRepo.GetByUrl(url)
	if err == nil && rule != nil && rule.RobotsTxt != "" {
		robotsTxt = rule.RobotsTxt
		blocked = rule.Blocked
		targetResponseStatusCode = http.StatusOK
	} else {
		// upload the robots.txt file if custom rule is not found in database
		tResp, err := h.getRobotsTxt(url)
		if err != nil {
			// most likely, there is no access to the URL, or the robots.txt file does not exist
			c.JSON(http.StatusInternalServerError, model.AllowedCrawlResponse{
				IsAllowed:  false,
				Blocked:    blocked,
				StatusCode: http.StatusInternalServerError,
				Error:      err.Error(),
			})
			h.metrics.ErrorResponseCounter(1)
			return
		}
		if !isSuccess(tResp.StatusCode) {
			c.JSON(http.StatusOK, model.AllowedCrawlResponse{
				IsAllowed:  false,
				Blocked:    blocked,
				StatusCode: tResp.StatusCode,
				Error:      string(tResp.Body),
			})
			h.metrics.SuccessResponseCounter(1)
			return
		}
		robotsTxt = string(tResp.Body)
		targetResponseStatusCode = tResp.StatusCode
	}

	if ok := grobotstxt.AgentAllowed(robotsTxt, userAgent, url); ok {
		c.JSON(http.StatusOK, model.AllowedCrawlResponse{
			IsAllowed:  true,
			Blocked:    blocked,
			StatusCode: targetResponseStatusCode,
			Error:      "",
		})
		h.metrics.SuccessResponseCounter(1)
		return
	}

	c.JSON(http.StatusOK, model.AllowedCrawlResponse{
		IsAllowed:  false,
		Blocked:    blocked,
		StatusCode: targetResponseStatusCode,
		Error:      "",
	})
	h.metrics.SuccessResponseCounter(1)
}

// GetCustomRule godoc
// @Summary Get custom rule by ID or URL
// @Description Retrieve a custom rule based on the provided query parameter 'id' or 'url'
// @Tags Custom Rule
// @Produce json
// @Param id query string false "Custom rule ID"
// @Param url query string false "Custom rule URL"
// @Success 200 {object} model.Rule "Custom rule object"
// @Security ApiKeyAuth
// @Router /custom-rule [get]
func (h *RuleApiHandler) GetCustomRule(c *gin.Context) {
	id := c.Query("id")
	url := c.Query("url")
	if id == "" && url == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "'id' or 'url' query parameter is required"})
		return
	}

	if id != "" {
		rule, err := h.ruleRepo.GetById(id)
		if err != nil {
			c.JSON(http.StatusNotFound,
				gin.H{"error": fmt.Sprintf("failed to get rule by id. %s", err.Error())})
			return
		}
		c.JSON(http.StatusOK, rule)
		return
	}

	rule, err := h.ruleRepo.GetByUrl(url)
	if err != nil {
		c.JSON(http.StatusNotFound,
			gin.H{"error": fmt.Sprintf("failed to get rule by url. %s", err.Error())})
		return
	}

	c.JSON(http.StatusOK, rule)
}

// CreateCustomRule godoc
// @Summary Create a custom rule
// @Description Create a new custom rule by providing a URL and the corresponding rule file
// @Tags Custom Rule
// @Accept plain
// @Produce json
// @Param url query string true "URL for the custom rule"
// @Param blocked query bool false "Block the domain from being crawled"
// @Param file body string true "Custom rule file content"
// @Success 200 {object} string "Custom rule created successfully"
// @Security ApiKeyAuth
// @Router /custom-rule [post]
func (h *RuleApiHandler) CreateCustomRule(c *gin.Context) {
	url := c.Query("url")
	if url == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "'url' query parameter is required"})
		return
	}

	b := c.DefaultQuery("blocked", "false")
	blocked, err := strconv.ParseBool(b)
	if err != nil {
		blocked = false
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("unable to read file. %s", err.Error())})
		return
	}
	if len(body) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "custom rules are not found or empty"})
		return
	}

	domain, err := util.GetDomain(url)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to parse url. %s", err.Error())})
		return
	}

	id, err := h.ruleRepo.Save(&model.Rule{
		Domain:    domain,
		RobotsTxt: string(body),
		Blocked:   blocked,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError,
			gin.H{"error": fmt.Sprintf("failed to save custom rule. %v", err.Error())})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id})
}

// UpdateCustomRule godoc
// @Summary Update a custom rule by ID or URL
// @Description Update an existing custom rule based on the provided ID or URL.
// @Tags Custom Rule
// @Accept plain
// @Produce json
// @Param id query string false "Custom rule ID"
// @Param url query string false "Custom rule URL"
// @Param blocked query bool true "Block the domain from being crawled"
// @Param file body string true "Updated custom rule file content"
// @Success 200 {object} model.Rule "Updated custom rule"
// @Security ApiKeyAuth
// @Router /custom-rule [put]
func (h *RuleApiHandler) UpdateCustomRule(c *gin.Context) {
	id := c.Query("id")
	url := c.Query("url")
	if id == "" && url == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "'id' or 'url' query parameter is required"})
		return
	}

	b := c.Query("blocked")
	if b == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "'blocked' query parameter is required"})
		return
	}
	blocked, parseErr := strconv.ParseBool(b)
	if parseErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to parse 'blocked' query parameter"})
		return
	}

	body, readErr := io.ReadAll(c.Request.Body)
	if readErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to read file"})
		return
	}
	if len(body) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "custom rules are not found or empty"})
		return
	}

	var rule *model.Rule
	var err error
	if id != "" {
		rule, err = h.ruleRepo.GetById(id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("failed to get rule by id. %s", err.Error())})
			return
		}
	} else {
		rule, err = h.ruleRepo.GetByUrl(url)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("failed to get rule by url. %s", err.Error())})
			return
		}
	}

	// skip updating if no changes are made
	if rule.RobotsTxt == string(body) && rule.Blocked == blocked {
		c.JSON(http.StatusOK, rule)
		return
	}

	rule.RobotsTxt = string(body)
	rule.Blocked = blocked

	result, err := h.ruleRepo.Update(rule)
	if err != nil {
		c.JSON(http.StatusInternalServerError,
			gin.H{"error": fmt.Sprintf("failed to update custom rule. %v", err.Error())})
		return
	}

	c.JSON(http.StatusOK, result)
}

// DeleteCustomRule godoc
// @Summary Delete a custom rule by ID
// @Description Delete an existing custom rule based on the provided ID.
// @Tags Custom Rule
// @Produce json
// @Param id query string true "Custom rule ID"
// @Success 200 {object} string "Rule deleted successfully"
// @Security ApiKeyAuth
// @Router /custom-rule [delete]
func (h *RuleApiHandler) DeleteCustomRule(c *gin.Context) {
	id := c.Query("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "'id' query parameter is required"})
		return
	}

	err := h.ruleRepo.Delete(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError,
			gin.H{"error": fmt.Sprintf("failed to delete custom rule. %v", err.Error())})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("rule with id '%s' is deleted", id)})
}

func (h *RuleApiHandler) getRobotsTxt(url string) (*model.TargetResponse, error) {
	// check if the robots.txt file is already saved in cache
	file, ok := h.cache.GetRobotsFile(url)
	if ok {
		return &model.TargetResponse{
			StatusCode: http.StatusOK,
			Body:       file,
		}, nil
	}
	// make get request to fetch the robots.txt file if it is not saved in cache
	tResp, err := h.requestToRobotsTxt(url)
	if err != nil {
		return nil, err
	}

	// save the robots.txt file to cache if the request is successful and the body is not empty
	if isSuccess(tResp.StatusCode) && len(tResp.Body) != 0 {
		h.cache.SaveRobotsFile(url, tResp.Body)
	}

	return tResp, nil
}

func (h *RuleApiHandler) requestToRobotsTxt(url string) (*model.TargetResponse, error) {
	baseUrl, err := util.GetBaseUrl(url)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to parse url. %s", err.Error()))
	}
	req, err := http.NewRequest(http.MethodGet, baseUrl+"/robots.txt", nil)
	req.Header.Set("User-Agent", h.cfg.RuleUserAgent)
	resp, err := h.httpClient.Do(req)
	if err != nil {
		slog.Error(fmt.Sprintf("error making http get request to %s/robots.txt", baseUrl),
			slog.String("err", err.Error()))
		return nil, err
	}
	defer func() {
		err = resp.Body.Close()
		if err != nil {
			slog.Error("error closing response body", slog.String("err", err.Error()))
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("error reading response body", slog.String("err", err.Error()))
		return nil, err
	}

	return &model.TargetResponse{
		StatusCode: resp.StatusCode,
		Body:       body,
	}, nil
}

func isSuccess(statusCode int) bool {
	return statusCode >= 200 && statusCode < 300
}
