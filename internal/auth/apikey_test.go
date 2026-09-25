package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthSecretIsLimitedToAPIAuthNamespace(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", strings.Repeat("a", 32))
	if _, err := authSecret("ADMIN_TOKEN"); err == nil {
		t.Fatal("platform secret name was accepted")
	}
}

func TestAuthSecretReadsFileSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jwt-secret")
	if err := os.WriteFile(path, []byte(strings.Repeat("r", 32)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("API_AUTH_TEST", "")
	t.Setenv("API_AUTH_TEST_FILE", path)
	value, err := authSecret("API_AUTH_TEST")
	if err != nil {
		t.Fatal(err)
	}
	if value != strings.Repeat("r", 32) {
		t.Fatalf("value = %q", value)
	}
}

func TestAuthSecretRejectsAmbiguousSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("file-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("API_AUTH_TEST", "environment-secret")
	t.Setenv("API_AUTH_TEST_FILE", path)
	if _, err := authSecret("API_AUTH_TEST"); err == nil {
		t.Fatal("ambiguous auth secret sources were accepted")
	}
}

func TestAuthSecretRejectsWeakValue(t *testing.T) {
	t.Setenv("API_AUTH_WEAK", "short-secret")
	if _, err := authSecret("API_AUTH_WEAK"); err == nil {
		t.Fatal("weak auth secret was accepted")
	}
}
