package auth

import (
	"api-manager/internal/model"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"github.com/golang-jwt/jwt/v5"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestHMACBindsBodyAndNonce(t *testing.T) {
	t.Setenv("TEST_HMAC_SECRET", "some-private-signing-secret")
	api := model.API{AuthMode: "hmac", AuthConfig: map[string]string{"secret_env": "TEST_HMAC_SECRET"}}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "unique-nonce-12345678"
	body := []byte(`{"price":1}`)
	digest := sha256.Sum256(body)
	message := timestamp + "\n" + nonce + "\nPOST\n/api/pay\n" + hex.EncodeToString(digest[:])
	mac := hmac.New(sha256.New, []byte(os.Getenv("TEST_HMAC_SECRET")))
	mac.Write([]byte(message))
	request := func(body []byte) *http.Request {
		r := httptest.NewRequest("POST", "/api/pay", bytes.NewReader(body))
		r.Header.Set("X-Timestamp", timestamp)
		r.Header.Set("X-Nonce", nonce)
		r.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
		return r
	}
	if err := validateHMAC(api, request(body)); err != nil {
		t.Fatalf("valid signature: %v", err)
	}
	if err := validateHMAC(api, request([]byte(`{"price":999}`))); err == nil {
		t.Fatal("signature accepted changed body")
	}
}

func TestJWTRequiresExpiryAndAudience(t *testing.T) {
	t.Setenv("TEST_JWT_SECRET", "some-private-jwt-secret")
	api := model.API{AuthMode: "jwt", AuthConfig: map[string]string{"secret_env": "TEST_JWT_SECRET", "issuer": "issuer", "audience": "client"}}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": "issuer", "aud": "client", "iat": time.Now().Unix()})
	encoded, err := token.SignedString([]byte(os.Getenv("TEST_JWT_SECRET")))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/test", nil)
	r.Header.Set("Authorization", "Bearer "+encoded)
	if err := validateJWT(api, r); err == nil {
		t.Fatal("accepted token without exp")
	}
}
