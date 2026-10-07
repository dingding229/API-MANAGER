package publicweb

import (
	"api-manager/internal/model"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, export http.Handler) *Handler {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>public docs</html>"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "_next/static"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "_next/static/app.js"), []byte("console.log('public')"), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := New(dir, export)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func TestPublicListenerDoesNotExposeManagementOrFiles(t *testing.T) {
	h := fixture(t, http.NotFoundHandler())
	for _, path := range []string{"/admin/v1/apis", "/auth/v1/login", "/console/", "/api/example", "/index.html", "/_next/static/../../secrets", "/_next/static/", "/secrets/admin_password"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/", "/_next/static/app.js"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("%s: %d", path, w.Code)
		}
		if w.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatal("missing security header")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/catalog.json", nil))
	if w.Code != 405 {
		t.Fatal("write accepted")
	}
}
func TestPublicExportDropsIncomingHeadersQueryAndPrivateDTOFields(t *testing.T) {
	h := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/public/v1/catalog" || r.URL.RawQuery != "" || len(r.Header) != 0 {
			t.Fatal("incoming browser credentials/target forwarded")
		}
		_, _ = w.Write([]byte(`{"version":1,"base_url":"","apis":[],"private_field":"PRIVATE"}`))
	}))
	r := httptest.NewRequest("GET", "/catalog.json?target=/admin/v1/apis", nil)
	r.Header.Set("Cookie", "session=PRIVATE")
	r.Header.Set("Authorization", "Bearer PRIVATE")
	r.Header.Set("X-API-Key", "PRIVATE")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatal("public projection failed")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("catalog caching enabled")
	}
}
func TestExportFailuresNeverLeakDetails(t *testing.T) {
	for _, body := range []string{"private error", strings.Repeat("x", maxCatalogBytes+1), `{"version":2,"apis":[]}`} {
		h := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/catalog.json", nil))
		if w.Code != 503 || w.Body.String() != "{\"error\":\"public catalog unavailable\"}\n" {
			t.Fatal("export failure not closed")
		}
	}
}

func TestServerRenderedWebsiteMetadataIsEscaped(t *testing.T) {
	h := fixture(t, http.NotFoundHandler())
	dir := t.TempDir()
	source := `<html><head><title>default</title><meta name="description" content="default"/></head><body>public</body></html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	h.assets = os.DirFS(dir)
	h.SetSiteProvider(func() model.PublicSiteInfo {
		return model.PublicSiteInfo{PublicTitle: `</title><script>alert(1)</script>`, Description: `"/><script>alert(2)</script>`, Keywords: `" bad=1`, WebsiteURL: "https://example.test"}
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	body := w.Body.String()
	if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, `rel="canonical" href="https://example.test/"`) {
		t.Fatal("unsafe or missing dynamic metadata")
	}
}

func TestPublicConnectPolicyOnlyAddsTheConfiguredSafeAPIDomain(t *testing.T) {
	h := fixture(t, http.NotFoundHandler())
	for _, tc := range []struct {
		origin  string
		allowed bool
	}{{"https://api.example.com", true}, {"http://127.0.0.1:8080", true}, {"https://api.example.com; script-src *", false}, {"https://u:p@api.example.com", false}, {"https://api.example.com/path", false}, {"javascript:alert(1)", false}} {
		h.SetSiteProvider(func() model.PublicSiteInfo { return model.PublicSiteInfo{APIDomain: tc.origin} })
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		policy := w.Header().Get("Content-Security-Policy")
		if strings.Contains(policy, "connect-src 'self' "+tc.origin+" ") != tc.allowed {
			t.Fatalf("%s policy=%s", tc.origin, policy)
		}
		if strings.Contains(policy, "script-src *") {
			t.Fatal("CSP injection")
		}
	}
}

func TestPlaygroundHTMLIsServedButNeverSharedCached(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "index.html"), []byte("index"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "playground.html"), []byte("playground"), 0600); e != nil {
		t.Fatal(e)
	}
	h, e := New(dir, http.NotFoundHandler())
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/playground", nil))
	if w.Code != 200 || w.Body.String() != "playground" || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatal(w.Code, w.Body.String(), w.Header())
	}
}
