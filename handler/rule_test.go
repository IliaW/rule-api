package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IliaW/rule-api/config"
	cacheMock "github.com/IliaW/rule-api/internal/cache/mocks"
	"github.com/IliaW/rule-api/internal/model"
	storageMock "github.com/IliaW/rule-api/internal/persistence/mocks"
	"github.com/IliaW/rule-api/internal/telemetry"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockRoundTripper struct {
	response *http.Response
}

func (rt *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return rt.response, nil
}

func Test_GetAllowedCrawl_Handler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	//mock telemetry
	metrics := telemetry.SetupMetrics(context.Background(), &config.Config{
		TelemetrySettings: &config.TelemetryConfig{
			Enabled: false,
		},
	})
	testSet := []struct {
		name                  string
		url                   string
		userAgent             string
		robotsUserAgent       string
		mockCachedRobotsFile  func() ([]byte, bool)
		mockStorageCustomRule func() (*model.Rule, error)
		mockHttpResponseCode  int
		mockHttpResponseBody  string
		expectedResponse      string
		expectedStatusCode    int
	}{
		{
			name:            "crawl allowed",
			url:             "https://example.com/test",
			userAgent:       "bot",
			robotsUserAgent: "robots-bot",
			mockCachedRobotsFile: func() ([]byte, bool) {
				return nil, false
			},
			mockStorageCustomRule: func() (*model.Rule, error) {
				return nil, errors.New("not found")
			},
			mockHttpResponseCode: http.StatusOK,
			mockHttpResponseBody: "User-agent: * \n Allow: /test",
			expectedResponse:     "{\"is_allowed\":true,\"status_code\":200,\"error\":\"\"}",
			expectedStatusCode:   http.StatusOK,
		},
		{
			name:            "crawl disallowed",
			url:             "https://example.com/test",
			userAgent:       "bot",
			robotsUserAgent: "robots-bot",
			mockCachedRobotsFile: func() ([]byte, bool) {
				return nil, false
			},
			mockStorageCustomRule: func() (*model.Rule, error) {
				return nil, errors.New("not found")
			},
			mockHttpResponseCode: http.StatusOK,
			mockHttpResponseBody: "User-agent: * \n Disallow: /test",
			expectedResponse:     "{\"is_allowed\":false,\"status_code\":200,\"error\":\"\"}",
			expectedStatusCode:   http.StatusOK,
		},
		{
			name:            "missed url in query",
			url:             "",
			userAgent:       "bot",
			robotsUserAgent: "robots-bot",
			mockCachedRobotsFile: func() ([]byte, bool) {
				return nil, false
			},
			mockStorageCustomRule: func() (*model.Rule, error) {
				return nil, errors.New("not found")
			},
			mockHttpResponseCode: http.StatusOK,
			mockHttpResponseBody: "User-agent: * \n Allow: /test",
			expectedResponse:     "{\"is_allowed\":false,\"status_code\":400,\"error\":\"'url' query parameter is required\"}",
			expectedStatusCode:   http.StatusBadRequest,
		},
		{
			name:            "missed user_agent in query",
			url:             "https://example.com/test",
			userAgent:       "",
			robotsUserAgent: "robots-bot",
			mockCachedRobotsFile: func() ([]byte, bool) {
				return nil, false
			},
			mockStorageCustomRule: func() (*model.Rule, error) {
				return nil, errors.New("not found")
			},
			mockHttpResponseCode: http.StatusOK,
			mockHttpResponseBody: "User-agent: * \n Allow: /test",
			expectedResponse:     "{\"is_allowed\":false,\"status_code\":400,\"error\":\"'user_agent' query parameter is required\"}\n",
			expectedStatusCode:   http.StatusBadRequest,
		},
		{
			name:            "custom rule exists in storage for the given domain",
			url:             "https://example.com/test",
			userAgent:       "bot",
			robotsUserAgent: "robots-bot",
			mockCachedRobotsFile: func() ([]byte, bool) {
				return nil, false
			},
			mockStorageCustomRule: func() (*model.Rule, error) {
				return &model.Rule{
					ID:        1,
					Domain:    "example.com",
					RobotsTxt: "User-agent: * \n Allow: /test",
				}, nil
			},
			mockHttpResponseCode: http.StatusOK,
			mockHttpResponseBody: "User-agent: * \n Disallow: /test",
			expectedResponse:     "{\"is_allowed\":true,\"status_code\":200,\"error\":\"\"}",
			expectedStatusCode:   http.StatusOK,
		},
		{
			name:            "robots.txt file exists in cache",
			url:             "https://example.com/test",
			userAgent:       "bot",
			robotsUserAgent: "robots-bot",
			mockCachedRobotsFile: func() ([]byte, bool) {
				return []byte("User-agent: * \n Allow: /test"), true
			},
			mockStorageCustomRule: func() (*model.Rule, error) {
				return nil, errors.New("not found")
			},
			mockHttpResponseCode: http.StatusOK,
			mockHttpResponseBody: "User-agent: * \n Disallow: /test",
			expectedResponse:     "{\"is_allowed\":true,\"status_code\":200,\"error\":\"\"}",
			expectedStatusCode:   http.StatusOK,
		},
	}
	for _, test := range testSet {
		t.Run(test.name, func(tt *testing.T) {
			// mock config
			cfg := &config.Config{
				RuleUserAgent: test.robotsUserAgent,
				TelemetrySettings: &config.TelemetryConfig{
					Enabled: false,
				},
			}
			// mock cache
			cache := cacheMock.NewCachedClient(tt)
			cache.On("GetRobotsFile", mock.Anything).Maybe().Return(test.mockCachedRobotsFile())
			cache.On("SaveRobotsFile", mock.Anything, mock.Anything).Maybe()
			// mock storage
			ruleRepo := storageMock.NewRuleStorage(tt)
			ruleRepo.On("GetByUrl", mock.Anything).Maybe().Return(test.mockStorageCustomRule())
			// mock http client
			httpMock := httptest.NewRecorder()
			httpMock.WriteString(test.mockHttpResponseBody)
			httpMock.Code = test.mockHttpResponseCode
			expectedRobotsTxt := httpMock.Result()
			httpClient := &http.Client{Transport: &mockRoundTripper{expectedRobotsTxt}}

			r := gin.Default()
			robotsHandler := NewRuleApiHandler(cfg, cache, ruleRepo, httpClient, metrics.ApiMetrics)
			r.GET("/crawl-allowed", robotsHandler.GetAllowedCrawl)
			req, _ := http.NewRequest("GET", fmt.Sprintf("/crawl-allowed?url=%s&user_agent=%s",
				test.url, test.userAgent), nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			responseData, _ := io.ReadAll(w.Body)
			assert.Equal(tt, test.expectedResponse, string(responseData))
			assert.Equal(tt, test.expectedStatusCode, w.Code)
		})
	}
}

