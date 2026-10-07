package api

import (
	"api-manager/internal/plugin"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"api-manager/internal/version"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
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

func TestVersionCredentialSettingsAreAdminOnlyMaskedAndRestartable(t *testing.T) {
	m := store.NewMemory()
	us := user.NewService(m)
	if e := us.EnsureInitialAdmin("admin", "Password888"); e != nil {
		t.Fatal(e)
	}
	dev, _ := us.Create("readdev", "Password888", "api_developer")
	_, at, _ := us.Authenticate("admin", "Password888")
	_, dt, _ := us.Authenticate(dev.Username, "Password888")
	makeAdmin := func() *Admin {
		a := NewAdminWithUserManagement(m, plugin.NewRegistry(), us, slog.New(slog.NewTextHandler(io.Discard, nil)))
		a.SetCredentialEncryptionKey("test-key-0123456789abcdefghijklmnopqrstuvwxyz")
		a.SetVersionChecker(version.NewChecker(""))
		a.SetCredentialGuard(func(r *http.Request, password, token string) error {
			if password != "Password888" {
				return errors.New("denied")
			}
			return nil
		})
		return a
	}
	a := makeAdmin()
	call := func(method, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/admin/v1/version/settings", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", dt, ""); w.Code != 403 {
		t.Fatal("developer read settings", w.Code)
	}
	secret := "github_pat_readonly_test_not_real_123456789"
	body := `{"version":0,"token":"` + secret + `","current_password":"Password888"}`
	if w := call("PUT", at, body); w.Code != 200 || strings.Contains(w.Body.String(), secret) {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, e := m.VersionCheckSettings(context.Background())
	if e != nil || saved.EncryptedToken == secret || strings.Contains(saved.EncryptedToken, secret) {
		t.Fatal("token not encrypted")
	}
	a = makeAdmin()
	if e = a.LoadVersionSettings(context.Background()); e != nil {
		t.Fatal(e)
	}
	if !a.versionChecker.TokenConfigured() {
		t.Fatal("credential lost on restart")
	}
	if w := call("GET", at, ""); w.Code != 200 || strings.Contains(w.Body.String(), secret) {
		t.Fatal("token leaked")
	}
	if w := call("PUT", at, `{"version":1,"clear_token":true,"current_password":"bad"}`); w.Code != 403 {
		t.Fatal("reauth bypass", w.Code)
	}
	if w := call("PUT", at, `{"version":1,"clear_token":true,"current_password":"Password888"}`); w.Code != 200 || a.versionChecker.TokenConfigured() {
		t.Fatal("clear failed", w.Code)
	}
}
