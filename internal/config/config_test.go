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
		"ADMIN_BOOTSTRAP_KEY", "ADMIN_BOOTSTRAP_KEY_FILE", "ADMIN_PASSWORD", "ADMIN_PASSWORD_FILE", "USER_SESSION_TTL", "SMTP_PASSWORD", "SMTP_PASSWORD_FILE", "SMTP_HOST", "SMTP_PORT", "SMTP_MODE", "SMTP_FROM", "SMTP_USERNAME", "PASSWORD_RESET_BASE_URL",
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

func TestBootstrapKeyHasIndependent32ByteMinimum(t *testing.T) {
	setValidTestEnvironment(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key   string
		valid bool
	}{{strings.Repeat("b", 32), true}, {strings.Repeat("b", 31), false}, {cfg.CredentialEncryptionKey, false}} {
		cfg.AdminBootstrapKey = tc.key
		if err := cfg.Validate(); (err == nil) != tc.valid {
			t.Errorf("bootstrap key validation: %v", err)
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

func TestOptionalSMTPPasswordFileMayBeEmpty(t *testing.T) {
	setValidTestEnvironment(t)
	file := filepath.Join(t.TempDir(), "smtp_password")
	if err := os.WriteFile(file, nil, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SMTP_PASSWORD_FILE", file)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SMTPPassword != "" {
		t.Fatal("empty SMTP secret altered")
	}
	t.Setenv("ADMIN_BOOTSTRAP_KEY_FILE", file)
	if _, err := Load(); err == nil {
		t.Fatal("empty mandatory bootstrap key secret accepted")
	}
}

func TestOptionalVersionCheckTokenLoadsOnlyFromSecret(t *testing.T) {
	setValidTestEnvironment(t)
	t.Setenv("GITHUB_UPDATE_TOKEN", "")
	path := filepath.Join(t.TempDir(), "github_update_token")
	if err := os.WriteFile(path, []byte("readonly-test-token\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_UPDATE_TOKEN_FILE", path)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitHubUpdateToken != "readonly-test-token" {
		t.Fatal("optional version credential not loaded")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o400); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil || cfg.GitHubUpdateToken != "" {
		t.Fatal("empty optional token should be accepted", err)
	}
}

func TestTrustedProxyConfigurationRejectsWildcardsAndInvalidCIDRs(t *testing.T) {
	setValidTestEnvironment(t)
	for _, value := range []string{"0.0.0.0/0", "::/0", "not-an-ip"} {
		t.Setenv("TRUSTED_PROXY_CIDRS", value)
		if _, err := Load(); err == nil {
			t.Fatal("unsafe proxy trust", value)
		}
	}
	t.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.2/32, 2001:db8::1/128")
	cfg, err := Load()
	if err != nil || len(cfg.TrustedProxyCIDRs) != 2 {
		t.Fatal(cfg.TrustedProxyCIDRs, err)
	}
}
