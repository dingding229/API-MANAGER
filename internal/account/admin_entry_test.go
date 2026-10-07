package account

import (
	"api-manager/internal/auth"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresAdminEntryIsPermissionBoundSessionBoundAndSingleUse(t *testing.T) {
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
	admin, e := us.Create("entryadmin"+ids.NewUUID()[:8], "Password888", "super_admin")
	if e != nil {
		t.Fatal(e)
	}
	member, e := us.Create("entrymember"+ids.NewUUID()[:8], "Password888", "member")
	if e != nil {
		t.Fatal(e)
	}
	_, at, e := us.Authenticate(admin.Username, "Password888")
	if e != nil {
		t.Fatal(e)
	}
	_, at2, e := us.Authenticate(admin.Username, "Password888")
	if e != nil {
		t.Fatal(e)
	}
	_, mt, e := us.Authenticate(member.Username, "Password888")
	if e != nil {
		t.Fatal(e)
	}
	// Each test initializes its own authentication settings, including when CI
	// reuses a database after storage-level encryption fixtures.
	cfg, _, err := p.SecuritySettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = p.SaveSecuritySettings(context.Background(), model.SecuritySettings{Version: cfg.Version, DefaultRole: "member", WebsiteURL: "https://example.test"}, ""); err != nil {
		t.Fatal(err)
	}
	s := New(p, us, "0123456789abcdefghijklmnopqrstuvwxyz", &fakeMail{codes: map[string]string{}}, true)
	s.SetAdminPath("/staff/console")
	issue := func(token, origin string, bearer bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://example.test/account/v1/admin-entry", strings.NewReader("{}"))
		r.Header.Set("Origin", origin)
		r.Header.Set("X-API-Request", "1")
		if bearer {
			r.Header.Set("Authorization", "Bearer "+token)
		} else {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		token, origin string
		bearer        bool
	}{{mt, "https://example.test", false}, {at, "https://attacker.test", false}, {at, "https://example.test", true}} {
		if w := issue(tc.token, tc.origin, tc.bearer); w.Code != 403 {
			t.Fatal("entry privilege or CSRF bypass", w.Code, w.Body.String())
		}
	}
	gate := s.ProtectConsole(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	enter := func(token string, proof *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://example.test/staff/console/", nil)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
		}
		if proof != nil {
			r.AddCookie(proof)
		}
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		return w
	}
	for _, token := range []string{"", mt, at} {
		if w := enter(token, nil); w.Code != 303 {
			t.Fatal("direct console access", w.Code)
		}
	}
	w := issue(at, "https://example.test", false)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing entry cookie")
	}
	proof := cookies[0]
	if !proof.HttpOnly || !proof.Secure || proof.Path != "/staff/console/" || proof.SameSite != http.SameSiteStrictMode || strings.Contains(w.Body.String(), proof.Value) {
		t.Fatal("entry protection flags or secret disclosure")
	}
	if w = enter(at2, proof); w.Code != 303 {
		t.Fatal("proof transferred to another session")
	}
	if w = enter(mt, proof); w.Code != 303 {
		t.Fatal("member used admin proof")
	}
	if w = enter(at, proof); w.Code != 200 {
		t.Fatal("authorized user-center entry rejected", w.Code)
	}
	if w = enter(at, proof); w.Code != 303 {
		t.Fatal("entry replay accepted")
	}
	expiredSecret := randomHex(32)
	id := ids.NewUUID()
	if e = p.PutVerification(context.Background(), model.Verification{ID: id, Purpose: "admin-entry", Subject: admin.ID, UserID: admin.ID, Binding: auth.HashAPIKey(at), CodeHash: auth.HashAPIKey(expiredSecret), ExpiresAt: time.Now().Add(-time.Minute)}); e != nil {
		t.Fatal(e)
	}
	if w = enter(at, &http.Cookie{Name: adminEntryCookie, Value: id + ":" + expiredSecret}); w.Code != 303 {
		t.Fatal("expired entry accepted")
	}
	w = issue(at, "https://example.test", false)
	proof = w.Result().Cookies()[0]
	if e = p.DeleteSession(auth.HashAPIKey(at)); e != nil {
		t.Fatal(e)
	}
	if w = enter(at, proof); w.Code != 303 {
		t.Fatal("revoked session entered console")
	}
}
