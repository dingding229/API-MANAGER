package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	UpstreamCredentials     string
	HTTPAddr                string
	AdminToken              string
	ProductionMode          bool
	MetricsToken            string
	ShutdownTimeout         time.Duration
	MaxBodyBytes            int64
	LogLevel                string
	PostgresDSN             string
	RedisAddr               string
	RedisPassword           string
	RedisUsername           string
	RedisTLS                bool
	RedisTLSCAFile          string
	RedisDB                 int
	UseRedis                bool
	UserJWTSecret           string
	UserJWTTTL              time.Duration
	PluginDir               string
	PluginMaxBytes          int64
	PluginDatabaseWrites    bool
	PluginLibraryDir        string
	CredentialEncryptionKey string
	CORSOrigins             string
	OTELEnabled             bool
	OTELServiceName         string
	OTLPEndpoint            string
	OTLPInsecure            bool
}

func Load() (Config, error) {
	for _, name := range []string{"PRODUCTION_MODE", "USE_REDIS", "REDIS_TLS_ENABLED"} {
		if value := os.Getenv(name); value != "" {
			if _, err := strconv.ParseBool(value); err != nil {
				return Config{}, fmt.Errorf("invalid boolean for %s: %w", name, err)
			}
		}
	}
	secrets := make(map[string]string)
	for _, name := range []string{"ADMIN_TOKEN", "USER_JWT_SECRET", "CREDENTIAL_ENCRYPTION_KEY", "POSTGRES_DSN", "REDIS_PASSWORD", "API_UPSTREAM_CREDENTIALS", "METRICS_TOKEN"} {
		value, err := secret(name)
		if err != nil {
			return Config{}, err
		}
		secrets[name] = value
	}
	return Config{
		UpstreamCredentials:     secrets["API_UPSTREAM_CREDENTIALS"],
		HTTPAddr:                env("HTTP_ADDR", ":8080"),
		AdminToken:              secrets["ADMIN_TOKEN"],
		ProductionMode:          envBool("PRODUCTION_MODE", false),
		MetricsToken:            secrets["METRICS_TOKEN"],
		ShutdownTimeout:         envDuration("SHUTDOWN_TIMEOUT", 10*time.Second),
		MaxBodyBytes:            envInt64("MAX_BODY_BYTES", 1<<20),
		LogLevel:                env("LOG_LEVEL", "info"),
		PostgresDSN:             secrets["POSTGRES_DSN"],
		RedisAddr:               env("REDIS_ADDR", "redis:6379"),
		RedisPassword:           secrets["REDIS_PASSWORD"],
		RedisUsername:           os.Getenv("REDIS_USERNAME"),
		RedisTLS:                envBool("REDIS_TLS_ENABLED", false),
		RedisTLSCAFile:          os.Getenv("REDIS_TLS_CA_FILE"),
		RedisDB:                 envInt("REDIS_DB", 0),
		UseRedis:                envBool("USE_REDIS", os.Getenv("REDIS_ADDR") != ""),
		UserJWTSecret:           secrets["USER_JWT_SECRET"],
		UserJWTTTL:              envDuration("USER_JWT_TTL", 12*time.Hour),
		PluginDir:               env("PLUGIN_DIR", "plugins"),
		PluginMaxBytes:          envInt64("PLUGIN_MAX_BYTES", 20<<20),
		PluginDatabaseWrites:    envBool("PLUGIN_DATABASE_WRITES_ENABLED", false),
		PluginLibraryDir:        env("PLUGIN_LIBRARY_DIR", "plugin-library"),
		CredentialEncryptionKey: secrets["CREDENTIAL_ENCRYPTION_KEY"],
		CORSOrigins:             env("CORS_ORIGINS", ""),
		OTELEnabled:             envBool("OTEL_ENABLED", false),
		OTELServiceName:         env("OTEL_SERVICE_NAME", "api-manager"),
		OTLPEndpoint:            env("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTLPInsecure:            envBool("OTEL_EXPORTER_OTLP_INSECURE", true),
	}, nil
}

// secret supports Docker/Kubernetes mounted secrets without copying their contents into
// the container environment. Ambiguous sources and unreadable files fail closed.
func secret(name string) (string, error) {
	value, file := os.Getenv(name), os.Getenv(name+"_FILE")
	if value != "" && file != "" {
		return "", fmt.Errorf("%s and %s_FILE cannot both be set", name, name)
	}
	if file == "" {
		return value, nil
	}
	info, err := os.Stat(file)
	if err != nil {
		return "", fmt.Errorf("read %s_FILE: %w", name, err)
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return "", fmt.Errorf("%s_FILE must be a regular file of at most 64 KiB", name)
	}
	contents, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("read %s_FILE: %w", name, err)
	}
	result := strings.TrimRight(string(contents), "\r\n")
	if result == "" || strings.ContainsRune(result, 0) {
		return "", fmt.Errorf("%s_FILE must contain a non-empty text value", name)
	}
	return result, nil
}