func Test_GetCustomRule_Handler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	metrics := telemetry.SetupMetrics(context.Background(), &config.Config{
		TelemetrySettings: &config.TelemetryConfig{
			Enabled: false,
		},
	})
	testSet := []struct {
		name               string
		id                 string
		url                string
		mockStorage        func() (*model.Rule, error)
		mockMethodName     string
		expectedResponse   string
		expectedStatusCode int
	}{
		{
			name: "get custom rule by url",
			id:   "",
			url:  "https://example.com/test",
			mockStorage: func() (*model.Rule, error) {
				return &model.Rule{
					ID:        1,
					Domain:    "example.com",
					RobotsTxt: "User-agent: * \n Allow: /test",
				}, nil
			},
			mockMethodName: "GetByUrl",
			expectedResponse: "{\"id\":1,\"domain\":\"example.com\",\"robots_txt\":\"User-agent: * \\n Allow: " +
				"/test\",\"blocked\":false,\"created_at\":\"0001-01-01T00:00:00Z\",\"updated_at\":\"0001-01-01T00:00:00Z\"}",
			expectedStatusCode: http.StatusOK,
		},
		{
			name: "get custom rule by non-existent url",
			id:   "",
			url:  "https://example1.com/test",
			mockStorage: func() (*model.Rule, error) {
				return nil, errors.New("rule with domain 'example1.com' not found")
			},
			mockMethodName:     "GetByUrl",
			expectedResponse:   "{\"error\":\"failed to get rule by url. rule with domain 'example1.com' not found\"}",
			expectedStatusCode: http.StatusNotFound,
		},
		{
			name: "get custom rule by id",
			id:   "1",
			url:  "",
			mockStorage: func() (*model.Rule, error) {
				return &model.Rule{
					ID:        1,
					Domain:    "example.com",
					RobotsTxt: "User-agent: * \n Allow: /test",
				}, nil
			},
			mockMethodName: "GetById",
			expectedResponse: "{\"id\":1,\"domain\":\"example.com\",\"robots_txt\":\"User-agent: * \\n Allow: " +
				"/test\",\"blocked\":false,\"created_at\":\"0001-01-01T00:00:00Z\",\"updated_at\":\"0001-01-01T00:00:00Z\"}",
			expectedStatusCode: http.StatusOK,
		},
		{
			name: "empty id and url in query",
			id:   "",
			url:  "",
			mockStorage: func() (*model.Rule, error) {
				return nil, nil
			},
			mockMethodName:     "GetById",
			expectedResponse:   "{\"error\":\"'id' or 'url' query parameter is required\"}",
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name: "get custom rule by non-existent id",
			id:   "2",
			url:  "",
			mockStorage: func() (*model.Rule, error) {
				return nil, errors.New("rule with id '2' not found")
			},
			mockMethodName:     "GetById",
			expectedResponse:   "{\"error\":\"failed to get rule by id. rule with id '2' not found\"}",
			expectedStatusCode: http.StatusNotFound,
		},
	}
	for _, test := range testSet {
		t.Run(test.name, func(tt *testing.T) {
			// mock storage
			ruleRepo := storageMock.NewRuleStorage(tt)
			ruleRepo.On(test.mockMethodName, mock.Anything).Maybe().Return(test.mockStorage())

			r := gin.Default()
			robotsHandler := NewRuleApiHandler(nil, nil, ruleRepo, nil, metrics.ApiMetrics)
			r.GET("/custom-rule", robotsHandler.GetCustomRule)
			req, _ := http.NewRequest("GET", fmt.Sprintf("/custom-rule?url=%s&id=%s",
				test.url, test.id), nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			responseData, _ := io.ReadAll(w.Body)
			assert.Equal(tt, test.expectedResponse, string(responseData))
			assert.Equal(tt, test.expectedStatusCode, w.Code)
		})
	}
}

