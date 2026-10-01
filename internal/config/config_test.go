package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setValidTestEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"ADMIN_PASSWORD", "ADMIN_PASSWORD_FILE", "USER_SESSION_TTL", "SMTP_PASSWORD", "SMTP_PASSWORD_FILE", "SMTP_HOST", "SMTP_PORT", "SMTP_MODE", "SMTP_FROM", "SMTP_USERNAME", "PASSWORD_RESET_BASE_URL",
		"CREDENTIAL_ENCRYPTION_KEY", "CREDENTIAL_ENCRYPTION_KEY_FILE", "POSTGRES_DSN", "POSTGRES_DSN_FILE",
		"REDIS_PASSWORD", "REDIS_PASSWORD_FILE", "API_UPSTREAM_CREDENTIALS", "API_UPSTREAM_CREDENTIALS_FILE",
		"METRICS_TOKEN", "METRICS_TOKEN_FILE",
		"PRODUCTION_MODE", "USE_REDIS", "REDIS_TLS_ENABLED", "OBSERVABILITY_STACK_ENABLED",
		"PLUGIN_DATABASE_WRITES_ENABLED", "OTEL_ENABLED", "OTEL_EXPORTER_OTLP_INSECURE",
		"SHUTDOWN_TIMEOUT", "MAX_BODY_BYTES", "PLUGIN_MAX_BYTES",
		"OBSERVABILITY_FILE_MAX_BYTES", "REDIS_DB", "OBSERVABILITY_MAX_LOGS", "OBSERVABILITY_MAX_TRACES",
		"REDIS_ADDR", "POSTGRES_PASSWORD", "POSTGRES_PASSWORD_FILE", "ALLOW_INTERNAL_PLAINTEXT", "POSTGRES_SSLMODE",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", strings.Repeat("c", 32))
}

func TestLoadRejectsMalformedTypedEnvironment(t *testing.T) {
	cases := []struct{ name, value string }{
		{"PRODUCTION_MODE", "sometimes"},
		{"PLUGIN_DATABASE_WRITES_ENABLED", "sometimes"},
		{"MAX_BODY_BYTES", "one-megabyte"},
		{"REDIS_DB", "zero"},
		{"SHUTDOWN_TIMEOUT", "soon"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setValidTestEnvironment(t)
			t.Setenv(tc.name, tc.value)
			if _, err := Load(); err == nil {
				t.Fatalf("Load accepted malformed %s", tc.name)
			}
		})
	}
}

func TestValidateRejectsUnsafeResourceLimits(t *testing.T) {
	setValidTestEnvironment(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxBodyBytes = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted an unsafe body limit")
	}
}

func TestSecretReadsKubernetesStyleRelativeSymlinks(t *testing.T) {
	root := t.TempDir()
	version := filepath.Join(root, "..2026_09_25_00_00_00")
	if err := os.Mkdir(version, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(version, "METRICS_TOKEN"), []byte(strings.Repeat("k", 32)+"\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(version), filepath.Join(root, "..data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..data/METRICS_TOKEN", filepath.Join(root, "METRICS_TOKEN")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("METRICS_TOKEN", "")
	t.Setenv("METRICS_TOKEN_FILE", filepath.Join(root, "METRICS_TOKEN"))
	value, err := secret("METRICS_TOKEN")
	if err != nil {
		t.Fatalf("read projected secret: %v", err)
	}
	if value != strings.Repeat("k", 32) {
		t.Fatal("projected secret value did not match")
	}
}

func TestPrivateComposeDatastoresAreTheOnlyPlaintextException(t *testing.T) {
	setValidTestEnvironment(t)
	t.Setenv("PRODUCTION_MODE", "true")
	t.Setenv("USE_REDIS", "true")
	t.Setenv("REDIS_PASSWORD", strings.Repeat("r", 64))
	t.Setenv("METRICS_TOKEN", strings.Repeat("m", 64))
	t.Setenv("POSTGRES_PASSWORD", strings.Repeat("p", 64))
	t.Setenv("POSTGRES_SSLMODE", "disable")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("ALLOW_INTERNAL_PLAINTEXT", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err = cfg.Validate(); err != nil {
		t.Fatalf("private Compose config rejected: %v", err)
	}
	cfg.AllowInternalPlaintext = false
	if cfg.Validate() == nil {
		t.Fatal("plaintext allowed without opt-in")
	}
	cfg.AllowInternalPlaintext = true
	cfg.RedisAddr = "redis.example.com:6379"
	if cfg.Validate() == nil {
		t.Fatal("external Redis plaintext allowed")
	}
	cfg.RedisAddr = "redis:6379"
	cfg.PostgresDSN = "postgres://api_manager:" + strings.Repeat("p", 64) + "@db.example.com:5432/api_manager?sslmode=disable"
	if cfg.Validate() == nil {
		t.Fatal("external PostgreSQL plaintext allowed")
	}
}

func TestAdminPasswordUsesEightByteMinimum(t *testing.T) {
	setValidTestEnvironment(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		password string
		valid    bool
	}{
		{"12345678", true}, {"密码12", true}, {"1234567", false}, {"密1234", false}, {strings.Repeat("a", 73), false},
	} {
		cfg.AdminPassword = tc.password
		if err := cfg.Validate(); (err == nil) != tc.valid {
			t.Errorf("%d-byte password: %v", len(tc.password), err)
		}
	}
}

func TestSMTPPortIsValidatedAtLoad(t *testing.T) {
	setValidTestEnvironment(t)
	t.Setenv("SMTP_PORT", "abc")
	if _, err := Load(); err == nil {
		t.Fatal("invalid SMTP port accepted")
	}
}