func (c Config) Validate() error {
	forbidden := map[string]bool{"change-me-in-production": true, "change-this-user-jwt-secret": true, "change-me": true, "replace-with-a-long-random-secret": true, "replace-with-a-long-random-jwt-secret": true}
	for _, item := range []struct{ name, value string }{{"ADMIN_TOKEN", c.AdminToken}, {"USER_JWT_SECRET", c.UserJWTSecret}, {"CREDENTIAL_ENCRYPTION_KEY", c.CredentialEncryptionKey}} {
		if len(strings.TrimSpace(item.value)) < 32 || forbidden[item.value] || strings.HasPrefix(item.value, "generate-") || strings.HasPrefix(item.value, "replace-") {
			return errors.New(item.name + " must be a distinct random secret of at least 32 characters")
		}
	}
	if c.AdminToken == c.UserJWTSecret || c.AdminToken == c.CredentialEncryptionKey || c.UserJWTSecret == c.CredentialEncryptionKey {
		return errors.New("admin, JWT, and credential encryption secrets must be distinct")
	}
	if c.ProductionMode {
		if c.OTELEnabled && (c.OTLPInsecure || c.OTLPEndpoint == "") {
			return errors.New("production tracing requires a configured OTLP endpoint and TLS")
		}
		if c.PostgresDSN == "" || !c.UseRedis || c.RedisAddr == "" {
			return errors.New("production requires PostgreSQL and Redis; in-memory fallback is forbidden")
		}
		postgresURL, err := url.Parse(c.PostgresDSN)
		if err != nil || (postgresURL.Scheme != "postgres" && postgresURL.Scheme != "postgresql") || postgresURL.Hostname() == "" || postgresURL.Query().Get("sslmode") != "verify-full" {
			return errors.New("production POSTGRES_DSN must be a PostgreSQL URL with sslmode=verify-full")
		}
		if !c.RedisTLS || c.RedisPassword == "" {
			return errors.New("production requires Redis TLS and a non-empty Redis password")
		}
		if c.RedisPassword == c.AdminToken || c.RedisPassword == c.UserJWTSecret || c.RedisPassword == c.CredentialEncryptionKey {
			return errors.New("Redis password must be distinct from application secrets")
		}
		for _, origin := range strings.Split(c.CORSOrigins, ",") {
			if strings.TrimSpace(origin) == "*" {
				return errors.New("production CORS_ORIGINS cannot include a wildcard")
			}
		}
		if len(c.MetricsToken) < 32 || c.MetricsToken == c.RedisPassword || c.MetricsToken == c.AdminToken || c.MetricsToken == c.UserJWTSecret || c.MetricsToken == c.CredentialEncryptionKey {
			return errors.New("production requires a distinct METRICS_TOKEN of at least 32 characters")
		}
	}
	if c.UserJWTTTL <= 0 || c.UserJWTTTL > 24*time.Hour {
		return errors.New("USER_JWT_TTL must be between 1 second and 24 hours")
	}
	return nil
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func envDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
func envInt64(key string, fallback int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
