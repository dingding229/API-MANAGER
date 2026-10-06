package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOnlySuccessfulPublicStaticResponsesCanBeCached(t *testing.T) {
	for _, path := range []string{"/", "/catalog.json", "/public/v1/catalog", "/test/v1/session", "/auth/v1/login", "/admin/app.css", "/admin/v1/version", "/api/report.css", "/health/ready", "/metrics", "/_next/static/chunks/app.js"} {
		for _, status := range []int{200, 404, 503} {
			for _, credential := range []string{"", "Cookie", "Authorization", "X-API-Key"} {
				h := CachePolicy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					w.Header().Set("Expires", "future")
					w.Header().Set("CDN-Cache-Control", "public")
					w.WriteHeader(status)
				}))
				r := httptest.NewRequest("GET", path, nil)
				if credential != "" {
					r.Header.Set(credential, "secret")
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				cacheable := path == "/_next/static/chunks/app.js" && status == 200 && credential == ""
				for _, header := range []string{"Cache-Control", "CDN-Cache-Control", "Cloudflare-CDN-Cache-Control"} {
					if strings.Contains(w.Header().Get(header), "immutable") != cacheable {
						t.Fatalf("%s %d %s %s", path, status, credential, header)
					}
				}
				if w.Header().Get("Expires") != "" {
					t.Fatal("upstream Expires leaked")
				}
			}
		}
	}
}
func TestStaticMissingImmutableOrSettingCookiesIsNotCached(t *testing.T) {
	for _, cookie := range []bool{true, false} {
		h := CachePolicy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cookie {
				w.Header().Set("Cache-Control", "public, immutable")
				w.Header().Set("Set-Cookie", "secret=1")
			}
			w.Write([]byte("content"))
		}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/_next/static/app.js", nil))
		if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("unsafe static cached")
		}
	}
}