func Test_CreateCustomRule_Handler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	metrics := telemetry.SetupMetrics(context.Background(), &config.Config{
		TelemetrySettings: &config.TelemetryConfig{
			Enabled: false,
		},
	})
	testSet := []struct {
		name               string
		url                string
		body               string
		mockStorage        func() (int64, error)
		mockMethodName     string
		expectedResponse   string
		expectedStatusCode int
	}{
		{
			name: "create custom rule",
			url:  "https://example.com/test",
			body: "User-agent: * \n Allow: /test",
			mockStorage: func() (int64, error) {
				return 1, nil
			},
			mockMethodName:     "Save",
			expectedResponse:   "{\"id\":1}",
			expectedStatusCode: http.StatusOK,
		},
		{
			name: "create custom without url in query",
			url:  "",
			body: "User-agent: * \n Allow: /test",
			mockStorage: func() (int64, error) {
				return 1, nil
			},
			mockMethodName:     "Save",
			expectedResponse:   "{\"error\":\"'url' query parameter is required\"}",
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name: "create custom rule with empty body",
			url:  "https://example.com/test",
			body: "",
			mockStorage: func() (int64, error) {
				return 1, nil
			},
			mockMethodName:     "Save",
			expectedResponse:   "{\"error\":\"custom rules are not found or empty\"}",
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name: "error when save custom rule to database",
			url:  "https://example.com/test",
			body: "User-agent: * \n Allow: /test",
			mockStorage: func() (int64, error) {
				return 0, errors.New("duplicate entry")
			},
			mockMethodName:     "Save",
			expectedResponse:   "{\"error\":\"failed to save custom rule. duplicate entry\"}",
			expectedStatusCode: http.StatusInternalServerError,
		},
	}
	for _, test := range testSet {
		t.Run(test.name, func(tt *testing.T) {
			// mock storage
			ruleRepo := storageMock.NewRuleStorage(tt)
			ruleRepo.On(test.mockMethodName, mock.Anything).Maybe().Return(test.mockStorage())

			r := gin.Default()
			robotsHandler := NewRuleApiHandler(nil, nil, ruleRepo, nil, metrics.ApiMetrics)
			r.POST("/custom-rule", robotsHandler.CreateCustomRule)
			req, _ := http.NewRequest("POST", fmt.Sprintf("/custom-rule?url=%s", test.url),
				strings.NewReader(test.body))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			responseData, _ := io.ReadAll(w.Body)
			assert.Equal(tt, test.expectedResponse, string(responseData))
			assert.Equal(tt, test.expectedStatusCode, w.Code)
		})
	}
}

