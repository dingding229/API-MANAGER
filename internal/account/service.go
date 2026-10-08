package account

import (
	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"api-manager/internal/httpx"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"api-manager/internal/ratelimit"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"context"
	"crypto/hmac"
	"crypto/rand"
	// #nosec G505 -- RFC 6238 interoperability requires HMAC-SHA1; not used for password hashing.
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Mail interface {
	SendCode(context.Context, string, string, string) error
	MailAvailable() bool
}
type Service struct {
	telegramCache telegramKeyCache
	limiter       ratelimit.Limiter
	store         store.Store
	accounts      store.AccountStore
	users         *user.Service
	key           string
	mail          Mail
	production    bool
	client        *http.Client
	adminPath     string
}

func New(s store.Store, u *user.Service, key string, mail Mail, production bool) *Service {
	a, _ := s.(store.AccountStore)
	service := &Service{store: s, accounts: a, users: u, key: key, mail: mail, production: production, limiter: ratelimit.NewMemory(), client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect denied") }}}
	if a != nil {
		u.SetProfileGuard(service.profileGuard)
		u.SetProfileRequestGuard(service.Guard)
	}
	return service
}
func (s *Service) settings(ctx context.Context) (model.SecuritySettings, error) {
	if s.accounts == nil {
		return model.SecuritySettings{}, errors.New("account store unavailable")
	}
	cfg, encrypted, err := s.accounts.SecuritySettings(ctx)
	if err != nil {
		return cfg, err
	}
	if encrypted != "" {
		raw, e := auth.DecryptSecret(s.key+":authentication", encrypted)
		if e != nil {
			return cfg, e
		}
		var secrets struct{ Turnstile, GitHub, Google, Telegram string }
		if e = json.Unmarshal([]byte(raw), &secrets); e != nil {
			return cfg, e
		}
		cfg.TurnstileSecret = secrets.Turnstile
		cfg.GitHubSecret = secrets.GitHub
		cfg.GoogleSecret = secrets.Google
		cfg.TelegramSecret = secrets.Telegram
	}
	if provider, ok := s.mail.(interface{ Public() model.PublicSiteInfo }); ok {
		if origin := provider.Public().WebsiteURL; origin != "" {
			cfg.WebsiteURL = origin
		}
	}
	if origin, err := url.Parse(cfg.WebsiteURL); err == nil && origin.Hostname() != "" {
		cfg.TurnstileHost = origin.Hostname()
	}
	if cfg.DefaultRole == "" {
		cfg.DefaultRole = "member"
	}
	return cfg, nil
}
func (s *Service) binding(r *http.Request) string {
	return auth.HashAPIKey(httpx.Client(r).IP + "\n" + r.UserAgent())
}
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
func (s *Service) putChallenge(ctx context.Context, purpose, subject, userID, binding, payload string) (string, error) {
	id := ids.NewUUID()
	secret := randomHex(32)
	if secret == "" {
		return "", errors.New("random unavailable")
	}
	err := s.accounts.PutVerification(ctx, model.Verification{ID: id, Purpose: purpose, Subject: subject, UserID: userID, Binding: binding, CodeHash: auth.HashAPIKey(secret), Payload: payload, ExpiresAt: time.Now().Add(5 * time.Minute)})
	return id + ":" + secret, err
}
func (s *Service) consume(ctx context.Context, value, purpose, binding string) (model.Verification, error) {
	id, secret, ok := strings.Cut(value, ":")
	if !ok || len(secret) != 64 {
		return model.Verification{}, store.ErrConflict
	}
	return s.accounts.ConsumeVerification(ctx, id, purpose, auth.HashAPIKey(secret), binding)
}
func (s *Service) verifyTurnstile(ctx context.Context, r *http.Request, cfg model.SecuritySettings, token, action string) error {
	if !cfg.TurnstileEnabled {
		return nil
	}
	if cfg.TurnstileSecret == "" || cfg.TurnstileSiteKey == "" || token == "" || len(token) > 2048 {
		return errors.New("请完成人机验证")
	}
	form := url.Values{"secret": {cfg.TurnstileSecret}, "response": {token}, "remoteip": {httpx.Client(r).IP}, "idempotency_key": {ids.NewUUID()}}
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://challenges.cloudflare.com/turnstile/v0/siteverify", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return errors.New("人机验证服务暂不可用")
	}
	defer resp.Body.Close()
	var result struct {
		Success          bool
		Hostname, Action string
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&result) != nil || !result.Success || !strings.EqualFold(result.Hostname, cfg.TurnstileHost) || result.Action != action {
		return errors.New("人机验证无效，请重新验证")
	}
	return nil
}
func totpStep(secret, code string, now time.Time) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return 0, false
	}
	step := now.Unix() / 30
	for offset := int64(-1); offset <= 1; offset++ {
		candidate := step + offset
		if candidate < 0 {
			continue
		}
		var msg [8]byte
		binary.BigEndian.PutUint64(msg[:], uint64(candidate))
		// #nosec G401 -- HMAC-SHA1 is the specified interoperable TOTP algorithm, not a standalone digest.
		mac := hmac.New(sha1.New, key)
		_, _ = mac.Write(msg[:])
		sum := mac.Sum(nil)
		i := sum[len(sum)-1] & 15
		number := binary.BigEndian.Uint32(sum[i:i+4]) & 0x7fffffff
		expected := fmt.Sprintf("%06d", number%1000000)
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return candidate, true
		}
	}
	return 0, false
}
func (s *Service) checkMFA(ctx context.Context, id, code string) error {
	if s.accounts == nil {
		return errors.New("account store unavailable")
	}
	a, err := s.accounts.Account(ctx, id)
	if err != nil {
		return err
	}
	if a.TOTPSecret == "" {
		return nil
	}
	if strings.HasPrefix(code, "rc_") {
		return s.accounts.AcceptTOTP(ctx, id, a.TOTPSecret, 0, auth.HashAPIKey(code))
	}
	secret, err := auth.DecryptSecret(s.key+":totp:"+id, a.TOTPSecret)
	if err != nil {
		return errors.New("二次验证暂不可用")
	}
	step, ok := totpStep(secret, code, time.Now())
	if !ok {
		return errors.New("动态码不正确")
	}
	return s.accounts.AcceptTOTP(ctx, id, a.TOTPSecret, step, "")
}
func (s *Service) loginResponse(w http.ResponseWriter, r *http.Request, u model.User) {
	a, err := s.accounts.Account(r.Context(), u.ID)
	if err != nil {
		write(w, 503, map[string]string{"error": "账号服务不可用"})
		return
	}
	if a.TOTPSecret != "" {
		challenge, e := s.putChallenge(r.Context(), "mfa-login", u.ID, u.ID, s.binding(r), loginFingerprint(u))
		if e != nil {
			write(w, 503, map[string]string{"error": "无法创建二次验证"})
			return
		}
		// #nosec G124 -- Secure is forced in production; only explicit non-production loopback previews allow HTTP. Cookie contains a short-lived, IP/UA-bound MFA challenge, never a full session.
		http.SetCookie(w, &http.Cookie{Name: "api_manager_mfa", Value: challenge, Path: "/account/v1", HttpOnly: true, Secure: s.production || r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 300})
		write(w, 202, map[string]any{"mfa_required": true, "challenge_id": challenge})
		return
	}
	s.completeLogin(w, r, u)
}
func (s *Service) completeLogin(w http.ResponseWriter, r *http.Request, u model.User) {
	u, token, err := s.users.StartSession(u, r)
	if err != nil {
		write(w, 503, map[string]string{"error": "登录暂不可用"})
		return
	}
	if previous, _ := auth.SessionToken(r); previous != "" && previous != token {
		if err = s.users.Logout(previous); err != nil {
			_ = s.users.Logout(token)
			write(w, 503, map[string]string{"error": "旧会话退出失败，请重试登录"})
			return
		}
	}
	user.SetSessionCookie(w, r, token, s.production)
	s.users.RecordAudit(auditActor(u), r, "auth.login", "user", u.ID, 200, nil)
	if r.URL.Path == "/test/v1/login" {
		write(w, 200, map[string]string{"username": u.Username})
		return
	}
	result := s.users.Profile(u)
	if r.Header.Get("X-API-Request") != "1" {
		result["token"] = token
	}
	write(w, 200, result)
}
func write(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func read(w http.ResponseWriter, r *http.Request, target any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil || d.Decode(&struct{}{}) != io.EOF {
		write(w, 400, map[string]string{"error": "请求格式不正确"})
		return false
	}
	return true
}

func auditActor(u model.User) audit.Actor {
	return audit.Actor{ID: u.ID, Type: "user", Email: u.Username}
}

func (s *Service) SetLimiter(l ratelimit.Limiter) { s.limiter = l }

func (s *Service) Guard(r *http.Request, action, token string) error {
	session, _ := auth.SessionToken(r)
	var u model.User
	var e error
	if s.users != nil {
		u, e = s.users.ValidateSession(session)
	} else {
		e = errors.New("user service unavailable")
	}
	if e == nil && auth.PasskeyConfirmed(r.Context(), u.ID) {
		return nil
	}
	if action == "sensitive" || action == "oauth" {
		return nil
	}
	cfg, err := s.settings(r.Context())
	if err != nil {
		return err
	}
	return s.verifyTurnstile(r.Context(), r, cfg, token, action)
}

func loginFingerprint(u model.User) string {
	return auth.HashAPIKey(u.Username + "\n" + u.Email + "\n" + u.PasswordHash + "\n" + fmt.Sprint(u.AuthRevision))
}
func (s *Service) ResetGuard(r *http.Request, hash, code string) (int64, error) {
	if s.accounts == nil {
		return 0, errors.New("account storage unavailable")
	}
	id, err := s.accounts.ResetUserID(r.Context(), hash)
	if err != nil {
		return 0, err
	}
	u, err := s.store.GetUserByID(id)
	if err != nil {
		return 0, err
	}
	if err = s.checkMFA(r.Context(), id, code); err != nil {
		return 0, err
	}
	return u.AuthRevision, nil
}

func publicRole(role model.Role) bool {
	for _, p := range role.Permissions {
		if p != "api.test" && !publicAccountPermission(p) {
			return false
		}
	}
	return role.Name != "super_admin"
}
func (s *Service) reauthenticate(r *http.Request, u model.User, req payload) error {
	return s.reauthenticateAction(r, u, req, "sensitive")
}
func (s *Service) reauthenticateAction(r *http.Request, u model.User, req payload, action string) error {
	if auth.PasskeyConfirmed(r.Context(), u.ID) {
		return nil
	}
	var err error
	if err = s.verifyConfirmation(r, u, req.CurrentPassword); err != nil {
		return err
	}
	if strings.HasPrefix(r.URL.Path, "/account/v1/admin/") {
		return nil
	}
	return s.checkMFA(r.Context(), u.ID, req.TOTPCode)
}

func (s *Service) SetAdminPath(path string) { s.adminPath = path }

func (s *Service) siteInfo() model.PublicSiteInfo {
	if provider, ok := s.mail.(interface{ Public() model.PublicSiteInfo }); ok {
		return provider.Public()
	}
	return model.PublicSiteInfo{TimeZone: model.DefaultTimeZone}
}

func publicAccountPermission(code string) bool {
	switch code {
	case "account.profile", "account.security", "account.keys.read", "account.keys.write", "account.keys.reveal", "account.logs", "account.sessions", "account.billing.read", "account.billing.purchase", "account.billing.redeem":
		return true
	}
	return false
}
