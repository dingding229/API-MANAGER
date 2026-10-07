package account

import (
	"api-manager/internal/auth"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresCredentialRotationCardsAndRevokedPermissions(t *testing.T) {
	dsn := os.Getenv("TEST_ACCOUNT_DSN")
	if dsn == "" {
		t.Skip("isolated database not configured")
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
	admin, e := us.Create("new-admin-"+ids.NewUUID()[:8], "Password888", "super_admin")
	if e != nil {
		t.Fatal(e)
	}
	u, e := us.Create("new-user-"+ids.NewUUID()[:8], "Password888", "member")
	if e != nil {
		t.Fatal(e)
	}
	other, e := us.Create("other-"+ids.NewUUID()[:8], "Password888", "member")
	if e != nil {
		t.Fatal(e)
	}
	_, token, e := us.Authenticate(u.Username, "Password888")
	if e != nil {
		t.Fatal(e)
	}
	_, atoken, e := us.Authenticate(admin.Username, "Password888")
	if e != nil {
		t.Fatal(e)
	}
	cfg, _, e := p.SecuritySettings(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = p.SaveSecuritySettings(context.Background(), model.SecuritySettings{Version: cfg.Version, DefaultRole: "member", WebsiteURL: "https://example.test"}, ""); e != nil {
		t.Fatal(e)
	}
	s := New(p, us, "0123456789abcdefghijklmnopqrstuvwxyz", &fakeMail{codes: map[string]string{}}, false)
	call := func(path, session string, body any) (int, map[string]any) {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPost, "https://example.test"+path, strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer "+session)
		r.Header.Set("Origin", "https://example.test")
		r.Header.Set("X-API-Request", "1")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		out := map[string]any{}
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	code, result := call("/account/v1/keys", token, map[string]any{"key_name": "<img src=x onerror=alert(1)>", "current_password": "Password888"})
	if code != 201 {
		t.Fatal(code, result)
	}
	id := result["credential"].(map[string]any)["id"].(string)
	old := result["api_key"].(string)
	code, result = call("/account/v1/keys/"+id+"/rotate", token, map[string]any{"current_password": "Password888", "confirm": true})
	if code != 200 {
		t.Fatal(code, result)
	}
	fresh := result["api_key"].(string)
	if fresh == old {
		t.Fatal("key unchanged")
	}
	if _, ok := p.FindCredentialByHash(auth.HashAPIKey(old)); ok {
		t.Fatal("old key valid")
	}
	if _, ok := p.FindCredentialByHash(auth.HashAPIKey(fresh)); !ok {
		t.Fatal("new key invalid")
	}
	code, result = call("/account/v1/admin/cards", atoken, map[string]any{"confirm": true, "kind": "balance", "amount_micros": 1000, "count": 2, "expires_at": time.Now().Add(time.Hour), "operation_id": ids.NewUUID(), "current_password": "Password888"})
	if code != 200 {
		t.Fatal(code, result)
	}
	rawCard := result["codes"].([]any)[0].(string)
	for i := 0; i < 2; i++ {
		code, result = call("/account/v1/redeem", token, map[string]any{"code": rawCard, "confirm": true})
		if code != 200 {
			t.Fatal(code, result)
		}
	}
	wallet, e := p.Wallet(context.Background(), u.ID)
	if e != nil || wallet.BalanceMicros != 1000 {
		t.Fatal(wallet, e)
	}
	_, otoken, e := us.Authenticate(other.Username, "Password888")
	if e != nil {
		t.Fatal(e)
	}
	if code, _ = call("/account/v1/redeem", otoken, map[string]any{"code": rawCard, "confirm": true}); code != 409 {
		t.Fatal("cross user redemption", code)
	}
	role, e := p.GetRoleByName("member")
	if e != nil {
		t.Fatal(e)
	}
	prior := role.Permissions
	defer func() {
		if e := p.UpdateRolePermissions(role.Name, prior); e != nil {
			t.Error(e)
		}
	}()
	if e = p.UpdateRolePermissions(role.Name, []string{"api.test"}); e != nil {
		t.Fatal(e)
	}
	if code, _ = call("/account/v1/keys", token, map[string]any{"key_name": "denied", "current_password": "Password888"}); code != 403 {
		t.Fatal("permission revocation not enforced", code)
	}
	// Admin change stores an unverified email; self verification is never forged.
	email := "new-" + ids.NewUUID()[:8] + "@example.test"
	_, _, e = us.UpdateProfile(admin.ID, u.ID, model.UpdateUserProfileRequest{AdminOperation: true, Email: &email, CurrentPassword: "Password888"})
	if e != nil {
		t.Fatal(e)
	}
	current, e := p.GetUserByID(u.ID)
	if e != nil || current.EmailVerified {
		t.Fatal("admin verified another mailbox", current.EmailVerified, e)
	}
}
