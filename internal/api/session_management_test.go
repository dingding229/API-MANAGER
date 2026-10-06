package api

import (
	"api-manager/internal/auth"
	"api-manager/internal/plugin"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionMetadataViewRevocationAndNoSecrets(t *testing.T) {
	m := store.NewMemory()
	u := user.NewService(m)
	_ = u.EnsureInitialAdmin("admin", "a-long-initial-password")
	r := httptest.NewRequest("POST", "/auth/v1/login", nil)
	r.RemoteAddr = "192.0.2.23:1100"
	r.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh) Safari/1.0")
	actor, token, _ := u.AuthenticateRequest("admin", "a-long-initial-password", r)
	session, _ := u.CurrentSession(token)
	admin := NewAdminWithUserManagement(m, plugin.NewRegistry(), u, slog.New(slog.NewTextHandler(io.Discard, nil)))
	get := httptest.NewRequest("GET", "/admin/v1/users/"+actor.ID+"/sessions", nil)
	get.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, get)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "192.0.2.23") || !strings.Contains(w.Body.String(), "Safari") {
		t.Fatal(w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), auth.HashAPIKey(token)) || strings.Contains(w.Body.String(), actor.PasswordHash) {
		t.Fatal("secret in session DTO")
	}
	var data []map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &data)
	if len(data) != 1 || data[0]["current"] != true {
		t.Fatal(data)
	}
	del := httptest.NewRequest("DELETE", "/admin/v1/users/"+actor.ID+"/sessions/"+session.ID, strings.NewReader(`{"confirm":true}`))
	del.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	admin.ServeHTTP(w, del)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if _, err := u.ValidateSession(token); err == nil {
		t.Fatal("revoked session still accepted")
	}
}
func TestSessionManagementCannotCrossUsersWithoutPermission(t *testing.T) {
	m := store.NewMemory()
	u := user.NewService(m)
	_ = u.EnsureInitialAdmin("admin", "a-long-initial-password")
	target, _, _ := u.Authenticate("admin", "a-long-initial-password")
	_, _ = u.Create("viewer", "ViewerPass888", "viewer")
	viewer, token, _ := u.Authenticate("viewer", "ViewerPass888")
	admin := NewAdminWithUserManagement(m, plugin.NewRegistry(), u, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, tc := range []struct {
		id   string
		want int
	}{{target.ID, 403}, {viewer.ID, 200}} {
		r := httptest.NewRequest("GET", "/admin/v1/users/"+tc.id+"/sessions", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		admin.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(tc, w.Code)
		}
	}
}

func TestOrdinaryManagerCannotRevokeSuperAdminSession(t *testing.T) {
	m := store.NewMemory()
	u := user.NewService(m)
	_ = u.EnsureInitialAdmin("admin", "a-long-initial-password")
	super, superToken, _ := u.Authenticate("admin", "a-long-initial-password")
	session, _ := u.CurrentSession(superToken)
	_, _ = u.Create("manager", "ManagerPass888", "tenant_admin")
	_, token, _ := u.Authenticate("manager", "ManagerPass888")
	admin := NewAdminWithUserManagement(m, plugin.NewRegistry(), u, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := httptest.NewRequest("DELETE", "/admin/v1/users/"+super.ID+"/sessions/"+session.ID, strings.NewReader(`{"confirm":true}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("super-admin session revoked", w.Code)
	}
	if _, err := u.ValidateSession(superToken); err != nil {
		t.Fatal("session affected", err)
	}
}
