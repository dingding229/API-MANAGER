package api

import (
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/sitesettings"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebsiteSettingsSuperAdminOnly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := store.NewMemory()
	users := user.NewService(m)
	_, err := users.Create("owner", "Password88", "super_admin")
	if err != nil {
		t.Fatal(err)
	}
	_, err = users.Create("tenant", "Password88", "tenant_admin")
	if err != nil {
		t.Fatal(err)
	}
	service, err := sitesettings.New(ctx, m, strings.Repeat("k", 64), sitesettings.Defaults(), "", users)
	if err != nil {
		t.Fatal(err)
	}
	admin := NewAdminWithUserManagement(m, plugin.NewRegistry(), users, slog.New(slog.NewTextHandler(io.Discard, nil)))
	admin.SetSiteSettings(service)
	_, ownerToken, _ := users.Authenticate("owner", "Password88")
	_, tenantToken, _ := users.Authenticate("tenant", "Password88")
	for _, tc := range []struct {
		token  string
		status int
	}{{"", 401}, {tenantToken, 403}, {ownerToken, 200}} {
		r := httptest.NewRequest("GET", "/admin/v1/settings", nil)
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		admin.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("settings permission: got %d want %d", w.Code, tc.status)
		}
	}
	cfg := service.View()
	cfg.Site.Name = "后台网站名称"
	request := model.UpdateSiteSettingsRequest{Version: cfg.Version, Site: cfg.Site, SMTP: cfg.SMTP}
	body, _ := json.Marshal(request)
	r := httptest.NewRequest("PUT", "/admin/v1/settings", strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer "+ownerToken)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, r)
	if w.Code != 200 || service.Public().Name != "后台网站名称" {
		t.Fatal("settings update failed")
	}
	r = httptest.NewRequest("PUT", "/admin/v1/settings", strings.NewReader(`{"site":{},"smtp":{},"roles":["super_admin"]}`))
	r.Header.Set("Authorization", "Bearer "+ownerToken)
	w = httptest.NewRecorder()
	admin.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("unknown setting fields accepted")
	}
	r = httptest.NewRequest("POST", "/admin/v1/settings/smtp/test", strings.NewReader(`{"recipient":"admin@example.test"}`))
	r.Header.Set("Authorization", "Bearer "+tenantToken)
	w = httptest.NewRecorder()
	admin.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("tenant administrator can use mail endpoint")
	}
}
