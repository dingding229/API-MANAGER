package api

import (
	"api-manager/internal/plugin"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"api-manager/internal/version"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVersionInfoRequiresUserSessionNotAPIKey(t *testing.T) {
	memory := store.NewMemory()
	users := user.NewService(memory)
	if err := users.EnsureInitialAdmin("admin", "long-initial-password"); err != nil {
		t.Fatal(err)
	}
	_, token, err := users.Authenticate("admin", "long-initial-password")
	if err != nil {
		t.Fatal(err)
	}
	admin := NewAdminWithUserManagement(memory, plugin.NewRegistry(), users, slog.New(slog.NewTextHandler(io.Discard, nil)))
	admin.SetVersionChecker(version.NewChecker(""))
	for _, tc := range []struct {
		header, value string
		status        int
	}{{"", "", 401}, {"X-API-Key", "not-a-login-token", 401}, {"Authorization", "Bearer " + token, 200}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/admin/v1/version", nil)
		if tc.header != "" {
			r.Header.Set(tc.header, tc.value)
		}
		admin.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("version auth %s %d", tc.header, w.Code)
		}
		if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("version cached")
		}
		if tc.status == 200 && !strings.Contains(w.Body.String(), version.Version) {
			t.Fatal("current version missing")
		}
	}
}
