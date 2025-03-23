package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/IliaW/rule-api/config"
	docs "github.com/IliaW/rule-api/docs"
	"github.com/IliaW/rule-api/handler"
	cacheClient "github.com/IliaW/rule-api/internal/cache"
	"github.com/IliaW/rule-api/internal/persistence"
	"github.com/IliaW/rule-api/internal/telemetry"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/lmittmann/tint"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

var (
	cfg        *config.Config
	cache      cacheClient.CachedClient
	db         *sql.DB
	ruleRepo   persistence.RuleStorage
	httpClient *http.Client
	metrics    *telemetry.MetricsProvider
)

// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg = config.MustLoad()
	setupLogger()
	metrics = telemetry.SetupMetrics(context.Background(), cfg)
	defer metrics.Close()
	db = setupDatabase()
	defer closeDatabase()
	ruleRepo = persistence.NewRuleRepository(db)
	cache = cacheClient.NewMemcachedClient(cfg.CacheSettings)
	defer cache.Close()
	httpClient = setupHttpClient()
	slog.Info("starting application on port "+cfg.Port, slog.String("env", cfg.Env))

	port := fmt.Sprintf(":%v", cfg.Port)
	srv := &http.Server{
		Addr:    port,
		Handler: httpServer().Handler(),
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			slog.Error("listen:", slog.Any("err", err))
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("stopping server...")
	ctxT, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := srv.Shutdown(ctxT)
	if errors.Is(err, context.DeadlineExceeded) {
		slog.Error("shutdown timeout exceeded")
		os.Exit(1)
	}
	slog.Info("server stopped.")
}

func httpServer() *gin.Engine {
	setupGinMod()
	r := gin.New()
	r.UseH2C = true
	r.Use(gin.Recovery())
	r.Use(setCORS())
	r.Use(limitBodySize())
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{SkipPaths: []string{"/ping", "/swagger"}}))
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	ruleApiHandler := handler.NewRuleApiHandler(cfg, cache, ruleRepo, httpClient, metrics.ApiMetrics)
	crawlAllowed := r.Group(cfg.RuleApiUrlPath)
	crawlAllowed.GET("/crawl-allowed", ruleApiHandler.GetAllowedCrawl)

	customRule := r.Group(cfg.RuleApiUrlPath)
	customRule.Use(apiKeyCheck())
	customRule.GET("/custom-rule", ruleApiHandler.GetCustomRule)
	customRule.POST("/custom-rule", ruleApiHandler.CreateCustomRule)
	customRule.PUT("/custom-rule", ruleApiHandler.UpdateCustomRule)
	customRule.DELETE("/custom-rule", ruleApiHandler.DeleteCustomRule)

	docs.SwaggerInfo.Title = fmt.Sprintf("Rule API (%s)", cfg.ServiceName)
	docs.SwaggerInfo.Description = "This API controls crawl permissions and creates custom rules for specific domains."
	docs.SwaggerInfo.Version = cfg.Version
	docs.SwaggerInfo.BasePath = cfg.RuleApiUrlPath
	docs.SwaggerInfo.Schemes = []string{"http", "https"}

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler))

	r.NoRoute(func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusNotFound,
			gin.H{"message": fmt.Sprintf("no route found for %s %s", c.Request.Method, c.Request.URL)})
	})

	return r
}

func setCORS() gin.HandlerFunc {
	return cors.New(cors.Config{
		AllowOriginFunc: func(origin string) bool { //allow all origins and echoes back the caller domain
			return true
		},
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions},
		AllowHeaders: []string{"Content-Type", "Content-Length", "Accept-Encoding", "Authorization", "X-Forwarded-For",
			"X-CSRF-Token", "X-Max"},
		AllowCredentials: true,
		MaxAge:           cfg.CorsMaxAgeHours,
	})
}

func limitBodySize() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, cfg.MaxBodySize*1024*1024)
	}
}

func apiKeyCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("X-API-Key")
		if apiKey == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "X-API-Key header is missing"})
			c.Abort()
			return
		}

		apiKeyHash := hashAPIKey(apiKey)
		var isActive bool

		err := db.QueryRow("SELECT is_active FROM web_crawler.api_key WHERE api_key = $1", apiKeyHash).
			Scan(&isActive)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api-key"})
				c.Abort()
				return
			}
			slog.Error("failed to query api key", slog.String("err", err.Error()))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "api-key check failed"})
			c.Abort()
			return
		}

		if !isActive {
			c.JSON(http.StatusForbidden, gin.H{"error": "api-key is not active"})
			c.Abort()
			return
		}

		c.Next()
	}
}

