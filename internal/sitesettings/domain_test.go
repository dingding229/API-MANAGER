package sitesettings

import (
	"api-manager/internal/httpx"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDomainNormalization(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{{"", ""}, {"API.example.com", "https://api.example.com"}, {"https://api.example.com:443/", "https://api.example.com"}, {"http://api.example.com:8081", "http://api.example.com:8081"}, {"https://[2001:db8::1]:8443", "https://[2001:db8::1]:8443"}} {
		got, err := NormalizeAPIDomain(tc.raw)
		if err != nil || got != tc.want {
			t.Fatalf("%s -> %s %v", tc.raw, got, err)
		}
	}
	for _, raw := range []string{"https://user:pass@api.example.com", "https://api.example.com/path", "https://api.example.com?q=1", "https://api.example.com#secret", "https://*.example.com", "https://api.example.com:99999", "https://api.example.com:", "https://api.example.com\\evil", "api.example.com,evil.example.com"} {
		if _, err := NormalizeAPIDomain(raw); err == nil {
			t.Fatalf("invalid domain accepted: %s", raw)
		}
	}
}
func TestDedicatedDomainGateAndClearAreImmediate(t *testing.T) {
	s, err := New(context.Background(), store.NewMemory(), strings.Repeat("k", 64), Defaults(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler := s.APIDomainGuard(httpx.CORS("https://site.example.com", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(200) })))
	request := func(method, host, path string, spoof bool) int {
		r := httptest.NewRequest(method, "http://"+host+path, nil)
		if spoof {
			r.Header.Set("X-Forwarded-Host", "api.example.com")
			r.Header.Set("Forwarded", "host=api.example.com")
		}
		if method == "OPTIONS" {
			r.Header.Set("Origin", "https://site.example.com")
			r.Header.Set("Access-Control-Request-Method", "POST")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if request("GET", "site.example.com", "/api/items", false) != 200 {
		t.Fatal("website fallback not available")
	}
	cfg := s.View()
	cfg.Site.APIDomain = "api.example.com"
	cfg.Site.WebsiteURL = "https://site.example.com"
	saved, err := s.Save(model.UpdateSiteSettingsRequest{Version: cfg.Version, Site: cfg.Site, SMTP: cfg.SMTP})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "POST", "OPTIONS"} {
		if request(method, "site.example.com", "/api/items", true) != 421 {
			t.Fatal("website or forwarded-header bypass accepted")
		}
	}
	if request("GET", "API.EXAMPLE.COM:443", "/api/items", false) != 200 {
		t.Fatal("dedicated origin rejected")
	}
	if request("GET", "api.example.com.evil.example", "/api/items", false) != 421 {
		t.Fatal("host suffix spoof accepted")
	}
	if request("GET", "site.example.com", "/admin/", false) != 200 {
		t.Fatal("admin page was blocked")
	}
	if request("OPTIONS", "api.example.com", "/api/items", false) != 204 {
		t.Fatal("valid preflight rejected")
	}
	saved.Site.APIDomain = ""
	if _, err = s.Save(model.UpdateSiteSettingsRequest{Version: saved.Version, Site: saved.Site, SMTP: saved.SMTP}); err != nil {
		t.Fatal(err)
	}
	if request("GET", "site.example.com", "/api/items", false) != 200 {
		t.Fatal("clearing domain did not restore website calls")
	}
	if calls != 4 {
		t.Fatalf("domain denied requests reached downstream: %d", calls)
	}
}
func TestDedicatedDomainCannotEqualWebsite(t *testing.T) {
	s, _ := New(context.Background(), store.NewMemory(), strings.Repeat("k", 64), Defaults(), "", nil)
	cfg := s.View()
	cfg.Site.WebsiteURL = "https://site.example.com"
	cfg.Site.APIDomain = "https://site.example.com:8443"
	if _, err := s.Save(model.UpdateSiteSettingsRequest{Site: cfg.Site, SMTP: cfg.SMTP}); err != ErrSameSiteDomain {
		t.Fatalf("same website host accepted: %v", err)
	}
}

func TestAPIDomainPersistsAcrossRestart(t *testing.T) {
	m := store.NewMemory()
	s, _ := New(context.Background(), m, strings.Repeat("k", 64), Defaults(), "", nil)
	cfg := s.View()
	cfg.Site.APIDomain = "https://api.example.com:8443"
	if _, err := s.Save(model.UpdateSiteSettingsRequest{Site: cfg.Site, SMTP: cfg.SMTP}); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), m, strings.Repeat("k", 64), Defaults(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.AllowsAPIHost("site.example.com") || !restarted.AllowsAPIHost("api.example.com:8443") || restarted.AllowsAPIHost("api.example.com") {
		t.Fatal("domain restriction was lost on restart")
	}
}
func TestTLSWebsiteNameCannotMasqueradeAsAPI(t *testing.T) {
	s, _ := New(context.Background(), store.NewMemory(), strings.Repeat("k", 64), Defaults(), "", nil)
	cfg := s.View()
	cfg.Site.APIDomain = "https://api.example.com"
	_, err := s.Save(model.UpdateSiteSettingsRequest{Site: cfg.Site, SMTP: cfg.SMTP})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "https://api.example.com/api/items", nil)
	r.TLS = &tls.ConnectionState{ServerName: "site.example.com"}
	w := httptest.NewRecorder()
	s.APIDomainGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(w, r)
	if w.Code != 421 {
		t.Fatal("website TLS name bypassed API domain restriction")
	}
}
