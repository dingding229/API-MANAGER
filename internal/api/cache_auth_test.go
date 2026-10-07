package api

import (
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAdminAcceptsFrontendCookieAndProtectsCacheClear(t *testing.T) {
	m := store.NewMemory()
	users := user.NewService(m)
	_ = users.EnsureInitialAdmin("admin", "a-long-initial-password")
	_, token, _ := users.Authenticate("admin", "a-long-initial-password")
	admin := NewAdminWithUserManagement(m, plugin.NewRegistry(), users, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Now()
	_ = m.CreateAPI(model.API{ID: "cached-api", Name: "cached", Method: "GET", Path: "/api/cached", Plugin: "plugin", UpdatedAt: now})
	for _, tc := range []struct {
		method, path, body string
		guard              bool
		status             int
	}{{"GET", "/admin/v1/apis", "", false, 200}, {"GET", "/admin/v1/apis/cached-api/cache", "", false, 200}, {"DELETE", "/admin/v1/apis/cached-api/cache", `{"confirm":true}`, false, 403}, {"DELETE", "/admin/v1/apis/cached-api/cache", `{}`, true, 400}, {"DELETE", "/admin/v1/apis/cached-api/cache", `{"confirm":true}`, true, 200}} {
		r := httptest.NewRequest(tc.method, "https://site.example"+tc.path, strings.NewReader(tc.body))
		r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
		if tc.guard {
			r.Header.Set("Origin", "https://site.example")
			r.Header.Set("X-API-Request", "1")
		}
		w := httptest.NewRecorder()
		admin.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s =%d %s", tc.method, tc.path, w.Code, w.Body)
		}
	}
	for _, c := range []model.PluginCacheConfig{{Enabled: true, TTLSeconds: 0, MaxEntries: 1}, {Enabled: true, TTLSeconds: 1, MaxEntries: 10001}, {Enabled: true, TTLSeconds: 604801, MaxEntries: 10}} {
		if c.Validate("plugin") == nil {
			t.Fatal("unsafe config accepted")
		}
	}
}

func TestReadOnlyUserCannotClearPluginCache(t *testing.T) {
	m := store.NewMemory()
	users := user.NewService(m)
	_ = users.EnsureInitialAdmin("admin", "a-long-initial-password")
	_, err := users.Create("readonlyuser", "ViewerPass888", "member")
	if err != nil {
		t.Fatal(err)
	}
	_, token, _ := users.Authenticate("readonlyuser", "ViewerPass888")
	admin := NewAdminWithUserManagement(m, plugin.NewRegistry(), users, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := httptest.NewRequest("DELETE", "/admin/v1/apis/cached/cache", strings.NewReader(`{"confirm":true}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("readonly role cleared cache", w.Code)
	}
}
