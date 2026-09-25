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
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"api-manager/internal/model"
	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrUnauthorized      = errors.New("unauthorized")
	ErrForbidden         = errors.New("forbidden")
	authSecretEnv        = regexp.MustCompile(`^API_AUTH_[A-Z0-9_]{1,64}$`)
	authSecretFileValues sync.Map
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
	secretEnv := configValue(api.AuthConfig, "secret_env", "API_AUTH_JWT_HS256_SECRET")
	if !authSecretEnv.MatchString(secretEnv) {
		return fmt.Errorf("jwt secret_env must use the API_AUTH_ namespace")
	}
	secret, err := authSecret(secretEnv)
	if err != nil {
		return fmt.Errorf("jwt secret is not configured: %w", err)
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
	if !authSecretEnv.MatchString(secretEnv) {
		return fmt.Errorf("hmac secret_env must use the API_AUTH_ namespace")
	}
	secret, err := authSecret(secretEnv)
	if err != nil {
		return fmt.Errorf("hmac secret is not configured: %w", err)
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

func authSecret(name string) (string, error) {
	if !authSecretEnv.MatchString(name) {
		return "", errors.New("invalid auth secret name")
	}
	value := os.Getenv(name)
	path := os.Getenv(name + "_FILE")
	if value != "" && path != "" {
		return "", errors.New("secret has multiple sources")
	}
	if value != "" {
		return validateAuthSecretValue(value)
	}
	if path == "" {
		return "", errors.New("secret source is empty")
	}
	if !filepath.IsAbs(path) || filepath.Base(path) == "." {
		return "", errors.New("secret file path must be absolute")
	}
	cacheKey := name + "\x00" + path
	if cached, ok := authSecretFileValues.Load(cacheKey); ok {
		return cached.(string), nil
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return "", errors.New("secret file is unavailable")
	}
	defer root.Close()
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return "", errors.New("secret file is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return "", errors.New("secret file is invalid")
	}
	contents, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(contents) > 64<<10 {
		return "", errors.New("secret file is invalid")
	}
	value = strings.TrimRight(string(contents), "\r\n")
	value, err = validateAuthSecretValue(value)
	if err != nil {
		return "", errors.New("secret file is invalid")
	}
	actual, _ := authSecretFileValues.LoadOrStore(cacheKey, value)
	return actual.(string), nil
}

func validateAuthSecretValue(value string) (string, error) {
	if len(value) < 32 || len(value) > 64<<10 || strings.ContainsRune(value, 0) {
		return "", errors.New("secret must contain 32 to 65536 bytes of text")
	}
	return value, nil
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
