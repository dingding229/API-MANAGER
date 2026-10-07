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

func TestSelfProfileUpdateRequiresCurrentPasswordAndRevokesSession(t *testing.T) {
	s := NewService(store.NewMemory())
	if err := s.EnsureInitialAdmin("admin", "a-long-initial-password"); err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Authenticate("admin", "a-long-initial-password")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("PUT", "/auth/v1/me", strings.NewReader(`{"username":"admin-renamed","current_password":"a-long-initial-password"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	NewHTTP(s).ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("profile update=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		ReauthenticationRequired bool `json:"reauthentication_required"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || !body.ReauthenticationRequired {
		t.Fatalf("unexpected profile response: %s", response.Body.String())
	}
	if _, err := s.ValidateSession(token); err == nil {
		t.Fatal("session survived self profile update")
	}
	if _, _, err := s.Authenticate("admin-renamed", "a-long-initial-password"); err != nil {
		t.Fatalf("renamed account cannot login: %v", err)
	}
}

func TestSelfProfileUpdateRejectsWrongCurrentPassword(t *testing.T) {
	s := NewService(store.NewMemory())
	if err := s.EnsureInitialAdmin("admin", "a-long-initial-password"); err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Authenticate("admin", "a-long-initial-password")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("PUT", "/auth/v1/me", strings.NewReader(`{"username":"admin-renamed","current_password":"wrong-password"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	NewHTTP(s).ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatalf("wrong password status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestViewerCanChangeOwnPasswordToEightBytes(t *testing.T) {
	s := NewService(store.NewMemory())
	u, err := s.Create("self-reader", "old-reader-password", "member")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Authenticate(u.Username, "old-reader-password")
	if err != nil {
		t.Fatal(err)
	}
	h := NewHTTP(s)
	for _, password := range []string{"1234567", "密1234"} {
		r := httptest.NewRequest("PUT", "/auth/v1/me", strings.NewReader(`{"password":"`+password+`","current_password":"old-reader-password"}`))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("short password accepted: %d", w.Code)
		}
		if _, err := s.ValidateSession(token); err != nil {
			t.Fatal("failed edit revoked session")
		}
	}
	r := httptest.NewRequest("PUT", "/auth/v1/me", strings.NewReader(`{"password":"密码12","current_password":"old-reader-password"}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("eight-byte self change failed: %d %s", w.Code, w.Body.String())
	}
	if _, err := s.ValidateSession(token); err == nil {
		t.Fatal("old session survived password change")
	}
	if _, _, err := s.Authenticate(u.Username, "old-reader-password"); err == nil {
		t.Fatal("old password still works")
	}
	if _, _, err := s.Authenticate(u.Username, "密码12"); err != nil {
		t.Fatal("new password rejected")
	}
}