func Test_UpdateCustomRule_Handler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	metrics := telemetry.SetupMetrics(context.Background(), &config.Config{
		TelemetrySettings: &config.TelemetryConfig{
			Enabled: false,
		},
	})
	testSet := []struct {
		name                       string
		id                         string
		url                        string
		blocked                    string
		body                       string
		mockGetByIdStorageRequest  func() (*model.Rule, error)
		mockGetByUrlStorageRequest func() (*model.Rule, error)
		mockUpdateStorageRequest   func() (*model.Rule, error)
		expectedResponse           string
		expectedStatusCode         int
	}{
		{
			name:    "update body by rule id",
			id:      "1",
			url:     "",
			blocked: "false",
			body:    "User-agent: * \n Disallow: /test",
			mockGetByIdStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{
					ID:        1,
					Domain:    "example.com",
					RobotsTxt: "User-agent: * \n Allow: /test",
				}, nil
			},
			mockGetByUrlStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{}, nil
			},
			mockUpdateStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{
					ID:        1,
					Domain:    "example.com",
					RobotsTxt: "User-agent: * \n Disallow: /test",
				}, nil
			},
			expectedResponse: "{\"id\":1,\"domain\":\"example.com\",\"robots_txt\":\"User-agent: * " +
				"\\n Disallow: /test\",\"blocked\":false,\"created_at\":\"0001-01-01T00:00:00Z\",\"updated_at\":\"0001-01-01T00:00:00Z\"}",
			expectedStatusCode: http.StatusOK,
		},
		{
			name:    "update body by url",
			id:      "",
			url:     "https://example.com/test",
			blocked: "false",
			body:    "User-agent: * \n Disallow: /test",
			mockGetByIdStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{}, nil
			},
			mockGetByUrlStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{
					ID:        1,
					Domain:    "example.com",
					RobotsTxt: "User-agent: * \n Allow: /test",
				}, nil
			},
			mockUpdateStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{
					ID:        1,
					Domain:    "example.com",
					RobotsTxt: "User-agent: * \n Disallow: /test",
				}, nil
			},
			expectedResponse: "{\"id\":1,\"domain\":\"example.com\",\"robots_txt\":\"User-agent: * " +
				"\\n Disallow: /test\",\"blocked\":false,\"created_at\":\"0001-01-01T00:00:00Z\",\"updated_at\":\"0001-01-01T00:00:00Z\"}",
			expectedStatusCode: http.StatusOK,
		},
		{
			name:    "empty id and url in query parameter",
			id:      "",
			url:     "",
			blocked: "false",
			body:    "User-agent: * \n Disallow: /test",
			mockGetByIdStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{}, nil
			},
			mockGetByUrlStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{}, nil
			},
			mockUpdateStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{}, nil
			},
			expectedResponse:   "{\"error\":\"'id' or 'url' query parameter is required\"}",
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:    "empty blocked query parameter",
			id:      "1",
			url:     "https://example.com/test",
			blocked: "",
			body:    "User-agent: * \n Disallow: /test",
			mockGetByIdStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{}, nil
			},
			mockGetByUrlStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{}, nil
			},
			mockUpdateStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{}, nil
			},
			expectedResponse:   "{\"error\":\"'blocked' query parameter is required\"}",
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:    "non-existent id in query",
			id:      "2",
			url:     "",
			blocked: "false",
			body:    "User-agent: * \n Disallow: /test",
			mockGetByIdStorageRequest: func() (*model.Rule, error) {
				return nil, errors.New("rule with id '2' not found")
			},
			mockGetByUrlStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{}, nil
			},
			mockUpdateStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{}, nil
			},
			expectedResponse:   "{\"error\":\"failed to get rule by id. rule with id '2' not found\"}",
			expectedStatusCode: http.StatusNotFound,
		},
		{
			name:    "non-existent url in query",
			id:      "",
			url:     "https://example.com/test",
			blocked: "false",
			body:    "User-agent: * \n Disallow: /test",
			mockGetByIdStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{}, nil
			},
			mockGetByUrlStorageRequest: func() (*model.Rule, error) {
				return nil, errors.New("rule with domain 'example.com' not found")
			},
			mockUpdateStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{}, nil
			},
			expectedResponse:   "{\"error\":\"failed to get rule by url. rule with domain 'example.com' not found\"}",
			expectedStatusCode: http.StatusNotFound,
		},
		{
			name:    "error in database when update custom rule",
			id:      "1",
			url:     "",
			blocked: "false",
			body:    "User-agent: * \n Disallow: /test",
			mockGetByIdStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{
					ID:        1,
					Domain:    "example.com",
					RobotsTxt: "User-agent: * \n Allow: /test",
				}, nil
			},
			mockGetByUrlStorageRequest: func() (*model.Rule, error) {
				return &model.Rule{}, nil
			},
			mockUpdateStorageRequest: func() (*model.Rule, error) {
				return nil, errors.New("something went wrong")
			},
			expectedResponse:   "{\"error\":\"failed to update custom rule. something went wrong\"}",
			expectedStatusCode: http.StatusInternalServerError,
		},
	}
	for _, test := range testSet {
		t.Run(test.name, func(tt *testing.T) {
			// mock storage
			ruleRepo := storageMock.NewRuleStorage(tt)
			ruleRepo.On("GetById", mock.Anything).Maybe().Return(test.mockGetByIdStorageRequest())
			ruleRepo.On("GetByUrl", mock.Anything).Maybe().Return(test.mockGetByUrlStorageRequest())
			ruleRepo.On("Update", mock.Anything).Maybe().Return(test.mockUpdateStorageRequest())

			r := gin.Default()
			robotsHandler := NewRuleApiHandler(nil, nil, ruleRepo, nil, metrics.ApiMetrics)
			r.PUT("/custom-rule", robotsHandler.UpdateCustomRule)
			req, _ := http.NewRequest("PUT", fmt.Sprintf("/custom-rule?id=%s&url=%s&blocked=%s",
				test.id, test.url, test.blocked),
				strings.NewReader(test.body))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			responseData, _ := io.ReadAll(w.Body)
			assert.Equal(tt, test.expectedResponse, string(responseData))
			assert.Equal(tt, test.expectedStatusCode, w.Code)
		})
	}
}

