package gateway

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"api-manager/internal/plugin"
	"api-manager/internal/ratelimit"
	"api-manager/internal/store"
)

func TestExactRouteWinsOverPublicTemplate(t *testing.T) {
	s := store.NewMemory()
	public := publishedAPI("public", "GET", "/api/{id}")
	protected := publishedAPI("protected", "GET", "/api/abcd")
	protected.AuthMode = "api_key"
	if err := s.CreateAPI(public); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAPI(protected); err != nil {
		t.Fatal(err)
	}
	g := New(s, plugin.NewRegistry(), ratelimit.NewMemory(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest("GET", "/api/abcd", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("template route bypassed exact route: %d %s", w.Code, w.Body.String())
	}
}

func TestHMACNonceCannotBeReused(t *testing.T) {
	t.Setenv("TEST_ROUTE_HMAC_SECRET", "dedicated-secret-value")
	api := publishedAPI("hmac-api", "POST", "/api/signed")
	api.AuthMode = "hmac"
	api.AuthConfig = map[string]string{"secret_env": "TEST_ROUTE_HMAC_SECRET"}
	g := testGateway(t, api)
	nonce := "unique-hmac-nonce-123"
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	body := []byte(`{"message":"signed"}`)
	digest := sha256.Sum256(body)
	mac := hmac.New(sha256.New, []byte(os.Getenv("TEST_ROUTE_HMAC_SECRET")))
	mac.Write([]byte(timestamp + "\n" + nonce + "\nPOST\n/api/signed\n" + hex.EncodeToString(digest[:])))
	request := func() *http.Request {
		r := httptest.NewRequest("POST", "/api/signed", bytes.NewReader(body))
		r.Header.Set("X-Nonce", nonce)
		r.Header.Set("X-Timestamp", timestamp)
		r.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
		return r
	}
	first := httptest.NewRecorder()
	g.ServeHTTP(first, request())
	if first.Code != 200 {
		t.Fatalf("first request: %d %s", first.Code, first.Body.String())
	}
	again := httptest.NewRecorder()
	g.ServeHTTP(again, request())
	if again.Code != 401 {
		t.Fatalf("replayed nonce: %d %s", again.Code, again.Body.String())
	}
}
