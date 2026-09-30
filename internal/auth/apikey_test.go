package auth

import (
	"api-manager/internal/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type keyStore struct{ credential model.Credential }

func (s keyStore) FindCredentialByHash(hash string) (model.Credential, bool) {
	return s.credential, s.credential.Hash == hash
}
func TestOnlyKeysAuthorizeBusinessRoutes(t *testing.T) {
	key := strings.Repeat("k", 64)
	s := keyStore{model.Credential{Hash: HashAPIKey(key)}}
	for _, mode := range []string{"api_key", "", "none", "jwt", "hmac"} {
		r := httptest.NewRequest(http.MethodGet, "http://example.com/api/test", nil)
		r.Header.Set("X-API-Key", key)
		err := Authorize(model.API{AuthMode: mode}, s, r)
		if (err == nil) != (mode == "api_key") {
			t.Fatalf("mode=%q error=%v", mode, err)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "http://example.com/api/test", nil)
	if Authorize(model.API{AuthMode: "api_key"}, s, r) == nil {
		t.Fatal("missing KEY accepted")
	}
}
func TestRequestKeyRejectsAmbiguousHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	r.Header.Set("X-API-Key", "key")
	r.Header.Set("Authorization", "Bearer key")
	if RequestKey(r) != "" {
		t.Fatal("ambiguous credentials accepted")
	}
	r.Header.Del("Authorization")
	r.Header.Add("X-API-Key", "key2")
	if RequestKey(r) != "" {
		t.Fatal("duplicate credentials accepted")
	}
}
func TestExpiredAndRevokedKeysAreRejected(t *testing.T) {
	key := strings.Repeat("k", 64)
	expired := time.Now().Add(-time.Second)
	for _, c := range []model.Credential{{Hash: HashAPIKey(key), Revoked: true}, {Hash: HashAPIKey(key), ExpiresAt: &expired}} {
		if _, valid := ValidateAPIKey(keyStore{c}, key); valid {
			t.Fatal("invalid key accepted")
		}
	}
}
