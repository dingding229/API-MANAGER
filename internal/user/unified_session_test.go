package user

import (
	"api-manager/internal/auth"
	"api-manager/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func unifiedHTTP(t *testing.T) (*HTTP, *Service) {
	t.Helper()
	s := NewService(store.NewMemory())
	if err := s.EnsureInitialAdmin("admin", "a-long-initial-password"); err != nil {
		t.Fatal(err)
	}
	return NewHTTP(s), s
}
func rootCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookie && c.Value != "" {
			return c
		}
	}
	return nil
}
func TestBothLoginsUseOneSessionAndBothLogoutsRevokeIt(t *testing.T) {
	for _, login := range []string{"/test/v1/login", "/auth/v1/login"} {
		for _, logout := range []string{"/test/v1/logout", "/auth/v1/logout"} {
			h, _ := unifiedHTTP(t)
			r := testingRequest("POST", login, `{"username":"admin","password":"a-long-initial-password"}`)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatal(login, w.Code)
			}
			if strings.Contains(w.Body.String(), `"token"`) {
				t.Fatal("browser login exposed token")
			}
			cookie := rootCookie(w)
			if cookie == nil || cookie.Path != "/" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
				t.Fatal("invalid shared cookie")
			}
			for _, path := range []string{"/test/v1/session", "/auth/v1/me"} {
				r = testingRequest("GET", path, "")
				r.AddCookie(cookie)
				w = httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != 200 {
					t.Fatal("second login required", login, path, w.Code)
				}
			}
			r = testingRequest("POST", logout, "")
			r.AddCookie(cookie)
			w = httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 204 {
				t.Fatal("logout failed", w.Code)
			}
			for _, path := range []string{"/test/v1/session", "/auth/v1/me"} {
				r = testingRequest("GET", path, "")
				r.AddCookie(cookie)
				w = httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != 401 {
					t.Fatal("other UI remained signed in", logout, path, w.Code)
				}
			}
		}
	}
}
func TestCookieSessionMutationsRequireSameOriginGuardAndProfileChangesRevokeBoth(t *testing.T) {
	h, s := unifiedHTTP(t)
	_, token, err := s.Authenticate("admin", "a-long-initial-password")
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: auth.SessionCookie, Value: token}
	for _, origin := range []string{"", "https://evil.example", "null"} {
		r := testingRequest("POST", "/auth/v1/logout", "")
		r.Header.Set("Origin", origin)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("CSRF allowed", origin, w.Code)
		}
	}
	r := testingRequest("PUT", "/auth/v1/me", `{"username":"renamed","current_password":"a-long-initial-password"}`)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if _, err := s.ValidateSession(token); err == nil {
		t.Fatal("profile change left session valid")
	}
	viewer, createErr := s.Create("viewer-user", "ViewerPass888", "viewer")
	if createErr != nil {
		t.Fatal(createErr)
	}
	_, token, err = s.Authenticate("viewer-user", "ViewerPass888")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(viewer.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ValidateSession(token); err == nil {
		t.Fatal("disabled user session valid")
	}
}
func TestLegacyFrontendAndBearerSessionsMigrateWithoutPassword(t *testing.T) {
	h, s := unifiedHTTP(t)
	_, token, _ := s.Authenticate("admin", "a-long-initial-password")
	r := testingRequest("GET", "/test/v1/session", "")
	r.AddCookie(&http.Cookie{Name: TestingCookie, Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || rootCookie(w) == nil {
		t.Fatal("legacy frontend migration failed", w.Code)
	}
	r = testingRequest("POST", "/auth/v1/session", "")
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || rootCookie(w) == nil {
		t.Fatal("legacy console migration failed", w.Code)
	}
	r = testingRequest("GET", "/auth/v1/me", "")
	r.AddCookie(rootCookie(w))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestAnonymousChecksCannotDeleteNewerBrowserSession(t *testing.T) {
	h, _ := unifiedHTTP(t)
	for _, path := range []string{"/auth/v1/me", "/test/v1/session"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, testingRequest("GET", path, ""))
		if w.Code != 401 {
			t.Fatal(w.Code)
		}
		for _, c := range w.Result().Cookies() {
			if c.Name == auth.SessionCookie {
				t.Fatal("late anonymous check can overwrite a concurrent login cookie")
			}
		}
	}
}
