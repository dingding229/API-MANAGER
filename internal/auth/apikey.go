package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"api-manager/internal/model"
	"github.com/golang-jwt/jwt/v5"
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
	if !ok || credential.Revoked {
		return model.Credential{}, false
	}
	if credential.ExpiresAt != nil && time.Now().After(*credential.ExpiresAt) {
		return model.Credential{}, false
	}
	if subtle.ConstantTimeCompare([]byte(credential.Hash), []byte(candidate)) != 1 {
		return model.Credential{}, false
	}
	return credential, true
}

func Authorize(api model.API, s interface {
	FindCredentialByHash(string) (model.Credential, bool)
}, r *http.Request) error {
	switch strings.ToLower(api.AuthMode) {
	case "", "none":
		return nil
	case "api_key":
		key := r.Header.Get("X-API-Key")
		if key == "" {
			key = ExtractAPIKey(r.Header.Get("Authorization"))
		}
		if _, valid := ValidateAPIKey(s, key); !valid {
			return ErrUnauthorized
		}
		return nil
	case "jwt":
		return validateJWT(api, r)
	case "hmac":
		return validateHMAC(api, r)
	default:
		return fmt.Errorf("unsupported auth mode: %s", api.AuthMode)
	}
}

func StatusCode(err error) int {
	if errors.Is(err, ErrUnauthorized) {
		return http.StatusUnauthorized
	}
	if errors.Is(err, ErrForbidden) {
		return http.StatusForbidden
	}
	return http.StatusInternalServerError
}

func validateJWT(api model.API, r *http.Request) error {
	secretEnv := configValue(api.AuthConfig, "secret_env", "JWT_HS256_SECRET")
	secret := os.Getenv(secretEnv)
	if secret == "" {
		return fmt.Errorf("jwt secret is not configured")
	}
	tokenString := ExtractAPIKey(r.Header.Get("Authorization"))
	if tokenString == "" {
		return ErrUnauthorized
	}
	options := []jwt.ParserOption{jwt.WithExpirationRequired(), jwt.WithIssuedAt()}
	if configValue(api.AuthConfig, "issuer", "") == "" || configValue(api.AuthConfig, "audience", "") == "" {
		return fmt.Errorf("jwt issuer and audience are required")
	}
	if issuer := configValue(api.AuthConfig, "issuer", ""); issuer != "" {
		options = append(options, jwt.WithIssuer(issuer))
	}
	if audience := configValue(api.AuthConfig, "audience", ""); audience != "" {
		options = append(options, jwt.WithAudience(audience))
	}
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected jwt signing method")
		}
		return []byte(secret), nil
	}, options...)
	if err != nil || !token.Valid {
		return ErrUnauthorized
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return ErrUnauthorized
	}
	issued, err := claims.GetIssuedAt()
	if err != nil || issued == nil {
		return ErrUnauthorized
	}
	expires, err := claims.GetExpirationTime()
	if err != nil || expires == nil || expires.Time.Sub(issued.Time) > 24*time.Hour || expires.Time.Before(issued.Time) {
		return ErrUnauthorized
	}
	return nil
}

func validateHMAC(api model.API, r *http.Request) error {
	secretEnv := configValue(api.AuthConfig, "secret_env", "")
	if secretEnv == "" {
		return fmt.Errorf("hmac secret_env is not configured")
	}
	secret := os.Getenv(secretEnv)
	if secret == "" {
		return fmt.Errorf("hmac secret is not configured")
	}
	timestampHeader := configValue(api.AuthConfig, "timestamp_header", "X-Timestamp")
	signatureHeader := configValue(api.AuthConfig, "signature_header", "X-Signature")
	timestamp := r.Header.Get(timestampHeader)
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || abs(time.Now().Unix()-unix) > 300 {
		return ErrUnauthorized
	}
	nonce := r.Header.Get("X-Nonce")
	if len(nonce) < 16 || len(nonce) > 128 || strings.ContainsAny(nonce, "\r\n\t ") {
		return ErrUnauthorized
	}
	// Hash and restore the body so request validation and upstream still receive it.
	if r.Body == nil {
		r.Body = http.NoBody
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return ErrUnauthorized
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	digest := sha256.Sum256(body)
	message := timestamp + "\n" + nonce + "\n" + r.Method + "\n" + r.URL.RequestURI() + "\n" + hex.EncodeToString(digest[:])
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(message))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(strings.ToLower(r.Header.Get(signatureHeader))), []byte(expected)) {
		return ErrUnauthorized
	}
	return nil
}

func configValue(values map[string]string, key, fallback string) string {
	if value := values[key]; value != "" {
		return value
	}
	return fallback
}

func abs(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
