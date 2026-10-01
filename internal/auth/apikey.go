package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"api-manager/internal/model"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
)

func ExtractAPIKey(value string) string {
	if strings.HasPrefix(value, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
	}
	return strings.TrimSpace(value)
}

// RequestKey accepts a single API Key source. Bearer is only a transport for an
// opaque key, not a signed token. Ambiguous or duplicate headers fail closed.
func RequestKey(r *http.Request) string {
	keys, authorizations := r.Header.Values("X-API-Key"), r.Header.Values("Authorization")
	if len(keys) > 1 || len(authorizations) > 1 || (len(keys) > 0 && len(authorizations) > 0) {
		return ""
	}
	if len(keys) == 1 {
		return strings.TrimSpace(keys[0])
	}
	if len(authorizations) == 1 && strings.HasPrefix(authorizations[0], "Bearer ") {
		return ExtractAPIKey(authorizations[0])
	}
	return ""
}

func MatchesKey(expected, provided string) bool {
	return len(expected) >= 32 && provided != "" && subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}

func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func ValidateAPIKey(s interface {
	FindCredentialByHash(string) (model.Credential, bool)
}, key string) (model.Credential, bool) {
	if key == "" {
		return model.Credential{}, false
	}
	candidate := HashAPIKey(key)
	credential, ok := s.FindCredentialByHash(candidate)
	if !ok || credential.Revoked || (credential.ExpiresAt != nil && !time.Now().Before(*credential.ExpiresAt)) {
		return model.Credential{}, false
	}
	return credential, subtle.ConstantTimeCompare([]byte(credential.Hash), []byte(candidate)) == 1
}

func Authorize(api model.API, s interface {
	FindCredentialByHash(string) (model.Credential, bool)
}, r *http.Request) error {
	// Only explicitly public routes bypass authentication.
	if api.AuthMode == "none" {
		return nil
	}
	if api.AuthMode != "api_key" {
		return ErrUnauthorized
	}
	if _, valid := ValidateAPIKey(s, RequestKey(r)); !valid {
		return ErrUnauthorized
	}
	return nil
}

func StatusCode(err error) int {
	if errors.Is(err, ErrForbidden) {
		return http.StatusForbidden
	}
	if errors.Is(err, ErrUnauthorized) {
		return http.StatusUnauthorized
	}
	return http.StatusInternalServerError
}
