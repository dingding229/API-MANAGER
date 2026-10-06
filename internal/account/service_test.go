package account

import (
	"api-manager/internal/auth"
	"api-manager/internal/gateway"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/ratelimit"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTOTP6238AndInvalidCodes(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	for _, v := range []struct {
		timestamp int64
		code      string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"}} {
		step, ok := totpStep(secret, v.code, time.Unix(v.timestamp, 0))
		if !ok || step != v.timestamp/30 {
			t.Fatal(v, step, ok)
		}
	}
	for _, bad := range []string{"123", "abcdef", "0000000", "<svg>"} {
		if _, ok := totpStep(secret, bad, time.Unix(59, 0)); ok {
			t.Fatal("invalid code accepted", bad)
		}
	}
	if _, ok := totpStep("invalid secret", "287082", time.Unix(59, 0)); ok {
		t.Fatal("invalid secret")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTurnstileValidatesHostnameActionAndExpiry(t *testing.T) {
	cfg := model.SecuritySettings{TurnstileEnabled: true, TurnstileSiteKey: "public", TurnstileSecret: "private", TurnstileHost: "example.test"}
	s := &Service{}
	r := httptest.NewRequest("POST", "https://example.test/account/v1/register", nil)
	for _, v := range []struct {
		body string
		ok   bool
	}{{`{"success":true,"hostname":"example.test","action":"register"}`, true}, {`{"success":true,"hostname":"evil.test","action":"register"}`, false}, {`{"success":true,"hostname":"example.test","action":"login"}`, false}, {`{"success":false,"error-codes":["timeout-or-duplicate"]}`, false}} {
		s.client = &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != "https://challenges.cloudflare.com/turnstile/v0/siteverify" {
				t.Fatal("unexpected verification destination")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(v.body)), Header: http.Header{}}, nil
		})}
		err := s.verifyTurnstile(context.Background(), r, cfg, "token", "register")
		if (err == nil) != v.ok {
			t.Fatal(v, err)
		}
	}
	if s.verifyTurnstile(context.Background(), r, cfg, "", "register") == nil {
		t.Fatal("missing token passed")
	}
}
func TestRegistrationDefaultCannotEscalate(t *testing.T) {
	for _, code := range []string{"*", "user.manage", "credential.reveal", "api.read", "api.write", "audit.read"} {
		if publicRole(model.Role{Name: "custom", Permissions: []string{code}}) {
			t.Fatal("unsafe public role", code)
		}
	}
	if !publicRole(model.Role{Name: "member", Permissions: []string{"api.test", "api.test.write"}}) {
		t.Fatal("safe role rejected")
	}
}

type fakeMail struct{ codes map[string]string }

func (m *fakeMail) SendCode(_ context.Context, email, code, purpose string) error {
	m.codes[purpose+":"+email] = code
	return nil
}
func (m *fakeMail) MailAvailable() bool { return true }
func TestPostgresAccountLoginRegistrationEmailMFACSRF(t *testing.T) {
	dsn := os.Getenv("TEST_ACCOUNT_DSN")
	if dsn == "" {
		t.Skip("isolated account database not configured")
	}
	if !strings.Contains(dsn, "/api_account_review_") {
		t.Fatal("disposable database required")
	}
	p, e := store.NewPostgres(context.Background(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	users := user.NewService(p)
	mail := &fakeMail{codes: map[string]string{}}
	s := New(p, users, "0123456789abcdefghijklmnopqrstuvwxyz", mail, false)
	s.SetAdminPath("/console")
	s.limiter = ratelimit.NewMemory()
	cfg := model.SecuritySettings{RegistrationEnabled: true, EmailLoginEnabled: true, DefaultRole: "member", WebsiteURL: "https://example.test"}
	previous, _, e := p.SecuritySettings(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	cfg.Version = previous.Version
	if e = p.SaveSecuritySettings(context.Background(), cfg, ""); e != nil {
		t.Fatal(e)
	}
	call := func(method, path string, body any, cookie *http.Cookie) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "https://example.test"+path, strings.NewReader(string(data)))
		r.Header.Set("Origin", "https://example.test")
		r.Header.Set("X-API-Request", "1")
		r.Header.Set("User-Agent", "Account Test")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		out := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w, out
	}
	email := "http-test@example.test"
	w, v := call("POST", "/account/v1/send-code", map[string]any{"email": email, "purpose": "register"}, nil)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	code := mail.codes["register:"+email]
	w, _ = call("POST", "/account/v1/register", map[string]any{"email": email, "username": "http-account", "nickname": "昵称", "password": "TestPass888", "verification_id": v["verification_id"], "code": code}, nil)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	u, e := p.GetUserByUsername("http-account")
	if e != nil || !u.EmailVerified || u.Nickname != "昵称" || u.UID != u.ID {
		t.Fatal(u, e)
	}
	w, _ = call("POST", "/auth/v1/login", map[string]any{"username": u.Username, "password": "TestPass888"}, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var session *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			session = c
		}
	}
	if session == nil {
		t.Fatal("no shared cookie")
	}
	w, _ = call("GET", "/account/v1/me", nil, session)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w, setup := call("POST", "/account/v1/totp", map[string]any{"purpose": "begin", "current_password": "TestPass888"}, session)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	secret := setup["secret"].(string)
	encrypted, e := auth.EncryptSecret(s.key+":totp:"+u.ID, secret)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.BeginTOTP(context.Background(), u.ID, encrypted); e != nil {
		t.Fatal(e)
	}
	if e = p.EnableTOTP(context.Background(), u.ID, encrypted, time.Now().Unix()/30-1, []string{auth.HashAPIKey("rc_test-recovery")}); e != nil {
		t.Fatal(e)
	}
	w, _ = call("POST", "/auth/v1/login", map[string]any{"username": u.Username, "password": "TestPass888"}, nil)
	if w.Code != 202 {
		t.Fatal("password bypasses MFA", w.Code, w.Body.String())
	}
	var result map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	challenge := result["challenge_id"]
	w, _ = call("POST", "/account/v1/mfa-login", map[string]any{"challenge_id": challenge, "totp_code": "rc_test-recovery"}, nil)
	if w.Code != 200 {
		t.Fatal("recovery login failed", w.Code, w.Body.String())
	}
	w, _ = call("POST", "/account/v1/mfa-login", map[string]any{"challenge_id": challenge, "totp_code": "rc_test-recovery"}, nil)
	if w.Code == 200 {
		t.Fatal("MFA replay")
	}
	w, v = call("POST", "/account/v1/send-code", map[string]any{"email": email, "purpose": "email-login"}, nil)
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	w, _ = call("POST", "/account/v1/email-login", map[string]any{"verification_id": v["verification_id"], "code": mail.codes["email-login:"+email]}, nil)
	if w.Code != 202 {
		t.Fatal("email login bypasses MFA", w.Code, w.Body.String())
	}
	w, _ = call("GET", "/auth/v1/login", nil, nil)
	if w.Code != 405 {
		t.Fatal("method not enforced")
	}
	r := httptest.NewRequest("PUT", "https://example.test/account/v1/basic", strings.NewReader(`{"nickname":"changed"}`))
	r.AddCookie(session)
	r.Header.Set("Origin", "https://evil.test")
	r.Header.Set("X-API-Request", "1")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-site mutation accepted", w.Code)
	}
	latest, _, e := p.SecuritySettings(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = p.SaveSecuritySettings(context.Background(), model.SecuritySettings{DefaultRole: "member", Version: latest.Version}, ""); e != nil {
		t.Fatal(e)
	}
}

