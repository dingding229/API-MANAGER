package api

import (
	"api-manager/internal/account"
	"api-manager/internal/auth"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestPostgresUserTwoFactorResetRequiresAdministratorPasswordAndAudit(t *testing.T) {
	dsn := os.Getenv("TEST_ACCOUNT_DSN")
	if dsn == "" {
		t.Skip("isolated database required")
	}
	if !strings.Contains(dsn, "/api_account_review_") {
		t.Fatal("disposable database required")
	}
	p, e := store.NewPostgres(context.Background(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	us := user.NewService(p)
	admin, e := us.Create("mfaadmin"+ids.NewUUID()[:8], "Password888", "super_admin")
	if e != nil {
		t.Fatal(e)
	}
	member, e := us.Create("mfamember"+ids.NewUUID()[:8], "Password888", "member")
	if e != nil {
		t.Fatal(e)
	}
	_, at, _ := us.Authenticate(admin.Username, "Password888")
	_, mt, _ := us.Authenticate(member.Username, "Password888")
	if e = p.BeginTOTP(context.Background(), member.ID, "protected-test-secret"); e != nil {
		t.Fatal(e)
	}
	if e = p.EnableTOTP(context.Background(), member.ID, "protected-test-secret", 12, []string{"recovery"}); e != nil {
		t.Fatal(e)
	}
	_, mt, e = us.Authenticate(member.Username, "Password888")
	if e != nil {
		t.Fatal(e)
	}
	target, _ := p.GetUserByID(member.ID)
	s := account.New(p, us, "0123456789abcdefghijklmnopqrstuvwxyz", nil, false)
	a := NewAdminWithUserManagement(p, plugin.NewRegistry(), us, slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.SetCredentialGuard(s.ReauthenticateAdmin)
	call := func(method, id, token string, data map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(data)
		r := httptest.NewRequest(method, "https://example.test/admin/v1/users/"+id+"/two-factor", strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", member.ID, mt, nil); w.Code != 403 {
		t.Fatal("member read admin security", w.Code)
	}
	w := call("GET", member.ID, at, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "protected-test-secret") || strings.Contains(w.Body.String(), "recovery\"") {
		t.Fatal("security secret leaked", w.Body.String())
	}
	data := map[string]any{"username": member.Username, "reason": "用户遗失验证器", "confirm": true, "revision": target.AuthRevision}
	if w := call("DELETE", member.ID, at, data); w.Code != 403 {
		t.Fatal("password bypass", w.Code)
	}
	for _, tc := range []struct {
		name, field string
		value       any
		status      int
	}{
		{"wrong administrator password", "current_password", "WrongPassword888", 403},
		{"wrong username", "username", "not-this-user", 400},
		{"confirmation omitted", "confirm", false, 400},
		{"reason missing", "reason", "", 400},
		{"stale security revision", "revision", target.AuthRevision - 1, 409},
	} {
		invalid := map[string]any{}
		for key, value := range data {
			invalid[key] = value
		}
		invalid["current_password"] = "Password888"
		invalid[tc.field] = tc.value
		if w := call("DELETE", member.ID, at, invalid); w.Code != tc.status {
			t.Fatal(tc.name, w.Code, w.Body.String())
		}
		record, err := p.Account(context.Background(), member.ID)
		if err != nil || record.TOTPSecret == "" {
			t.Fatal("rejected reset changed security state", err)
		}
	}
	data["current_password"] = "Password888"
	if w := call("DELETE", admin.ID, at, data); w.Code != 403 {
		t.Fatal("self MFA bypass", w.Code)
	}
	if w := call("DELETE", member.ID, at, data); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, e = p.GetSession(auth.HashAPIKey(mt)); e == nil {
		t.Fatal("old member session survived")
	}
	logs, e := p.ListAuditLogs(model.AuditLogQuery{Action: "user.two_factor.reset", PageSize: 20})
	if e != nil || len(logs.Items) == 0 {
		t.Fatal("reset not audited", e)
	}
}
