package account

import (
	"api-manager/internal/auth"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"api-manager/internal/user"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var onboardingUsername = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{2,31}$`)

const onboardingCookie = "api_manager_onboarding"

type onboardingProfile struct{ Provider, Subject, Nickname string }

func (s *Service) beginTelegramOnboarding(w http.ResponseWriter, r *http.Request, cfg model.SecuritySettings, subject, nickname, returnTo string) {
	if !s.mail.MailAvailable() {
		write(w, 503, map[string]string{"error": "首次 Telegram 登录需要验证邮箱，网站邮件服务暂不可用"})
		return
	}
	raw, _ := json.Marshal(onboardingProfile{Provider: "telegram", Subject: subject, Nickname: nickname})
	encrypted, e := auth.EncryptSecret(s.key+":onboarding", string(raw))
	if e != nil {
		write(w, 503, nil)
		return
	}
	pendingID := ids.NewUUID()
	secret := randomHex(32)
	if secret == "" {
		write(w, 503, nil)
		return
	}
	token := pendingID + ":" + secret
	e = s.accounts.PutVerification(r.Context(), model.Verification{ID: pendingID, Purpose: "onboarding:telegram", Subject: subject, Binding: s.binding(r), CodeHash: auth.HashAPIKey(secret), Payload: encrypted, ExpiresAt: time.Now().Add(20 * time.Minute)})
	if e != nil {
		write(w, 503, nil)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: onboardingCookie, Value: token, Path: "/account/v1/onboarding", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 1200})
	destination := strings.TrimRight(cfg.WebsiteURL, "/") + "/complete-registration?return=" + rReturn(returnTo, s.adminPath)
	http.Redirect(w, r, destination, 303)
}
func (s *Service) pendingOnboarding(r *http.Request) (model.Verification, onboardingProfile, string, error) {
	var v model.Verification
	var info onboardingProfile
	cookies := r.CookiesNamed(onboardingCookie)
	if len(cookies) != 1 {
		return v, info, "", user.ErrInvalidCredentials
	}
	token := cookies[0].Value
	parts := strings.Split(token, ":")
	if len(parts) != 2 || len(parts[0]) > 64 || len(parts[1]) != 64 {
		return v, info, "", user.ErrInvalidCredentials
	}
	st, ok := s.store.(interface {
		PeekVerification(context.Context, string, string, string, string) (model.Verification, error)
	})
	if !ok {
		return v, info, "", user.ErrInvalidCredentials
	}
	v, e := st.PeekVerification(r.Context(), parts[0], "onboarding:telegram", auth.HashAPIKey(parts[1]), s.binding(r))
	if e != nil {
		return v, info, "", e
	}
	plain, e := auth.DecryptSecret(s.key+":onboarding", v.Payload)
	if e != nil || json.Unmarshal([]byte(plain), &info) != nil || info.Provider != "telegram" || info.Subject != v.Subject {
		return v, info, "", user.ErrInvalidCredentials
	}
	return v, info, token, nil
}
func (s *Service) onboarding(w http.ResponseWriter, r *http.Request, cfg model.SecuritySettings) {
	if !cfg.TelegramEnabled {
		write(w, 403, map[string]string{"error": "Telegram 登录未开放"})
		return
	}
	v, info, token, e := s.pendingOnboarding(r)
	if e != nil {
		write(w, 401, map[string]string{"error": "授权验证已过期，请重新使用 Telegram 登录"})
		return
	}
	if r.Method == "GET" && r.URL.Path == "/account/v1/onboarding" {
		write(w, 200, map[string]any{"provider": "telegram", "nickname": info.Nickname, "allowed_email_domains": func() []string {
			if cfg.AllowedEmailDomains == nil {
				return []string{}
			}
			return cfg.AllowedEmailDomains
		}(), "expires_at": v.ExpiresAt})
		return
	}
	if r.Method != "POST" || !auth.CookieMutationAllowed(r) {
		write(w, 403, nil)
		return
	}
	var q payload
	if !read(w, r, &q) {
		return
	}
	q.Email = strings.ToLower(strings.TrimSpace(q.Email))
	if !validEmail(q.Email) || !emailDomainAllowed(cfg, q.Email) {
		write(w, 400, map[string]string{"error": "邮箱不符合网站允许的后缀，请更换邮箱"})
		return
	}
	if r.URL.Path == "/account/v1/onboarding/send-code" {
		if time.Until(v.ExpiresAt) < 10*time.Minute {
			write(w, 401, map[string]string{"error": "请重新使用 Telegram 授权后获取验证码"})
			return
		}
		if !s.mail.MailAvailable() {
			write(w, 503, map[string]string{"error": "邮件服务暂不可用"})
			return
		}
		if s.limiter != nil && (!s.limiter.Allow("onboard-subject:"+auth.HashAPIKey(info.Subject), 3, time.Hour, time.Now()) || !s.limiter.Allow("account-email:"+auth.HashAPIKey(q.Email), 3, time.Hour, time.Now())) {
			write(w, 429, map[string]string{"error": "验证码发送过于频繁"})
			return
		}
		b := make([]byte, 4)
		if _, e = rand.Read(b); e != nil {
			write(w, 503, nil)
			return
		}
		code := sixDigit(b)
		id := ids.NewUUID()
		proof := model.Verification{ID: id, Purpose: "onboard-email", Subject: q.Email, CodeHash: auth.HashAPIKey(s.key + ":" + id + ":" + code), Binding: v.ID, ExpiresAt: time.Now().Add(10 * time.Minute)}
		if e = s.accounts.PutVerification(r.Context(), proof); e != nil {
			write(w, 503, nil)
			return
		}
		if e = s.mail.SendCode(r.Context(), q.Email, code, "register"); e != nil {
			write(w, 503, map[string]string{"error": "验证码发送失败，请稍后再试"})
			return
		}
		write(w, 202, map[string]string{"verification_id": id, "message": "请在本次授权有效期内填写验证码"})
		return
	}
	if r.URL.Path != "/account/v1/onboarding/complete" {
		write(w, 404, nil)
		return
	}
	if !ValidOnboardingBasics(q) {
		write(w, 400, map[string]string{"error": "请核对用户名、昵称与密码长度"})
		return
	}
	// A verified mailbox never authorizes silently merging into an existing account.
	if _, e = s.store.GetUserByEmail(q.Email); e == nil {
		write(w, 409, map[string]string{"error": "邮箱已有账号，请登录该账号后绑定 Telegram；不会自动合并账号"})
		return
	}
	if _, e = s.store.GetUserByUsername(q.Username); e == nil {
		write(w, 409, map[string]string{"error": "用户名已使用，请选择其他用户名"})
		return
	}
	role, e := s.store.GetRoleByName(cfg.DefaultRole)
	if e != nil || !publicRole(role) {
		write(w, 503, nil)
		return
	}
	proof, e := s.verifyCode(r.Context(), q.VerificationID, "onboard-email", q.Code, v.ID)
	if e != nil || proof.Subject != q.Email {
		write(w, 403, map[string]string{"error": "邮箱验证码无效或已过期"})
		return
	}
	if _, e = s.consume(r.Context(), token, "onboarding:telegram", s.binding(r)); e != nil {
		write(w, 409, map[string]string{"error": "本次授权已完成或已过期，请重新登录"})
		return
	}
	u, e := s.users.CreateVerifiedUser(q.Username, q.Email, strings.TrimSpace(q.Nickname), q.Password, []string{role.Name}, &model.Identity{Provider: "telegram", Subject: info.Subject})
	if e != nil {
		write(w, 409, map[string]string{"error": "账号资料已被使用，请重新开始登录或使用已有账号"})
		return
	}
	_, session, e := s.users.StartSession(u, r)
	if e != nil {
		write(w, 503, map[string]string{"error": "账号已创建，请使用用户名和密码登录"})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: onboardingCookie, Value: "", Path: "/account/v1/onboarding", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	user.SetSessionCookie(w, r, session, s.production)
	s.users.RecordAudit(auditActor(u), r, "account.register.telegram", "user", u.ID, 201, nil)
	write(w, 201, map[string]bool{"registered": true})
}
func ValidOnboardingBasics(q payload) bool {
	if !onboardingUsername.MatchString(q.Username) || len([]byte(q.Password)) < 8 || len([]byte(q.Password)) > 72 {
		return false
	}
	return utf8.RuneCountInString(strings.TrimSpace(q.Nickname)) >= 1 && utf8.RuneCountInString(q.Nickname) <= 64
}