func TestPostgresPaidGatewayKeyIsolationAndRefund(t *testing.T) {
	dsn := os.Getenv("TEST_ACCOUNT_DSN")
	if dsn == "" {
		t.Skip("isolated account database not configured")
	}
	if !strings.Contains(dsn, "/api_account_review_") {
		t.Fatal("disposable database required")
	}
	p, e := store.NewPostgres(context.Background(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	users := user.NewService(p)
	uname := "billing-" + ids.NewUUID()
	u, e := users.CreateWithContact(uname, uname+"@example.test", "TestPass888", []string{"member"})
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if _, e = p.AdjustBalance(ctx, u.ID, 1000, "credit:"+u.ID, "test"); e != nil {
		t.Fatal(e)
	}
	raw := "ak_" + ids.NewUUID()
	c := model.Credential{ID: ids.NewUUID(), OwnerUserID: u.ID, Name: "owned", Prefix: "ak_test", Hash: auth.HashAPIKey(raw), CreatedAt: time.Now()}
	if e = p.CreateCredential(c); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	a := model.API{ID: ids.NewUUID(), Name: "priced", Method: "GET", Path: "/api/" + ids.NewUUID(), AuthMode: "api_key", PriceMicros: 100, ResponseStatus: 200, ResponseBody: "{}", Enabled: true, PublishedAt: &now, CreatedAt: now, UpdatedAt: now}
	if e = p.CreateAPI(a); e != nil {
		t.Fatal(e)
	}
	g := gateway.NewWithMetrics(p, plugin.NewRegistry(), ratelimit.NewMemory(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	invoke := func(key string, cookie bool) int {
		r := httptest.NewRequest("GET", a.Path, nil)
		if key != "" {
			r.Header.Set("X-API-Key", key)
		}
		if cookie {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "us_cannot_replace_key"})
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w.Code
	}
	if invoke("", true) != 401 {
		t.Fatal("web cookie became business KEY")
	}
	if invoke(raw, false) != 200 {
		t.Fatal("owned KEY rejected")
	}
	w, e := p.Wallet(ctx, u.ID)
	if e != nil || w.BalanceMicros != 900 || w.HeldMicros != 0 {
		t.Fatal(w, e)
	}
	a.ResponseStatus = 500
	a.UpdatedAt = time.Now().UTC()
	if e = p.UpdateAPI(a); e != nil {
		t.Fatal(e)
	}
	if invoke(raw, false) != 500 {
		t.Fatal("failure fixture")
	}
	w, e = p.Wallet(ctx, u.ID)
	if e != nil || w.BalanceMicros != 900 || w.HeldMicros != 0 {
		t.Fatal("failed response billed", w, e)
	}
	logs, e := p.CallLogs(ctx, u.ID)
	if e != nil || len(logs) != 2 {
		t.Fatal(logs, e)
	}
	other := "ak_" + ids.NewUUID()
	if e = p.CreateCredential(model.Credential{ID: ids.NewUUID(), Name: "legacy", Hash: auth.HashAPIKey(other), CreatedAt: now}); e != nil {
		t.Fatal(e)
	}
	if invoke(other, false) != 403 {
		t.Fatal("paid API accepts unowned legacy key")
	}
}
