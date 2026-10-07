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
	if !publicRole(model.Role{Name: "member", Permissions: []string{"api.test"}}) {
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
	uname := "billing-" + ids.NewUUID()[:8]
	u, e := users.CreateWithContact(uname, uname+"@example.test", "TestPass888", []string{"member"})
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if _, e = p.AdjustBalance(ctx, u.ID, 1000, "credit:"+u.ID, "test"); e != nil {
		t.Fatal(e)
	}
	raw := "ak_" + ids.NewUUID()[:8]
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
	other := "ak_" + ids.NewUUID()[:8]
	if e = p.CreateCredential(model.Credential{ID: ids.NewUUID(), Name: "legacy", Hash: auth.HashAPIKey(other), CreatedAt: now}); e != nil {
		t.Fatal(e)
	}
	if invoke(other, false) != 403 {
		t.Fatal("paid API accepts unowned legacy key")
	}
}

func TestPostgresOwnKeyRevealAndLogsCannotCrossUsers(t *testing.T) {
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
	cfg, _, e := p.SecuritySettings(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = p.SaveSecuritySettings(context.Background(), model.SecuritySettings{Version: cfg.Version, DefaultRole: "member"}, ""); e != nil {
		t.Fatal(e)
	}
	owner, e := users.Create("key-owner-"+ids.NewUUID()[:8], "Password888", "member")
	if e != nil {
		t.Fatal(e)
	}
	other, e := users.Create("key-other-"+ids.NewUUID()[:8], "Password888", "member")
	if e != nil {
		t.Fatal(e)
	}
	_, token, e := users.Authenticate(owner.Username, "Password888")
	if e != nil {
		t.Fatal(e)
	}
	_, foreign, e := users.Authenticate(other.Username, "Password888")
	if e != nil {
		t.Fatal(e)
	}
	s := New(p, users, "0123456789abcdefghijklmnopqrstuvwxyz", &fakeMail{codes: map[string]string{}}, false)
	secret := "ak_" + ids.NewUUID()[:8]
	encrypted, e := auth.EncryptSecret(s.key, secret)
	if e != nil {
		t.Fatal(e)
	}
	key := model.Credential{ID: ids.NewUUID(), OwnerUserID: owner.ID, Name: "test", Prefix: "ak_safe", Hash: auth.HashAPIKey(secret), EncryptedKey: encrypted, CreatedAt: time.Now()}
	if e = p.CreateOwnedCredential(context.Background(), key); e != nil {
		t.Fatal(e)
	}
	call := func(method, path, session, password string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]string{"current_password": password})
		r := httptest.NewRequest(method, "https://example.test"+path, strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer "+session)
		r.Header.Set("Origin", "https://example.test")
		r.Header.Set("X-API-Request", "1")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	path := "/account/v1/keys/" + key.ID + "/reveal"
	if w := call("POST", path, foreign, "Password888"); w.Code != 404 {
		t.Fatal("cross-user secret", w.Code, w.Body.String())
	}
	if w := call("POST", path, token, "wrong"); w.Code != 403 {
		t.Fatal("reauth omitted", w.Code)
	}
	if w := call("POST", path, token, "Password888"); w.Code != 200 || !strings.Contains(w.Body.String(), secret) || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatal(w.Code, w.Body.String())
	}
	logs, e := p.ListAuditLogs(model.AuditLogQuery{Action: "account.key.reveal"})
	if e != nil || logs.Total < 1 {
		t.Fatal("secret access not audited", e)
	}
	if w := call("GET", "/account/v1/logs?user_id="+other.ID, token, ""); w.Code != 403 {
		t.Fatal("cross-user logs", w.Code)
	}
	if w := call("GET", "/account/v1/admin/credentials", token, ""); w.Code != 403 {
		t.Fatal("ordinary user accessed global credentials", w.Code)
	}
	key.Revoked = true
	if e = p.UpdateCredential(key); e != nil {
		t.Fatal(e)
	}
	if w := call("POST", path, token, "Password888"); w.Code != 409 {
		t.Fatal("revoked key revealed", w.Code)
	}
}

