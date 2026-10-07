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
	batchID := result["batch"].(map[string]any)["id"].(string)
	rawCard := result["codes"].([]any)[0].(string)
	unusedCard := result["codes"].([]any)[1].(string)
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

	getItems := func(session string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "https://example.test/account/v1/admin/cards/"+batchID+"/items", nil)
		if session != "" {
			r.Header.Set("Authorization", "Bearer "+session)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := getItems(""); w.Code != 401 {
		t.Fatal("anonymous card records", w.Code)
	}
	if w := getItems(token); w.Code != 403 {
		t.Fatal("member card records", w.Code)
	}
	w := getItems(atoken)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var batch model.CardBatch
	if e := json.Unmarshal(w.Body.Bytes(), &batch); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(w.Body.String(), rawCard) || strings.Contains(w.Body.String(), unusedCard) || strings.Contains(w.Body.String(), "code_hash") || strings.Contains(w.Body.String(), "encrypted_code") {
		t.Fatal("card record leaked a secret")
	}
	var used, unused int
	for _, c := range batch.Cards {
		switch c.Status {
		case "used":
			used++
			if c.RedeemedBy == nil || *c.RedeemedBy != u.ID || c.RedeemedUsername != u.Username || c.RedeemedAt == nil {
				t.Fatal("missing card consumer", c)
			}
		case "unused":
			unused++
		}
	}
	if used != 1 || unused != 1 {
		t.Fatal("wrong card statuses", used, unused)
	}
	revealPath := "/account/v1/admin/cards/" + batchID + "/reveal"
	if code, _ := call(revealPath, atoken, map[string]any{"confirm": true, "purpose": "all"}); code != 403 {
		t.Fatal("card reveal omitted reauthentication", code)
	}
	for _, scope := range []struct {
		purpose, expected string
		count             int
	}{{"", unusedCard, 1}, {"unused", unusedCard, 1}, {"used", rawCard, 1}, {"all", "", 2}} {
		code, result = call(revealPath, atoken, map[string]any{"confirm": true, "purpose": scope.purpose, "current_password": "Password888"})
		if code != 200 {
			t.Fatal(scope.purpose, code, result)
		}
		codes := result["codes"].([]any)
		if len(codes) != scope.count || (scope.expected != "" && codes[0].(string) != scope.expected) {
			t.Fatal("card reveal filter failed", scope.purpose)
		}
	}
	if code, _ := call(revealPath, atoken, map[string]any{"confirm": true, "purpose": "invalid", "current_password": "Password888"}); code != 400 {
		t.Fatal("invalid reveal filter", code)
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
