package api

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"api-manager/internal/plugin"
	"api-manager/internal/store"
	"api-manager/internal/user"
)

func TestUsernamePasswordSessionsEnforceUserRoles(t *testing.T) {
	m := store.NewMemory()
	users := user.NewService(m)
	if err := users.EnsureInitialAdmin("admin", "admin-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := users.Create("reader", "reader-password", "viewer"); err != nil {
		t.Fatal(err)
	}
	_, adminSession, err := users.Authenticate("admin", "admin-password")
	if err != nil {
		t.Fatal(err)
	}
	_, readerSession, err := users.Authenticate("reader", "reader-password")
	if err != nil {
		t.Fatal(err)
	}
	plugins := plugin.NewRegistry()
	a := NewAdminWithUserManagement(m, plugins, users, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, tc := range []struct {
		method, path, token, body string
		status                    int
	}{
		{"GET", "/admin/v1/apis", adminSession, "", 200},
		{"GET", "/admin/v1/apis", readerSession, "", 200},
		{"POST", "/admin/v1/apis", readerSession, `{"name":"forbidden"}`, 403},
		{"GET", "/admin/v1/apis", "ak_business_key", "", 401},
		{"POST", "/admin/v1/users", readerSession, `{"email":"other","password":"other-password"}`, 403},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d want %d", tc.method, tc.path, w.Code, tc.status)
		}
	}
}
