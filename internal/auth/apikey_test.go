package auth

import (
	"api-manager/internal/model"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestHMACTimestampCannotOverflowAgeCheck(t *testing.T) {
	secret := strings.Repeat("s", 32)
	t.Setenv("API_AUTH_TIMESTAMP_TEST", secret)
	api := model.API{AuthConfig: map[string]string{"secret_env": "API_AUTH_TIMESTAMP_TEST"}}
	now := time.Now().Unix()
	for _, timestamp := range []int64{now, now - 601, now + 601, math.MinInt64, math.MaxInt64, math.MinInt64 + now} {
		t.Run(strconv.FormatInt(timestamp, 10), func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://example.com/api/test?x=1", strings.NewReader("body"))
			stamp := strconv.FormatInt(timestamp, 10)
			nonce := "unique-nonce-123456"
			digest := sha256.Sum256([]byte("body"))
			message := stamp + "\n" + nonce + "\n" + r.Method + "\n" + r.URL.RequestURI() + "\n" + hex.EncodeToString(digest[:])
			mac := hmac.New(sha256.New, []byte(secret))
			_, _ = mac.Write([]byte(message))
			r.Header.Set("X-Timestamp", stamp)
			r.Header.Set("X-Nonce", nonce)
			r.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
			err := validateHMAC(api, r)
			if timestamp == now && err != nil {
				t.Fatalf("current timestamp rejected: %v", err)
			}
			if timestamp != now && !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("out-of-window timestamp accepted: %d, %v", timestamp, err)
			}
		})
	}
}
