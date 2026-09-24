package integration

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"api-manager/internal/auth"
	"api-manager/internal/gateway"
	"api-manager/internal/httpx"
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/ratelimit"
	"api-manager/internal/store"
	"api-manager/internal/upstream"
)

func TestGameDiscountProxyContract(t *testing.T) {
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-API-Key") != "service-secret" {
			t.Error("upstream key was not injected")
		}
		for _, header := range []string{"Authorization", "Cookie", "X-Admin-Token"} {
			if r.Header.Get(header) != "" {
				t.Errorf("forwarded %s", header)
			}
		}
		if r.Header.Get("X-Request-ID") != "integration-request" {
			t.Error("lost request ID")
		}
		if strings.Contains(r.Header.Get("X-Forwarded-For"), "spoofed") {
			t.Error("trusted forwarded IP")
		}
		if r.URL.EscapedPath() != "/v1/offers/A%20B" {
			t.Errorf("wrong upstream path: %s", r.URL.EscapedPath())
		}
		if r.URL.Query().Get("cursor") != "a+b==" || r.URL.Query().Get("region") != "HK" {
			t.Error("query changed")
		}
		w.Header().Set("ETag", `"snapshot"`)
		if r.Header.Get("If-None-Match") == `"snapshot"` {
			w.WriteHeader(304)
			return
		}
		if r.URL.Query().Get("limit") == "invalid" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"code":"INVALID_PARAMETER"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"external_id":"A B"}`))
	}))
	defer backend.Close()
	now := time.Now()
	memory := store.NewMemory()
	item := model.API{ID: "game", Name: "game", Method: "GET", Path: "/api/game-discount/v1/offers/{external_id}", UpstreamURL: backend.URL, UpstreamPath: "/v1/offers/{external_id}", UpstreamAuthRef: "game-discount", AuthMode: "api_key", Enabled: true, PublishedAt: &now}
	if err := memory.CreateAPI(item); err != nil {
		t.Fatal(err)
	}
	if err := memory.CreateCredential(model.Credential{ID: "client", Hash: auth.HashAPIKey("caller-secret")}); err != nil {
		t.Fatal(err)
	}
	h := gateway.New(memory, plugin.NewRegistry(), ratelimit.NewMemory(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	raw, _ := json.Marshal(map[string]upstream.Credential{"game-discount": {Origin: backend.URL, APIKey: "service-secret"}})
	credentials, err := upstream.Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	h.SetUpstreamCredentials(credentials)
	handler := httpx.RequestID(h)
	for _, tc := range []struct {
		key, etag, query string
		status           int
	}{
		{"", "", "", 401}, {"caller-secret", "", "", 200},
		{"caller-secret", `"snapshot"`, "", 304}, {"caller-secret", "", "&limit=invalid", 400},
	} {
		r := httptest.NewRequest("GET", "/api/game-discount/v1/offers/A%20B?region=HK&cursor=a%2Bb%3D%3D"+tc.query, nil)
		if tc.key != "" {
			r.Header.Set("Authorization", "Bearer "+tc.key)
		}
		r.Header.Set("X-Admin-Token", "must-not-leak")
		r.Header.Set("Cookie", "session=must-not-leak")
		r.Header.Set("X-Forwarded-For", "spoofed")
		r.Header.Set("X-Request-ID", "integration-request")
		r.Header.Set("If-None-Match", tc.etag)
		// A caller must not remove our injected upstream key via hop-by-hop headers.
		r.Header.Set("Connection", "X-API-Key")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%d: %s", w.Code, w.Body.String())
		}
		if tc.status == 304 && (w.Body.Len() != 0 || w.Header().Get("ETag") == "") {
			t.Fatal("conditional response broken")
		}
	}
	if calls != 3 {
		t.Fatalf("unauthenticated request reached upstream: %d", calls)
	}
	item.UpstreamURL = "http://127.0.0.1:1"
	if err := memory.UpdateAPI(item); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/game-discount/v1/offers/test", nil)
	r.Header.Set("X-API-Key", "caller-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 502 || strings.Contains(w.Body.String(), "service-secret") {
		t.Fatal("destination pinning failed")
	}
}