func hashAPIKey(apiKey string) string {
	hash := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(hash[:])
}

func setupLogger() *slog.Logger {
	envLogLevel := strings.ToLower(cfg.LogLevel)
	var slogLevel slog.Level
	err := slogLevel.UnmarshalText([]byte(envLogLevel))
	if err != nil {
		log.Printf("encountenred log level: '%s'. The package does not support custom log levels", envLogLevel)
		slogLevel = slog.LevelDebug
	}
	log.Printf("slog level overwritten to '%v'", slogLevel)
	slog.SetLogLoggerLevel(slogLevel)

	replaceAttrs := func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.SourceKey {
			source := a.Value.Any().(*slog.Source)
			source.File = filepath.Base(source.File)
		}
		return a
	}

	var logger *slog.Logger
	if strings.ToLower(cfg.LogType) == "json" {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			AddSource:   true,
			Level:       slogLevel,
			ReplaceAttr: replaceAttrs}))
	} else {
		logger = slog.New(tint.NewHandler(os.Stdout, &tint.Options{
			AddSource:   true,
			Level:       slogLevel,
			ReplaceAttr: replaceAttrs,
			NoColor: func() bool {
				if cfg.Env == "local" {
					return false
				}
				return true
			}()}))
	}

	slog.SetDefault(logger)
	logger.Debug("debug messages are enabled.")

	return logger
}

func setupGinMod() {
	env := strings.ToLower(cfg.Env)
	if env == "dev" || env == "" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}
}

func setupDatabase() *sql.DB {
	slog.Info("connecting to the database...")
	connStr := fmt.Sprintf("user=%s password=%s host=%s port=%s dbname=%s sslmode=disable",
		cfg.DbSettings.User,
		cfg.DbSettings.Password,
		cfg.DbSettings.Host,
		cfg.DbSettings.Port,
		cfg.DbSettings.Name,
	)
	database, err := sql.Open("postgres", connStr)
	if err != nil {
		slog.Error("failed to establish database connection.", slog.String("err", err.Error()))
		os.Exit(1)
	}
	database.SetConnMaxLifetime(cfg.DbSettings.ConnMaxLifetime)
	database.SetMaxOpenConns(cfg.DbSettings.MaxOpenConns)
	database.SetMaxIdleConns(cfg.DbSettings.MaxIdleConns)

	maxRetry := 6
	for i := 1; i <= maxRetry; i++ {
		slog.Info("ping the database.", slog.String("attempt", fmt.Sprintf("%d/%d", i, maxRetry)))
		pingErr := database.Ping()
		if pingErr != nil {
			slog.Error("not responding.", slog.String("err", pingErr.Error()))
			if i == maxRetry {
				slog.Error("failed to establish database connection.")
				os.Exit(1)
			}
			slog.Info(fmt.Sprintf("wait %d seconds", 5*i))
			time.Sleep(time.Duration(5*i) * time.Second)
		} else {
			break
		}
	}
	slog.Info("connected to the database!")

	return database
}

func closeDatabase() {
	slog.Info("closing database connection.")
	err := db.Close()
	if err != nil {
		slog.Error("failed to close database connection.", slog.String("err", err.Error()))
	}
}

func setupHttpClient() *http.Client {
	transport := &http.Transport{
		MaxIdleConns:        cfg.HttpClientSettings.MaxIdleConnections,
		MaxIdleConnsPerHost: cfg.HttpClientSettings.MaxIdleConnectionsPerHost,
		MaxConnsPerHost:     cfg.HttpClientSettings.MaxConnectionsPerHost,
		IdleConnTimeout:     cfg.HttpClientSettings.IdleConnectionTimeout,
		TLSHandshakeTimeout: cfg.HttpClientSettings.TlsHandshakeTimeout,
		DialContext: (&net.Dialer{
			Timeout:   cfg.HttpClientSettings.DialTimeout,
			KeepAlive: cfg.HttpClientSettings.DialKeepAlive,
		}).DialContext,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.HttpClientSettings.TlsInsecureSkipVerify,
		},
	}

	return &http.Client{
		Transport: transport,
		Timeout:   cfg.HttpClientSettings.RequestTimeout,
	}
}
