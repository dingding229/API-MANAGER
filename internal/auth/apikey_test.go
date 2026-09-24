package auth

import (
	"testing"
	"time"

	"api-manager/internal/model"
	"api-manager/internal/store"
)

func TestValidateAPIKey(t *testing.T) {
	s := store.NewMemory()
	key := "ak_test-secret"
	if err := s.CreateCredential(model.Credential{
		ID:        "cred_test",
		Name:      "test",
		Prefix:    "ak_test",
		Hash:      HashAPIKey(key),
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	if _, ok := ValidateAPIKey(s, key); !ok {
		t.Fatal("expected valid API key")
	}
	if _, ok := ValidateAPIKey(s, "wrong"); ok {
		t.Fatal("expected invalid API key")
	}
}

func TestCredentialSecretRoundTrip(t *testing.T) {
	encoded, err := EncryptSecret("admin-secret", "ak_test-secret")
	if err != nil {
		t.Fatal(err)
	}
	if encoded == "ak_test-secret" || encoded == "" {
		t.Fatal("secret was not encrypted")
	}
	decoded, err := DecryptSecret("admin-secret", encoded)
	if err != nil || decoded != "ak_test-secret" {
		t.Fatalf("decrypt: %q %v", decoded, err)
	}
	if _, err := DecryptSecret("wrong-secret", encoded); err == nil {
		t.Fatal("accepted wrong encryption key")
	}
}
