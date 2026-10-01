package grafanaproxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"api-manager/internal/httpx"
	"api-manager/internal/store"
	"api-manager/internal/user"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func setup(t *testing.T) (*Proxy, *user.Service, string, string) {
	t.Helper()
	s := user.NewService(store.NewMemory())
	u, err := s.Create("grafana-viewer", "grafana-password", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Authenticate("grafana-viewer", "grafana-password")
	if err != nil {
		t.Fatal(err)
	}
	p := New(s, "/admin", true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return p, s, token, u.ID
}
func TestGrafanaRequiresSessionAndCurrentPermission(t *testing.T) {
	p, s, token, id := setup(t)
	p.upstream.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("grafana")), Request: r}, nil
	})
	for _, tc := range []struct {
		token  string
		status int
	}{{"", 401}, {"ak_business_key", 401}, {token, 200}} {
		r := httptest.NewRequest("GET", "http://manager.example/admin/grafana/api/health", nil)
		if tc.token != "" {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: tc.token})
		}
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("got %d want %d", w.Code, tc.status)
		}
	}
	if err := s.SetStatus(id, "disabled"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://manager.example/admin/grafana/api/health", nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("disabled account retained Grafana access")
	}
}
func TestGrantIsScopedAndBusinessKeysCannotMintIt(t *testing.T) {
	p, _, token, _ := setup(t)
	for _, tc := range []struct {
		token, origin string
		status        int
	}{{"ak_business_key", "http://manager.example", 401}, {token, "http://attacker.example", 403}, {token, "http://manager.example", 204}} {
		r := httptest.NewRequest("POST", "http://manager.example/admin/grafana-session", nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		p.Grant(w, r)
		if w.Code != tc.status {
			t.Fatalf("grant %d want %d", w.Code, tc.status)
		}
		if w.Code == 204 {
			c := w.Result().Cookies()
			if len(c) != 1 || c[0].Path != "/admin/grafana/" || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode || c[0].MaxAge != 900 {
				t.Fatal("unscoped Grafana cookie")
			}
		}
	}
}
func TestProxyStripsCredentialsAndAllowsOnlySameOriginEmbedding(t *testing.T) {
	p, _, token, _ := setup(t)
	p.upstream.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "127.0.0.1:3000" || r.URL.Path != "/admin/grafana/api/health" {
			t.Fatal("unfixed proxy target")
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-WEBAUTH-USER") == "attacker" || r.Header.Get("X-WEBAUTH-ROLE") != "Viewer" {
			t.Fatal("credential or spoofed identity forwarded")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Set-Cookie": []string{"grafana_session=independent"}}, Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
	})
	r := httptest.NewRequest("GET", "http://manager.example/admin/grafana/api/health", nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	r.Header.Set("Authorization", "Bearer never-forward")
	r.Header.Set("X-WEBAUTH-USER", "attacker")
	r.Header.Set("X-WEBAUTH-ROLE", "Admin")
	w := httptest.NewRecorder()
	httpx.SecurityHeaders(p).ServeHTTP(w, r)
	if w.Code != 200 || len(w.Header().Values("X-Frame-Options")) != 1 || w.Header().Get("X-Frame-Options") != "SAMEORIGIN" || len(w.Header().Values("Content-Security-Policy")) != 1 || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("embedding/session headers incorrect: %+v", w.Header())
	}
}
func TestViewerPermissionAndLogoutAreRevalidated(t *testing.T) {
	p, s, token, id := setup(t)
	role, err := s.CreateRole("no-observability", "", []string{"api.read"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AssignRoles(id, []string{role.Name}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://manager.example/admin/grafana/", nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("permissionless user entered Grafana")
	}
	if err = s.Logout(token); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("logout left iframe authenticated")
	}
}
func TestCrossOriginWritesAreRejected(t *testing.T) {
	p, _, token, _ := setup(t)
	for _, tc := range []struct{ method, origin, site string }{{"POST", "http://attacker.example", "cross-site"}, {"POST", "", ""}, {"GET", "http://attacker.example", "cross-site"}} {
		r := httptest.NewRequest(tc.method, "http://manager.example/admin/grafana/api/ds/query", nil)
		r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("cross-origin Grafana request allowed")
		}
	}
}
