package sitesettings

import (
	"api-manager/internal/store"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTestingCORSOnlyOpensBusinessAPIToConfiguredWebsite(t *testing.T) {
	defaults := Defaults()
	defaults.Site.WebsiteURL = "https://docs.example.com"
	defaults.Site.APIDomain = "https://api.example.com"
	s, err := New(context.Background(), store.NewMemory(), strings.Repeat("k", 64), defaults, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	h := s.APIDomainGuard(s.PublicAPICORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })))
	for _, tc := range []struct {
		path, host, origin, headers string
		allow                       bool
		status                      int
	}{
		{"/api/sample", "api.example.com", "https://docs.example.com", "content-type,x-api-key,x-request-id", true, 204},
		{"/admin/v1/apis", "api.example.com", "https://docs.example.com", "x-api-key", false, 200},
		{"/test/v1/session", "api.example.com", "https://docs.example.com", "", false, 200},
		{"/api/sample", "api.example.com", "https://evil.example", "x-api-key", false, 200},
		{"/api/sample", "docs.example.com", "https://docs.example.com", "x-api-key", false, 421},
		{"/api/sample", "api.example.com", "https://docs.example.com", "authorization", false, 200},
	} {
		r := httptest.NewRequest("OPTIONS", "https://"+tc.host+tc.path, nil)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Access-Control-Request-Method", "GET")
		r.Header.Set("Access-Control-Request-Headers", tc.headers)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if (w.Header().Get("Access-Control-Allow-Origin") != "") != tc.allow || w.Code != tc.status {
			t.Fatalf("%+v = %d %v", tc, w.Code, w.Header())
		}
		if w.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Fatal("session cookies allowed to API")
		}
	}
}