func Test_DeleteCustomRule_Handler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	metrics := telemetry.SetupMetrics(context.Background(), &config.Config{
		TelemetrySettings: &config.TelemetryConfig{
			Enabled: false,
		},
	})
	testSet := []struct {
		name                      string
		id                        string
		mockDeleteStorageResponse error
		expectedResponse          string
		expectedStatusCode        int
	}{
		{
			name:                      "delete custom rule by id",
			id:                        "1",
			mockDeleteStorageResponse: nil,
			expectedResponse:          "{\"message\":\"rule with id '1' is deleted\"}",
			expectedStatusCode:        http.StatusOK,
		},
		{
			name:                      "id query parameter is empty",
			id:                        "",
			mockDeleteStorageResponse: nil,
			expectedResponse:          "{\"error\":\"'id' query parameter is required\"}",
			expectedStatusCode:        http.StatusBadRequest,
		},
		{
			name:                      "delete custom rule with non-existent id",
			id:                        "1",
			mockDeleteStorageResponse: nil,
			expectedResponse:          "{\"message\":\"rule with id '1' is deleted\"}",
			expectedStatusCode:        http.StatusOK,
		},
		{
			name:                      "error when delete custom rule",
			id:                        "1",
			mockDeleteStorageResponse: errors.New("something went wrong"),
			expectedResponse:          "{\"error\":\"failed to delete custom rule. something went wrong\"}",
			expectedStatusCode:        http.StatusInternalServerError,
		},
	}
	for _, test := range testSet {
		t.Run(test.name, func(tt *testing.T) {
			// mock storage
			ruleRepo := storageMock.NewRuleStorage(tt)
			ruleRepo.On("Delete", mock.Anything).Maybe().Return(test.mockDeleteStorageResponse)

			r := gin.Default()
			robotsHandler := NewRuleApiHandler(nil, nil, ruleRepo, nil, metrics.ApiMetrics)
			r.DELETE("/custom-rule", robotsHandler.DeleteCustomRule)
			req, _ := http.NewRequest("DELETE", fmt.Sprintf("/custom-rule?id=%s", test.id), nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			responseData, _ := io.ReadAll(w.Body)
			assert.Equal(tt, test.expectedResponse, string(responseData))
			assert.Equal(tt, test.expectedStatusCode, w.Code)
		})
	}
}
