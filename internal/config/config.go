package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	UpstreamCredentials       string
	HTTPAddr                  string
	PublicAPIBaseURL          string
	PublicHTTPAddr            string
	PublicUIDir               string
	AdminUsername             string
	AdminPassword             string
	UserSessionTTL            time.Duration
	ProductionMode            bool
	AllowInternalPlaintext    bool
	MetricsToken              string
	ShutdownTimeout           time.Duration
	MaxBodyBytes              int64
	LogLevel                  string
	PostgresDSN               string
	RedisAddr                 string
	RedisPassword             string
	RedisUsername             string
	RedisTLS                  bool
	RedisDB                   int
	UseRedis                  bool
	PluginDir                 string
	PluginMaxBytes            int64
	PluginDatabaseWrites      bool
	PluginLibraryDir          string
	CredentialEncryptionKey   string
	CORSOrigins               string
	OTELEnabled               bool
	OTELServiceName           string
	OTLPEndpoint              string
	OTLPInsecure              bool
	ObservabilityDir          string
	ObservabilityStackEnabled bool
	GrafanaAdminPassword      string
	ObservabilityMaxLogs      int
	ObservabilityMaxTraces    int
	ObservabilityFileBytes    int64
}

func Load() (Config, error) {
	secrets := make(map[string]string)
	for _, name := range []string{"ADMIN_PASSWORD", "CREDENTIAL_ENCRYPTION_KEY", "POSTGRES_DSN", "POSTGRES_PASSWORD", "REDIS_PASSWORD", "API_UPSTREAM_CREDENTIALS", "METRICS_TOKEN", "GRAFANA_ADMIN_PASSWORD"} {
		value, err := secret(name)
		if err != nil {
			return Config{}, err
		}
		secrets[name] = value
	}

	redisAddr := env("REDIS_ADDR", "redis:6379")
	productionMode, err := envBool("PRODUCTION_MODE", false)
	if err != nil {
		return Config{}, err
	}
	redisTLS, err := envBool("REDIS_TLS_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	useRedis, err := envBool("USE_REDIS", os.Getenv("REDIS_ADDR") != "")
	if err != nil {
		return Config{}, err
	}
	pluginDatabaseWrites, err := envBool("PLUGIN_DATABASE_WRITES_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	otelEnabled, err := envBool("OTEL_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	otlpInsecure, err := envBool("OTEL_EXPORTER_OTLP_INSECURE", true)
	if err != nil {
		return Config{}, err
	}
	stackEnabled, err := envBool("OBSERVABILITY_STACK_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := envDuration("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	maxBodyBytes, err := envInt64("MAX_BODY_BYTES", 1<<20)
	if err != nil {
		return Config{}, err
	}
	pluginMaxBytes, err := envInt64("PLUGIN_MAX_BYTES", 20<<20)
	if err != nil {
		return Config{}, err
	}
	observabilityFileBytes, err := envInt64("OBSERVABILITY_FILE_MAX_BYTES", 16<<20)
	if err != nil {
		return Config{}, err
	}
	redisDB, err := envInt("REDIS_DB", 0)
	if err != nil {
		return Config{}, err
	}
	observabilityMaxLogs, err := envInt("OBSERVABILITY_MAX_LOGS", 5000)
	if err != nil {
		return Config{}, err
	}
	observabilityMaxTraces, err := envInt("OBSERVABILITY_MAX_TRACES", 2000)
	if err != nil {
		return Config{}, err
	}

	allowInternalPlaintext, err := envBool("ALLOW_INTERNAL_PLAINTEXT", false)
	if err != nil {
		return Config{}, err
	}
	postgresDSN := secrets["POSTGRES_DSN"]
	if postgresDSN == "" && secrets["POSTGRES_PASSWORD"] != "" {
		port, err := envInt("POSTGRES_PORT", 5432)
		if err != nil || port < 1 || port > 65535 {
			return Config{}, errors.New("POSTGRES_PORT must be 1..65535")
		}
		u := url.URL{Scheme: "postgres", Host: net.JoinHostPort(env("POSTGRES_HOST", "postgres"), strconv.Itoa(port)), Path: "/" + env("POSTGRES_DB", "api_manager"), User: url.UserPassword(env("POSTGRES_USER", "api_manager"), secrets["POSTGRES_PASSWORD"])}
		query := url.Values{"sslmode": {env("POSTGRES_SSLMODE", "verify-full")}}
		u.RawQuery = query.Encode()
		postgresDSN = u.String()
	}

	userSessionTTL, err := envDuration("USER_SESSION_TTL", 12*time.Hour)
	if err != nil {
		return Config{}, err
	}

	return Config{
		UpstreamCredentials:       secrets["API_UPSTREAM_CREDENTIALS"],
		HTTPAddr:                  env("HTTP_ADDR", ":8080"),
		PublicAPIBaseURL:          env("PUBLIC_API_BASE_URL", ""),
		PublicHTTPAddr:            env("PUBLIC_HTTP_ADDR", ""),
		PublicUIDir:               env("PUBLIC_UI_DIR", "/usr/share/api-manager/public-ui"),
		AdminUsername:             env("ADMIN_USERNAME", "admin"),
		AdminPassword:             secrets["ADMIN_PASSWORD"],
		UserSessionTTL:            userSessionTTL,
		ProductionMode:            productionMode,
		AllowInternalPlaintext:    allowInternalPlaintext,
		MetricsToken:              secrets["METRICS_TOKEN"],
		ShutdownTimeout:           shutdownTimeout,
		MaxBodyBytes:              maxBodyBytes,
		LogLevel:                  env("LOG_LEVEL", "info"),
		PostgresDSN:               postgresDSN,
		RedisAddr:                 redisAddr,
		RedisPassword:             secrets["REDIS_PASSWORD"],
		RedisUsername:             os.Getenv("REDIS_USERNAME"),
		RedisTLS:                  redisTLS,
		RedisDB:                   redisDB,
		UseRedis:                  useRedis,
		PluginDir:                 env("PLUGIN_DIR", "plugins"),
		PluginMaxBytes:            pluginMaxBytes,
		PluginDatabaseWrites:      pluginDatabaseWrites,
		PluginLibraryDir:          env("PLUGIN_LIBRARY_DIR", "plugin-library"),
		CredentialEncryptionKey:   secrets["CREDENTIAL_ENCRYPTION_KEY"],
		CORSOrigins:               env("CORS_ORIGINS", ""),
		OTELEnabled:               otelEnabled,
		OTELServiceName:           env("OTEL_SERVICE_NAME", "api-manager"),
		OTLPEndpoint:              env("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTLPInsecure:              otlpInsecure,
		ObservabilityDir:          env("OBSERVABILITY_DIR", "data/observability"),
		ObservabilityStackEnabled: stackEnabled,
		GrafanaAdminPassword:      secrets["GRAFANA_ADMIN_PASSWORD"],
		ObservabilityMaxLogs:      observabilityMaxLogs,
		ObservabilityMaxTraces:    observabilityMaxTraces,
		ObservabilityFileBytes:    observabilityFileBytes,
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
	if !filepath.IsAbs(file) || filepath.Base(file) == "." {
		return "", fmt.Errorf("%s_FILE must be an absolute file path", name)
	}
	root, err := os.OpenRoot(filepath.Dir(file))
	if err != nil {
		return "", fmt.Errorf("read %s_FILE: %w", name, err)
	}
	defer root.Close()
	secretFile, err := root.Open(filepath.Base(file))
	if err != nil {
		return "", fmt.Errorf("read %s_FILE: %w", name, err)
	}
	defer secretFile.Close()
	info, err := secretFile.Stat()
	if err != nil {
		return "", fmt.Errorf("read %s_FILE: %w", name, err)
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return "", fmt.Errorf("%s_FILE must be a regular file of at most 64 KiB", name)
	}
	contents, err := io.ReadAll(io.LimitReader(secretFile, (64<<10)+1))
	if err != nil || len(contents) > 64<<10 {
		return "", fmt.Errorf("read %s_FILE: invalid or oversized secret", name)
	}
	result := strings.TrimRight(string(contents), "\r\n")
	if result == "" || strings.ContainsRune(result, 0) {
		return "", fmt.Errorf("%s_FILE must contain a non-empty text value", name)
	}
	return result, nil
}

func (c Config) Validate() error {
	forbidden := map[string]bool{"change-me-in-production": true, "change-me": true, "replace-with-a-long-random-secret": true}
	for _, item := range []struct{ name, value string }{{"CREDENTIAL_ENCRYPTION_KEY", c.CredentialEncryptionKey}} {
		if len(strings.TrimSpace(item.value)) < 32 || forbidden[item.value] || strings.HasPrefix(item.value, "generate-") || strings.HasPrefix(item.value, "replace-") {
			return errors.New(item.name + " must be a distinct random secret of at least 32 characters")
		}
	}
	if c.ProductionMode {
		if c.OTELEnabled && (c.OTLPEndpoint == "" || (c.OTLPInsecure && !(c.ObservabilityStackEnabled && c.OTLPEndpoint == "127.0.0.1:4317"))) {
			return errors.New("production tracing requires a configured OTLP endpoint and TLS")
		}
		if c.PostgresDSN == "" || !c.UseRedis || c.RedisAddr == "" {
			return errors.New("production requires PostgreSQL and Redis; in-memory fallback is forbidden")
		}
		postgresURL, err := url.Parse(c.PostgresDSN)
		if err != nil || (postgresURL.Scheme != "postgres" && postgresURL.Scheme != "postgresql") || postgresURL.Hostname() == "" {
			return errors.New("production requires a valid PostgreSQL URL")
		}
		if len(postgresURL.Query()["sslmode"]) != 1 {
			return errors.New("POSTGRES_DSN must have exactly one sslmode")
		}
		password, hasPassword := "", false
		if postgresURL.User != nil {
			password, hasPassword = postgresURL.User.Password()
		}
		internalPostgres := c.AllowInternalPlaintext && postgresURL.Host == "postgres:5432" && postgresURL.Query().Get("sslmode") == "disable" && hasPassword && len(password) >= 32
		if postgresURL.Query().Get("sslmode") != "verify-full" && !internalPostgres {
			return errors.New("external PostgreSQL requires sslmode=verify-full; plaintext is only allowed for the private Compose postgres service")
		}
		internalRedis := c.AllowInternalPlaintext && c.RedisAddr == "redis:6379"
		if (!c.RedisTLS && !internalRedis) || len(c.RedisPassword) < 32 {
			return errors.New("Redis requires a 32-byte password and TLS unless it is the private Compose redis service")
		}
		if c.RedisPassword == c.CredentialEncryptionKey {
			return errors.New("Redis password must be distinct from application secrets")
		}
		for _, origin := range strings.Split(c.CORSOrigins, ",") {
			if strings.TrimSpace(origin) == "*" {
				return errors.New("production CORS_ORIGINS cannot include a wildcard")
			}
		}
		if len(c.MetricsToken) < 32 || c.MetricsToken == c.RedisPassword || c.MetricsToken == c.CredentialEncryptionKey {
			return errors.New("production requires a distinct METRICS_TOKEN of at least 32 characters")
		}
	}
	if c.ObservabilityStackEnabled && (len(c.GrafanaAdminPassword) < 32 || c.GrafanaAdminPassword == c.CredentialEncryptionKey || c.GrafanaAdminPassword == c.RedisPassword || c.GrafanaAdminPassword == c.MetricsToken) {
		return errors.New("the full observability stack requires a distinct GRAFANA_ADMIN_PASSWORD of at least 32 characters")
	}
	if c.PublicHTTPAddr != "" {
		host, port, err := net.SplitHostPort(c.PublicHTTPAddr)
		if err != nil || (host != "" && net.ParseIP(host) == nil && !strings.Contains(host, ".")) || port == "" {
			return errors.New("PUBLIC_HTTP_ADDR must be a valid host:port address")
		}
		if c.PublicUIDir == "" || filepath.IsAbs(c.PublicUIDir) == false {
			return errors.New("PUBLIC_UI_DIR must be an absolute directory")
		}
	}
	if c.UserSessionTTL < time.Second || c.UserSessionTTL > 24*time.Hour {
		return errors.New("USER_SESSION_TTL must be between 1 second and 24 hours")
	}
	if c.AdminPassword != "" && (len(c.AdminPassword) < 12 || len(c.AdminPassword) > 72 || c.AdminPassword == c.CredentialEncryptionKey || c.AdminPassword == c.RedisPassword || c.AdminPassword == c.MetricsToken) {
		return errors.New("ADMIN_PASSWORD must be a distinct 12..72 byte password")
	}

	if c.ShutdownTimeout < time.Second || c.ShutdownTimeout > 5*time.Minute {
		return errors.New("SHUTDOWN_TIMEOUT must be between 1 second and 5 minutes")
	}
	if c.MaxBodyBytes < 1024 || c.MaxBodyBytes > 100<<20 {
		return errors.New("MAX_BODY_BYTES must be between 1 KiB and 100 MiB")
	}
	if c.PluginMaxBytes < 1<<20 || c.PluginMaxBytes > 100<<20 {
		return errors.New("PLUGIN_MAX_BYTES must be between 1 MiB and 100 MiB")
	}
	if c.RedisDB < 0 || c.RedisDB > 1024 {
		return errors.New("REDIS_DB must be between 0 and 1024")
	}
	if c.LogLevel != "debug" && c.LogLevel != "info" && c.LogLevel != "warn" && c.LogLevel != "error" {
		return errors.New("LOG_LEVEL must be debug, info, warn, or error")
	}
	if strings.TrimSpace(c.OTELServiceName) == "" {
		return errors.New("OTEL_SERVICE_NAME must not be empty")
	}
	if c.ObservabilityMaxLogs < 100 || c.ObservabilityMaxLogs > 100000 {
		return errors.New("OBSERVABILITY_MAX_LOGS must be between 100 and 100000")
	}
	if c.ObservabilityMaxTraces < 100 || c.ObservabilityMaxTraces > 50000 {
		return errors.New("OBSERVABILITY_MAX_TRACES must be between 100 and 50000")
	}
	if c.ObservabilityFileBytes < 1<<20 || c.ObservabilityFileBytes > 1<<30 {
		return errors.New("OBSERVABILITY_FILE_MAX_BYTES must be between 1 MiB and 1 GiB")
	}
	return nil
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid duration for %s: %w", key, err)
	}
	return parsed, nil
}

func envInt64(key string, fallback int64) (int64, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid integer for %s: %w", key, err)
	}
	return parsed, nil
}

func envInt(key string, fallback int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid integer for %s: %w", key, err)
	}
	return parsed, nil
}

func envBool(key string, fallback bool) (bool, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid boolean for %s: %w", key, err)
	}
	return parsed, nil
}
