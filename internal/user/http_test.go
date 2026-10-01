package user

import (
	"api-manager/internal/store"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConsoleLoginUsesPasswordSessionAndLogoutRevokesIt(t *testing.T) {
	s := NewService(store.NewMemory())
	if err := s.EnsureInitialAdmin("admin", "a-long-initial-password"); err != nil {
		t.Fatal(err)
	}
	h := NewHTTP(s)
	req := httptest.NewRequest("POST", "/auth/v1/login", strings.NewReader(`{"username":"admin","password":"a-long-initial-password"}`))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatalf("login=%d", response.Code)
	}
	var body struct{ Token string }
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	for _, c := range []struct {
		method, path string
		status       int
	}{{"GET", "/auth/v1/me", 200}, {"POST", "/auth/v1/logout", 204}, {"GET", "/auth/v1/me", 401}} {
		r := httptest.NewRequest(c.method, c.path, nil)
		r.Header.Set("Authorization", "Bearer "+body.Token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.status {
			t.Fatalf("%s=%d want=%d", c.path, w.Code, c.status)
		}
	}
	r := httptest.NewRequest("GET", "/auth/v1/me", nil)
	r.Header.Set("X-API-Key", strings.Repeat("a", 64))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("API key accepted as console session")
	}
}

func TestLoginRejectsAmbiguousOrOversizedJSON(t *testing.T) {
	h := NewHTTP(NewService(store.NewMemory()))
	for _, body := range []string{`{"username":"admin","password":"password","key":"ignored"}`, `{"username":"admin"} {"username":"other"}`, `{"username":"` + strings.Repeat("x", 5000) + `"}`} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/auth/v1/login", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("malformed/oversized login: %d", w.Code)
		}
	}
}
