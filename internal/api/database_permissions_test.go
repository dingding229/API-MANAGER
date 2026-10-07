package api

import (
	"api-manager/internal/auth"
	"api-manager/internal/plugin"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDatabaseManagementCannotBeReachedByUsersOrDevelopers(t *testing.T) {
	s := store.NewMemory()
	users := user.NewService(s)
	admin, e := users.Create("dbroot", "Password888", "super_admin")
	if e != nil {
		t.Fatal(e)
	}
	a := NewAdminWithUserManagement(s, plugin.NewRegistry(), users, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, role := range []string{"member", "api_developer"} {
		u, e := users.Create("db"+map[string]string{"member": "member", "api_developer": "developer"}[role], "Password888", role)
		if e != nil {
			t.Fatal(e)
		}
		_, token, e := users.Authenticate(u.Username, "Password888")
		if e != nil {
			t.Fatal(e)
		}
		for _, path := range []string{"/tables", "/rows/users", "/backups", "/restore", "/import", "/download", "/reveal"} {
			method := "POST"
			if path == "/tables" || path == "/rows/users" {
				method = "GET"
			}
			r := httptest.NewRequest(method, "/admin/v1/database"+path, nil)
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			a.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatal(role, path, w.Code)
			}
		}
	}
	_, token, _ := users.Authenticate(admin.Username, "Password888")
	r := httptest.NewRequest("POST", "/admin/v1/database/restore", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
	r.Header.Set("Origin", "https://attacker.example")
	r.Header.Set("X-API-Request", "1")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("restore CSRF allowed", w.Code)
	}
}
