package user

import (
	"api-manager/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testingRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, "https://docs.example.com"+path, strings.NewReader(body))
	r.Header.Set("Origin", "https://docs.example.com")
	r.Header.Set("X-API-Test", "1")
	return r
}
func TestPublicTestingSessionIsMinimalAndRevoked(t *testing.T) {
	s := NewService(store.NewMemory())
	if err := s.EnsureInitialAdmin("admin", "a-long-initial-password"); err != nil {
		t.Fatal(err)
	}
	h := NewHTTP(s)
	h.SetProductionMode(true)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, testingRequest("POST", "/test/v1/login", `{"username":"admin","password":"a-long-initial-password"}`))
	if w.Code != 200 || strings.Contains(w.Body.String(), "token") || strings.Contains(w.Body.String(), "permissions") {
		t.Fatalf("login %d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing session cookie")
	}
	cookie := cookies[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/test/v1" {
		t.Fatal("unsafe cookie")
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "/test/v1/session", 200}, {"GET", "/auth/v1/me", 401}, {"POST", "/test/v1/logout", 204}, {"GET", "/test/v1/session", 401}} {
		r := testingRequest(tc.method, tc.path, "")
		r.AddCookie(cookie)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s %d", tc.method, tc.path, w.Code)
		}
		if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("session cached")
		}
	}
}
func TestTestingRejectsCrossOriginAndAmbiguousBodies(t *testing.T) {
	h := NewHTTP(NewService(store.NewMemory()))
	for _, origin := range []string{"https://evil.example", "null", "", "https://docs.example.com/path"} {
		r := testingRequest("POST", "/test/v1/login", `{}`)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("origin %s = %d", origin, w.Code)
		}
	}
	for _, body := range []string{`{"username":"x","password":"y","url":"https://evil"}`, `{} {}`, strings.Repeat("x", 5000)} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, testingRequest("POST", "/test/v1/login", body))
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	r := testingRequest("GET", "/test/v1/session", "")
	r.AddCookie(&http.Cookie{Name: TestingCookie, Value: "us_" + strings.Repeat("a", 64)})
	r.AddCookie(&http.Cookie{Name: TestingCookie, Value: "us_" + strings.Repeat("b", 64)})
	if testingToken(r) != "" {
		t.Fatal("ambiguous cookie")
	}
}
func TestAdminLoginAlsoEnablesFrontendSession(t *testing.T) {
	s := NewService(store.NewMemory())
	_ = s.EnsureInitialAdmin("admin", "a-long-initial-password")
	h := NewHTTP(s)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, testingRequest("POST", "/auth/v1/login", `{"username":"admin","password":"a-long-initial-password"}`))
	r := testingRequest("GET", "/test/v1/session", "")
	for _, cookie := range w.Result().Cookies() {
		r.AddCookie(cookie)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}
