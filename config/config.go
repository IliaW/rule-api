package config

import (
	"log/slog"
	"os"
	"path"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Env                string            `mapstructure:"env"`
	LogLevel           string            `mapstructure:"log_level"`
	LogType            string            `mapstructure:"log_type"`
	ServiceName        string            `mapstructure:"service_name"`
	Port               string            `mapstructure:"port"`
	Version            string            `mapstructure:"version"`
	CorsMaxAgeHours    time.Duration     `mapstructure:"cors_max_age_hours"`
	RuleApiUrlPath     string            `mapstructure:"rule_api_url_path"`
	MaxBodySize        int64             `mapstructure:"max_body_size"`
	RuleUserAgent      string            `mapstructure:"rule_user_agent"`
	CacheSettings      *CacheConfig      `mapstructure:"cache"`
	DbSettings         *DatabaseConfig   `mapstructure:"database"`
	HttpClientSettings *HttpClientConfig `mapstructure:"http_client"`
	TelemetrySettings  *TelemetryConfig  `mapstructure:"telemetry"`
}

type CacheConfig struct {
	Servers         []string      `mapstructure:"servers"`
	TtlForRobotsTxt time.Duration `mapstructure:"ttl_for_robots_txt"`
}

type DatabaseConfig struct {
	Host            string        `mapstructure:"host"`
	Port            string        `mapstructure:"port"`
	User            string        `mapstructure:"user"`
	Password        string        `mapstructure:"password"`
	Name            string        `mapstructure:"name"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
}

type HttpClientConfig struct {
	RequestTimeout            time.Duration `mapstructure:"request_timeout"`
	MaxIdleConnections        int           `mapstructure:"max_idle_connections"`
	MaxIdleConnectionsPerHost int           `mapstructure:"max_idle_connections_per_host"`
	MaxConnectionsPerHost     int           `mapstructure:"max_connections_per_host"`
	IdleConnectionTimeout     time.Duration `mapstructure:"idle_connection_timeout"`
	TlsHandshakeTimeout       time.Duration `mapstructure:"tls_handshake_timeout"`
	DialTimeout               time.Duration `mapstructure:"dial_timeout"`
	DialKeepAlive             time.Duration `mapstructure:"dial_keep_alive"`
	TlsInsecureSkipVerify     bool          `mapstructure:"tls_insecure_skip_verify"`
}

type TelemetryConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	CollectorUrl string `mapstructure:"collector_url"`
}

func MustLoad() *Config {
	viper.AddConfigPath(path.Join("."))
	viper.SetConfigName("config")
	viper.AutomaticEnv()

	err := viper.ReadInConfig()
	if err != nil {
		slog.Error("can't initialize config file.", slog.String("err", err.Error()))
		os.Exit(1)
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		slog.Error("error unmarshalling viper config.", slog.String("err", err.Error()))
		os.Exit(1)
	}

	return &cfg
}
