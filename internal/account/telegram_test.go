package account

import (
	"api-manager/internal/auth"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTelegramIdentityRejectsTamperingAndInvalidClaims(t *testing.T) {
	private, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	key := telegramKey{Kid: "test", Kty: "RSA", Alg: "RS256", N: base64.RawURLEncoding.EncodeToString(private.N.Bytes()), E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(private.E)).Bytes())}
	s := &Service{client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != telegramJWKS {
			t.Fatal("untrusted signing destination", r.URL.String())
		}
		body, _ := json.Marshal(map[string]any{"keys": []telegramKey{key}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: http.Header{}}, nil
	})}}
	sign := func(claims map[string]any, algorithm string) string {
		header, _ := json.Marshal(map[string]string{"alg": algorithm, "kid": "test"})
		body, _ := json.Marshal(claims)
		message := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
		hash := sha256.Sum256([]byte(message))
		signature, e := rsa.SignPKCS1v15(rand.Reader, private, crypto.SHA256, hash[:])
		if e != nil {
			t.Fatal(e)
		}
		return message + "." + base64.RawURLEncoding.EncodeToString(signature)
	}
	base := func() map[string]any {
		return map[string]any{"iss": telegramIssuer, "aud": "123456", "sub": "42", "name": "用户", "nonce": "server-nonce", "iat": time.Now().Unix() - 1, "exp": time.Now().Unix() + 300}
	}
	good := sign(base(), "RS256")
	sub, name, e := s.telegramIdentity(context.Background(), good, "123456", "server-nonce")
	if e != nil || sub != "42" || name != "用户" {
		t.Fatal(sub, name, e)
	}
	for _, change := range []struct {
		k string
		v any
	}{{"iss", "https://evil.test"}, {"aud", "other-client"}, {"nonce", "other-nonce"}, {"exp", time.Now().Unix() - 1}, {"iat", time.Now().Unix() + 120}, {"sub", ""}, {"aud", []string{"123456", "other-client"}}} {
		claims := base()
		claims[change.k] = change.v
		if _, _, e = s.telegramIdentity(context.Background(), sign(claims, "RS256"), "123456", "server-nonce"); e == nil {
			t.Fatal("invalid identity accepted", change.k)
		}
	}
	for _, bad := range []string{"", good + "x", sign(base(), "none"), strings.Repeat("a", 17000)} {
		if _, _, e = s.telegramIdentity(context.Background(), bad, "123456", "server-nonce"); e == nil {
			t.Fatal("tampered identity accepted")
		}
	}
}
func TestTelegramAuthorizationUsesPKCEAndSameOriginCallback(t *testing.T) {
	s := &Service{adminPath: "/admin"}
	if oauthReturn("https://evil.test", s.adminPath) != "/account" || oauthReturn("home", s.adminPath) != "/" {
		t.Fatal("unsafe return")
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "https://example.test/account/v1/oauth/telegram/start", strings.NewReader(`{}`))
	s.oauth(w, r, model.SecuritySettings{})
	if w.Code != 403 {
		t.Fatal("disabled Telegram accepted", w.Code)
	}
}

