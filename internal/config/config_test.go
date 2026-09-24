package config

import (
	"os"
	"testing"
	"time"
)

func TestSecretsMustBeExplicitAndDistinct(t *testing.T) {
	for _, test := range []struct {
		name  string
		cfg   Config
		valid bool
	}{
		{"missing", Config{UserJWTTTL: 12 * time.Hour}, false},
		{"old defaults", Config{UserJWTTTL: 12 * time.Hour, AdminToken: "change-me-in-production", UserJWTSecret: "change-this-user-jwt-secret", CredentialEncryptionKey: "abcdef0123456789abcdef0123456789"}, false},
		{"shared keys", Config{UserJWTTTL: 12 * time.Hour, AdminToken: "abcdef0123456789abcdef0123456789", UserJWTSecret: "abcdef0123456789abcdef0123456789", CredentialEncryptionKey: "fedcba9876543210fedcba9876543210"}, false},
		{"distinct", Config{UserJWTTTL: 12 * time.Hour, AdminToken: "abcdef0123456789abcdef0123456789", UserJWTSecret: "0123456789abcdef0123456789abcdef", CredentialEncryptionKey: "fedcba9876543210fedcba9876543210"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.cfg.Validate() == nil; got != test.valid {
				t.Fatalf("valid=%t want %t", got, test.valid)
			}
		})
	}
}

func TestSecretFilesAndProductionGuard(t *testing.T) {
	path := t.TempDir() + "/admin"
	if err := os.WriteFile(path, []byte("a-long-random-admin-token-value-1234567890\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ADMIN_TOKEN", "")
	t.Setenv("ADMIN_TOKEN_FILE", path)
	t.Setenv("PRODUCTION_MODE", "tru")
	if _, err := Load(); err == nil {
		t.Fatal("invalid production flag must fail")
	}
	t.Setenv("PRODUCTION_MODE", "false")
	cfg, err := Load()
	if err != nil || cfg.AdminToken != "a-long-random-admin-token-value-1234567890" {
		t.Fatalf("load admin secret: %v", err)
	}
	t.Setenv("ADMIN_TOKEN", "conflicting-value")
	if _, err := Load(); err == nil {
		t.Fatal("conflicting environment and file must fail")
	}
	t.Setenv("ADMIN_TOKEN", "")
	t.Setenv("ADMIN_TOKEN_FILE", path+".missing")
	if _, err := Load(); err == nil {
		t.Fatal("missing secret file must fail")
	}
	cfg = Config{AdminToken: "a-long-random-admin-token-value-1234567890", UserJWTSecret: "a-long-random-jwt-secret-value-1234567890", CredentialEncryptionKey: "a-long-random-encryption-key-1234567890", UserJWTTTL: time.Hour, ProductionMode: true}
	if err := cfg.Validate(); err == nil {
		t.Fatal("production must reject in-memory state")
	}
	cfg.PostgresDSN, cfg.UseRedis, cfg.RedisAddr, cfg.RedisTLS, cfg.RedisPassword = "postgres://example/db?sslmode=verify-full", true, "redis:6379", true, "test-secret"
	if err := cfg.Validate(); err == nil {
		t.Fatal("production must protect metrics")
	}
	cfg.MetricsToken = "a-long-random-metrics-secret-1234567890"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.OTELEnabled = true
	cfg.OTLPEndpoint = "collector.internal:4317"
	cfg.OTLPInsecure = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("production tracing must use TLS")
	}
	cfg.OTLPInsecure = false
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.CORSOrigins = "https://console.example,*"
	if err := cfg.Validate(); err == nil {
		t.Fatal("production must reject wildcard CORS")
	}
}