func TestPostgresUserCenterSessionAdministrationIsScoped(t *testing.T) {
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
	admin, e := users.Create("session-admin-"+ids.NewUUID()[:8], "Password888", "super_admin")
	if e != nil {
		t.Fatal(e)
	}
	member, e := users.Create("session-member-"+ids.NewUUID()[:8], "Password888", "member")
	if e != nil {
		t.Fatal(e)
	}
	_, adminToken, e := users.Authenticate(admin.Username, "Password888")
	if e != nil {
		t.Fatal(e)
	}
	_, memberToken, e := users.Authenticate(member.Username, "Password888")
	if e != nil {
		t.Fatal(e)
	}
	cfg, _, e := p.SecuritySettings(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = p.SaveSecuritySettings(context.Background(), model.SecuritySettings{Version: cfg.Version, DefaultRole: "member"}, ""); e != nil {
		t.Fatal(e)
	}
	s := New(p, users, "0123456789abcdefghijklmnopqrstuvwxyz", &fakeMail{codes: map[string]string{}}, false)
	call := func(method, path, token string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "https://example.test"+path, strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Origin", "https://example.test")
		r.Header.Set("X-API-Request", "1")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "/account/v1/sessions?user_id="+admin.ID, memberToken, nil); w.Code != 403 {
		t.Fatal("ordinary user read foreign sessions", w.Code)
	}
	if w := call("GET", "/account/v1/admin/logs", adminToken, nil); w.Code != 200 {
		t.Fatal("administrator log route unavailable", w.Code, w.Body.String())
	}
	if w := call("GET", "/account/v1/admin/logs", memberToken, nil); w.Code != 403 {
		t.Fatal("ordinary user queried all logs", w.Code)
	}
	if w := call("GET", "/account/v1/session-users", memberToken, nil); w.Code != 403 {
		t.Fatal("global users exposed", w.Code)
	}
	current, e := users.CurrentSession(memberToken)
	if e != nil {
		t.Fatal(e)
	}
	if w := call("DELETE", "/account/v1/sessions/"+current.ID, adminToken, map[string]any{"user_id": member.ID, "confirm": true}); w.Code != 403 {
		t.Fatal("admin reauth omitted", w.Code)
	}
	if w := call("DELETE", "/account/v1/sessions/"+current.ID, adminToken, map[string]any{"user_id": member.ID, "confirm": true, "current_password": "Password888"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, e = users.ValidateSession(memberToken); e == nil {
		t.Fatal("revoked session survived")
	}
}

func TestPostgresAdminOperationsDoNotRequireOTPButLoginStillDoes(t *testing.T) {
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
	u, e := users.Create("otp-admin-"+ids.NewUUID()[:8], "Password888", "super_admin")
	if e != nil {
		t.Fatal(e)
	}
	_, token, e := users.Authenticate(u.Username, "Password888")
	if e != nil {
		t.Fatal(e)
	}
	cfg, _, e := p.SecuritySettings(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = p.SaveSecuritySettings(context.Background(), model.SecuritySettings{Version: cfg.Version, DefaultRole: "member"}, ""); e != nil {
		t.Fatal(e)
	}
	s := New(p, users, "0123456789abcdefghijklmnopqrstuvwxyz", &fakeMail{codes: map[string]string{}}, false)
	secret, e := auth.EncryptSecret(s.key+":totp:"+u.ID, "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ")
	if e != nil {
		t.Fatal(e)
	}
	if e = p.BeginTOTP(context.Background(), u.ID, secret); e != nil {
		t.Fatal(e)
	}
	if e = p.EnableTOTP(context.Background(), u.ID, secret, 0, []string{auth.HashAPIKey("rc_admin-test")}); e != nil {
		t.Fatal(e)
	}
	fresh, e := p.GetUserByID(u.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, token, e = users.StartSession(fresh, nil)
	if e != nil {
		t.Fatal(e)
	}
	call := func(path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "https://example.test"+path, strings.NewReader(string(raw)))
		r.Header.Set("Origin", "https://example.test")
		r.Header.Set("X-API-Request", "1")
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	target, e := users.Create("grant-user-"+ids.NewUUID()[:8], "Password888", "member")
	if e != nil {
		t.Fatal(e)
	}
	plan := model.Plan{ID: ids.NewUUID(), Name: "人工绑定", Days: 30, Daily: 100, Enabled: true}
	if e = p.SavePlan(context.Background(), plan); e != nil {
		t.Fatal(e)
	}
	body := map[string]any{"user_id": target.ID, "plan_id": plan.ID, "note": "测试", "operation_id": ids.NewUUID(), "confirm": true, "current_password": "Password888"}
	if w := call("/account/v1/admin/plan-assignment", body); w.Code != 200 {
		t.Fatal("admin OTP still required", w.Code, w.Body.String())
	}
	if w := call("/auth/v1/login", map[string]string{"username": u.Username, "password": "Password888"}); w.Code != 202 {
		t.Fatal("login MFA bypassed", w.Code, w.Body.String())
	}
	if w := call("/account/v1/keys", map[string]string{"key_name": "test", "current_password": "Password888"}); w.Code != 403 {
		t.Fatal("personal security OTP bypassed", w.Code)
	}
	username := u.Username
	profile := model.UpdateUserProfileRequest{Username: &username, CurrentPassword: "Password888"}
	if _, _, err := users.UpdateProfile(u.ID, u.ID, profile); err == nil {
		t.Fatal("personal profile OTP bypassed")
	}
	profile.AdminOperation = true
	if _, _, err := users.UpdateProfile(u.ID, u.ID, profile); err != nil {
		t.Fatal("backend self edit still requires OTP", err)
	}
}

type websiteMail struct {
	*fakeMail
	info model.PublicSiteInfo
}

func (m *websiteMail) Public() model.PublicSiteInfo { return m.info }
func TestPostgresAuthenticationUsesWebsiteOriginAndTurnstileHostname(t *testing.T) {
	dsn := os.Getenv("TEST_ACCOUNT_DSN")
	if dsn == "" {
		t.Skip("isolated account database not configured")
	}
	if !strings.Contains(dsn, "/api_account_review_") {
		t.Fatal("disposable database required")
	}
	p, err := store.NewPostgres(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	mail := &websiteMail{fakeMail: &fakeMail{codes: map[string]string{}}, info: model.PublicSiteInfo{WebsiteURL: "https://site.example.test"}}
	s := New(p, user.NewService(p), "0123456789abcdefghijklmnopqrstuvwxyz", mail, false)
	cfg, err := s.settings(context.Background())
	if err != nil || cfg.WebsiteURL != mail.info.WebsiteURL || cfg.TurnstileHost != "site.example.test" {
		t.Fatal(cfg.WebsiteURL, cfg.TurnstileHost, err)
	}
	mail.info.WebsiteURL = "https://new.example.test"
	cfg, err = s.settings(context.Background())
	if err != nil || cfg.WebsiteURL != mail.info.WebsiteURL || cfg.TurnstileHost != "new.example.test" {
		t.Fatal("domain change not propagated", err)
	}
}