func TestPostgresTelegramOIDCLinkLoginAndNoUnverifiedRegistration(t *testing.T) {
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
	u, e := us.Create("telegram-user-"+ids.NewUUID()[:8], "Password888", "member")
	if e != nil {
		t.Fatal(e)
	}
	_, session, _ := us.Authenticate(u.Username, "Password888")
	encryptionKey := "0123456789abcdefghijklmnopqrstuvwxyz"
	cfg, _, e := p.SecuritySettings(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	cfg.TelegramEnabled = true
	cfg.TelegramClientID = "123456"
	cfg.WebsiteURL = "https://example.test"
	encrypted, e := auth.EncryptSecret(encryptionKey+":authentication", `{"Telegram":"test-client-secret"}`)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.SaveSecuritySettings(context.Background(), cfg, encrypted); e != nil {
		t.Fatal(e)
	}
	s := New(p, us, encryptionKey, &fakeMail{codes: map[string]string{}}, false)
	private, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	nonce := ""
	subject := "telegram-subject-" + ids.NewUUID()
	s.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		var body []byte
		switch r.URL.String() {
		case telegramIssuer + "/token":
			id, secret, ok := r.BasicAuth()
			if !ok || id != "123456" || secret != "test-client-secret" {
				t.Fatal("wrong OIDC client authentication")
			}
			if e := r.ParseForm(); e != nil || len(r.Form.Get("code_verifier")) != 64 || r.Form.Get("client_secret") != "" {
				t.Fatal("PKCE/code exchange invalid")
			}
			header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"test"}`))
			claims, _ := json.Marshal(map[string]any{"iss": telegramIssuer, "aud": "123456", "sub": subject, "nonce": nonce, "iat": time.Now().Unix() - 1, "exp": time.Now().Unix() + 300})
			message := header + "." + base64.RawURLEncoding.EncodeToString(claims)
			hash := sha256.Sum256([]byte(message))
			signature, _ := rsa.SignPKCS1v15(rand.Reader, private, crypto.SHA256, hash[:])
			body, _ = json.Marshal(map[string]string{"access_token": "server-only", "id_token": message + "." + base64.RawURLEncoding.EncodeToString(signature)})
		case telegramJWKS:
			body, _ = json.Marshal(map[string]any{"keys": []telegramKey{{Kid: "test", Kty: "RSA", Alg: "RS256", N: base64.RawURLEncoding.EncodeToString(private.N.Bytes()), E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(private.E)).Bytes())}}})
		default:
			t.Fatal("unexpected identity destination", r.URL.String())
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: http.Header{}}, nil
	})
	flow := func(link bool) *httptest.ResponseRecorder {
		payload := `{"return_to":"home"}`
		if link {
			payload = `{"purpose":"link","current_password":"Password888"}`
		}
		r := httptest.NewRequest("POST", "https://example.test/account/v1/oauth/telegram/start", strings.NewReader(payload))
		r.Header.Set("Origin", "https://example.test")
		r.Header.Set("X-API-Request", "1")
		r.Header.Set("User-Agent", "review")
		if link {
			r.Header.Set("Authorization", "Bearer "+session)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var result map[string]string
		json.Unmarshal(w.Body.Bytes(), &result)
		authorize, e := url.Parse(result["url"])
		if e != nil || authorize.Host != "oauth.telegram.org" || authorize.Query().Get("code_challenge_method") != "S256" {
			t.Fatal(result)
		}
		nonce = authorize.Query().Get("nonce")
		callback := httptest.NewRequest("GET", "https://example.test/account/v1/oauth/telegram/callback?code=test-code&state="+url.QueryEscape(authorize.Query().Get("state")), nil)
		callback.Header.Set("User-Agent", "review")
		for _, cookie := range w.Result().Cookies() {
			callback.AddCookie(cookie)
		}
		done := httptest.NewRecorder()
		s.ServeHTTP(done, callback)
		return done
	}
	if w := flow(true); w.Code != 303 {
		t.Fatal("link failed", w.Code, w.Body.String())
	}
	w := flow(false)
	if w.Code != 303 || w.Header().Get("Location") != "https://example.test/" {
		t.Fatal("login failed", w.Code, w.Body.String())
	}
	found := false
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			logged, e := us.ValidateSession(c.Value)
			if e != nil || logged.ID != u.ID {
				t.Fatal("incorrect account")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no session")
	}
	count := p.CountUsers()
	subject = "unlinked-" + ids.NewUUID()
	if w := flow(false); w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "/complete-registration") {
		t.Fatal("new Telegram account did not require email onboarding", w.Code)
	}
	if p.CountUsers() != count {
		t.Fatal("created account with invented email")
	}
}

func TestPostgresTelegramOnboardingRequiresVerifiedEmailAndOneUse(t *testing.T) {
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
	mail := &fakeMail{codes: map[string]string{}}
	s := New(p, us, "0123456789abcdefghijklmnopqrstuvwxyz", mail, false)
	cfg, _, e := p.SecuritySettings(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	cfg.DefaultRole = "member"
	cfg.TelegramEnabled = true
	cfg.TelegramClientID = "123456"
	cfg.AllowedEmailDomains = []string{"allowed.test"}
	if e = p.SaveSecuritySettings(context.Background(), cfg, ""); e != nil {
		t.Fatal(e)
	}
	start := httptest.NewRequest("GET", "https://example.test/account/v1/oauth/telegram/callback", nil)
	start.Header.Set("User-Agent", "review")
	begin := httptest.NewRecorder()
	subject := "onboard-" + ids.NewUUID()
	s.beginTelegramOnboarding(begin, start, cfg, subject, "用户", "/")
	if begin.Code != 303 {
		t.Fatal(begin.Code)
	}
	var cookie *http.Cookie
	for _, c := range begin.Result().Cookies() {
		if c.Name == onboardingCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("missing onboarding cookie")
	}
	if _, e = p.FindIdentity(context.Background(), "telegram", subject); e == nil {
		t.Fatal("account created before mailbox proof")
	}
	call := func(path string, data map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(data)
		r := httptest.NewRequest("POST", "https://example.test/account/v1/onboarding"+path, strings.NewReader(string(raw)))
		r.Header.Set("Origin", "https://example.test")
		r.Header.Set("X-API-Request", "1")
		r.Header.Set("User-Agent", "review")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := call("/send-code", map[string]any{"email": "a@other.test"}); w.Code != 400 {
		t.Fatal("domain policy bypass", w.Code)
	}
	email := "u-" + ids.NewUUID()[:8] + "@allowed.test"
	w := call("/send-code", map[string]any{"email": email})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var proof map[string]string
	json.Unmarshal(w.Body.Bytes(), &proof)
	username := "tg-" + ids.NewUUID()[:8]
	body := map[string]any{"username": username, "email": email, "password": "Password888", "nickname": "用户", "verification_id": proof["verification_id"], "code": "wrong"}
	if w := call("/complete", body); w.Code != 403 {
		t.Fatal("unverified mailbox accepted", w.Code)
	}
	body["code"] = mail.codes["register:"+email]
	w = call("/complete", body)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	u, e := p.GetUserByUsername(username)
	if e != nil || !u.EmailVerified {
		t.Fatal("mailbox not verified", e)
	}
	id, e := p.FindIdentity(context.Background(), "telegram", subject)
	if e != nil || id != u.ID {
		t.Fatal("identity link missing")
	}
	if w := call("/complete", body); w.Code != 401 {
		t.Fatal("onboarding replayed", w.Code)
	}
}
