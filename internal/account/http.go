package account

import (
	"api-manager/internal/auth"
	"api-manager/internal/httpx"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

type payload struct {
	UserID          string     `json:"user_id,omitempty"`
	Username        string     `json:"username"`
	Email           string     `json:"email"`
	Password        string     `json:"password"`
	Nickname        string     `json:"nickname"`
	Code            string     `json:"code"`
	Purpose         string     `json:"purpose"`
	VerificationID  string     `json:"verification_id"`
	ChallengeID     string     `json:"challenge_id"`
	TOTPCode        string     `json:"totp_code"`
	CurrentPassword string     `json:"current_password"`
	TurnstileToken  string     `json:"turnstile_token"`
	OperationID     string     `json:"operation_id"`
	PlanID          string     `json:"plan_id"`
	KeyName         string     `json:"key_name"`
	Proof           string     `json:"proof"`
	Confirm         bool       `json:"confirm"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	limit := 30
	bucket := "account-write-ip:"
	if r.Method == "GET" || r.Method == "HEAD" {
		limit = 180
		bucket = "account-read-ip:"
	}
	if s.limiter != nil && !s.limiter.Allow(bucket+httpx.Client(r).IP, limit, time.Minute, time.Now()) {
		write(w, 429, map[string]string{"error": "操作过于频繁，请稍后再试"})
		return
	}
	cfg, err := s.settings(r.Context())
	if err != nil {
		write(w, 503, map[string]string{"error": "账号服务不可用"})
		return
	}
	path := r.URL.Path
	if method, known := map[string]string{"/auth/v1/login": "POST", "/test/v1/login": "POST", "/account/v1/mfa-login": "POST", "/account/v1/send-code": "POST", "/account/v1/register": "POST", "/account/v1/email-login": "POST"}[path]; known && r.Method != method {
		w.Header().Set("Allow", method)
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	if path == "/account/v1/options" && r.Method == "GET" {
		write(w, 200, map[string]any{"time_zone": s.siteInfo().TimeZone, "website_url": s.siteInfo().WebsiteURL, "admin_path": s.adminPath, "registration": cfg.RegistrationEnabled && s.mail.MailAvailable(), "email_login": cfg.EmailLoginEnabled && s.mail.MailAvailable(), "github": cfg.GitHubEnabled, "google": cfg.GoogleEnabled, "turnstile": cfg.TurnstileEnabled, "turnstile_site_key": cfg.TurnstileSiteKey})
		return
	}
	if strings.HasPrefix(path, "/account/v1/oauth/") {
		s.oauth(w, r, cfg)
		return
	}
	if auth.UnsafeMethod(r.Method) && r.Header.Get("Origin") != "" && !auth.CookieMutationAllowed(r) {
		write(w, 403, map[string]string{"error": "请从本站提交操作"})
		return
	}
	if path == "/auth/v1/login" || path == "/test/v1/login" {
		var req model.LoginRequest
		if !read(w, r, &req) {
			return
		}
		if req.ChallengeID == "" {
			if err = s.verifyTurnstile(r.Context(), r, cfg, req.TurnstileToken, "login"); err != nil {
				write(w, 403, map[string]string{"error": err.Error()})
				return
			}
		}
		if req.ChallengeID != "" {
			s.mfaLogin(w, r, req.ChallengeID, req.TOTPCode)
			return
		}
		name := req.Username
		if name == "" {
			name = req.Email
		}
		if s.limiter != nil && !s.limiter.Allow("account-password:"+auth.HashAPIKey(strings.ToLower(strings.TrimSpace(name))), 10, time.Minute, time.Now()) {
			write(w, 429, map[string]string{"error": "登录尝试过于频繁，请稍后再试"})
			return
		}
		u, err := s.users.VerifyPassword(name, req.Password)
		if err != nil {
			s.users.RecordAudit(auditActor(model.User{Username: name}), r, "auth.login.denied", "user", "", 401, nil)
			write(w, 401, map[string]string{"error": "用户名或密码不正确"})
			return
		}
		s.loginResponse(w, r, u)
		return
	}
	if path == "/account/v1/mfa-login" {
		var req payload
		if !read(w, r, &req) {
			return
		}
		if req.ChallengeID == "" {
			if cookie, err := r.Cookie("api_manager_mfa"); err == nil {
				req.ChallengeID = cookie.Value
			}
		}
		s.mfaLogin(w, r, req.ChallengeID, req.TOTPCode)
		return
	}
	if path == "/account/v1/send-code" {
		s.sendCode(w, r, cfg)
		return
	}
	if path == "/account/v1/register" {
		s.register(w, r, cfg)
		return
	}
	if path == "/account/v1/email-login" {
		s.emailLogin(w, r, cfg)
		return
	}
	token, _ := auth.SessionToken(r)
	u, err := s.users.ValidateSession(token)
	if err != nil {
		write(w, 401, map[string]string{"error": "请先登录"})
		return
	}
	s.users.TouchSession(token, r)
	if permission := accountPermission(r); permission != "" && !s.users.Can(u.ID, permission) {
		write(w, 403, map[string]string{"error": "没有执行此操作的权限"})
		return
	}
	if auth.UnsafeMethod(r.Method) {
		if _, cookie := auth.SessionToken(r); cookie && !auth.CookieMutationAllowed(r) {
			write(w, 403, map[string]string{"error": "请求来源不正确"})
			return
		}
	}
	if path == "/account/v1/session-users" && r.Method == "GET" {
		if !s.users.Can(u.ID, "user.read") {
			write(w, 403, map[string]string{"error": "没有查看用户会话的权限"})
			return
		}
		list := []map[string]string{}
		for _, v := range s.store.ListUsers() {
			list = append(list, map[string]string{"id": v.ID, "username": v.Username, "nickname": v.Nickname})
		}
		write(w, 200, list)
		return
	}
	if strings.HasPrefix(path, "/account/v1/sessions") {
		s.sessions(w, r, u)
		return
	}
	if strings.HasPrefix(path, "/account/v1/identities") {
		s.identities(w, r, u)
		return
	}
	if path == "/account/v1/me" && r.Method == "GET" {
		a, e := s.accounts.Account(r.Context(), u.ID)
		if e != nil {
			write(w, 503, map[string]string{"error": "账号服务不可用"})
			return
		}
		roles := s.store.ListRoles()
		write(w, 200, map[string]any{"user": u, "uid": u.ID, "nickname": a.Nickname, "email_verified": a.EmailVerified, "totp_enabled": a.TOTPSecret != "", "levels": roles, "permissions": s.users.Profile(u)["permissions"]})
		return
	}
	if path == "/account/v1/basic" && r.Method == "PUT" {
		var req payload
		if !read(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.Nickname) == "" || utf8.RuneCountInString(req.Nickname) > 64 {
			write(w, 400, map[string]string{"error": "昵称须为 1–64 个字符"})
			return
		}
		if err = s.accounts.UpdateBasics(r.Context(), u.ID, strings.TrimSpace(req.Nickname)); err != nil {
			write(w, 503, map[string]string{"error": "资料暂不可保存"})
			return
		}
		write(w, 200, map[string]bool{"updated": true})
		return
	}
	if path == "/account/v1/verify-email" && r.Method == "POST" {
		var req payload
		if !read(w, r, &req) {
			return
		}
		v, e := s.verifyCode(r.Context(), req.VerificationID, "verify-email", req.Code, s.binding(r))
		if e != nil || v.Subject != u.Email {
			write(w, 403, map[string]string{"error": "邮箱验证码无效"})
			return
		}
		if e = s.accounts.VerifyEmail(r.Context(), u.ID, u.Email); e != nil {
			write(w, 503, map[string]string{"error": "邮箱验证暂不可用"})
			return
		}
		write(w, 200, map[string]bool{"verified": true})
		return
	}
	if path == "/account/v1/totp" {
		if r.Method != "GET" && r.Method != "POST" {
			w.Header().Set("Allow", "GET, POST")
			write(w, 405, map[string]string{"error": "method not allowed"})
			return
		}
		s.totp(w, r, u, cfg)
		return
	}

	if path == "/account/v1/redeem" {
		s.cards(w, r, u)
		return
	}
	if path == "/account/v1/subscriptions" && r.Method == "GET" {
		if st, ok := s.store.(interface {
			Subscriptions(context.Context, string) ([]model.Subscription, error)
		}); ok {
			v, e := st.Subscriptions(r.Context(), u.ID)
			if e != nil {
				write(w, 503, nil)
			} else {
				write(w, 200, v)
			}
			return
		}
	}
	if strings.HasPrefix(path, "/account/v1/wallet") || path == "/account/v1/plans" || path == "/account/v1/subscription" || path == "/account/v1/logs" || strings.HasPrefix(path, "/account/v1/keys") {
		s.portal(w, r, u)
		return
	}
	if strings.HasPrefix(path, "/account/v1/admin/") {
		if permission := adminAccountPermission(r); !s.users.Can(u.ID, permission) {
			write(w, 403, map[string]string{"error": "没有访问此管理功能的权限"})
			return
		}
		s.admin(w, r, u, cfg)
		return
	}
	write(w, 404, map[string]string{"error": "not found"})
}
func validEmail(raw string) bool {
	address, err := mail.ParseAddress(raw)
	return err == nil && address.Address == raw && len(raw) <= 254 && strings.Contains(raw, "@") && !strings.ContainsAny(raw, "\r\n\x00")
}
func (s *Service) sendCode(w http.ResponseWriter, r *http.Request, cfg model.SecuritySettings) {
	var req payload
	if !read(w, r, &req) {
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !validEmail(req.Email) || !s.mail.MailAvailable() {
		write(w, 400, map[string]string{"error": "邮箱或邮件服务不可用"})
		return
	}
	if err := s.verifyTurnstile(r.Context(), r, cfg, req.TurnstileToken, "email_code"); err != nil {
		write(w, 403, map[string]string{"error": err.Error()})
		return
	}
	if req.Purpose != "register" && req.Purpose != "email-login" && req.Purpose != "verify-email" && req.Purpose != "change-email" {
		write(w, 400, map[string]string{"error": "验证码用途不正确"})
		return
	}
	if (req.Purpose == "register" && !cfg.RegistrationEnabled) || (req.Purpose == "email-login" && !cfg.EmailLoginEnabled) {
		write(w, 403, map[string]string{"error": "此登录或注册方式未开放"})
		return
	}
	if req.Purpose == "change-email" {
		token, _ := auth.SessionToken(r)
		if u, err := s.users.ValidateSession(token); err != nil || !s.users.Can(u.ID, "account.security") {
			write(w, 401, map[string]string{"error": "请先登录"})
			return
		}
	}
	if req.Purpose == "verify-email" {
		token, _ := auth.SessionToken(r)
		u, e := s.users.ValidateSession(token)
		if e != nil || u.Email != req.Email || !s.users.Can(u.ID, "account.profile") {
			write(w, 403, map[string]string{"error": "只能验证自己的邮箱"})
			return
		}
	}
	if s.limiter != nil && !s.limiter.Allow("account-email:"+auth.HashAPIKey(req.Email), 3, time.Hour, time.Now()) {
		write(w, 429, map[string]string{"error": "验证码发送过于频繁"})
		return
	}
	random := make([]byte, 4)
	if _, err := rand.Read(random); err != nil {
		write(w, 503, map[string]string{"error": "验证码不可用"})
		return
	}
	code := sixDigit(random)
	id := ids.NewUUID()
	bind := s.binding(r)
	if req.Purpose == "change-email" {
		token, _ := auth.SessionToken(r)
		u, _ := s.users.ValidateSession(token)
		bind = u.ID
	}
	v := model.Verification{ID: id, Purpose: req.Purpose, Subject: req.Email, CodeHash: auth.HashAPIKey(s.key + ":" + id + ":" + code), Binding: bind, ExpiresAt: time.Now().Add(10 * time.Minute)}
	if err := s.accounts.PutVerification(r.Context(), v); err != nil {
		write(w, 503, map[string]string{"error": "验证码暂不可发送"})
		return
	}
	if err := s.mail.SendCode(r.Context(), req.Email, code, req.Purpose); err != nil {
		write(w, 503, map[string]string{"error": "邮件发送失败，请稍后再试"})
		return
	}
	write(w, 202, map[string]string{"verification_id": id, "message": "验证码已发送，10 分钟内有效"})
}
func (s *Service) verifyCode(ctx context.Context, id, purpose, code, binding string) (model.Verification, error) {
	if len(id) > 64 || len(code) != 6 {
		return model.Verification{}, store.ErrConflict
	}
	return s.accounts.ConsumeVerification(ctx, id, purpose, auth.HashAPIKey(s.key+":"+id+":"+code), binding)
}
func (s *Service) register(w http.ResponseWriter, r *http.Request, cfg model.SecuritySettings) {
	if !cfg.RegistrationEnabled {
		write(w, 403, map[string]string{"error": "注册尚未开放"})
		return
	}
	var req payload
	if !read(w, r, &req) {
		return
	}
	if err := s.verifyTurnstile(r.Context(), r, cfg, req.TurnstileToken, "register"); err != nil {
		write(w, 403, map[string]string{"error": err.Error()})
		return
	}
	if utf8.RuneCountInString(req.Nickname) > 64 || !validEmail(strings.ToLower(strings.TrimSpace(req.Email))) {
		write(w, 400, map[string]string{"error": "昵称或邮箱无效"})
		return
	}
	v, err := s.verifyCode(r.Context(), req.VerificationID, "register", req.Code, s.binding(r))
	if err != nil || v.Subject != strings.ToLower(strings.TrimSpace(req.Email)) {
		write(w, 403, map[string]string{"error": "邮箱验证码无效"})
		return
	}
	role := cfg.DefaultRole
	if role == "" {
		role = "member"
	}
	target, e := s.store.GetRoleByName(role)
	if e != nil || !publicRole(target) {
		write(w, 503, map[string]string{"error": "默认注册等级配置不安全"})
		return
	}
	u, err := s.users.CreateVerifiedUser(req.Username, req.Email, strings.TrimSpace(req.Nickname), req.Password, []string{role}, nil)
	if err != nil {
		write(w, 400, map[string]string{"error": "资料不符合要求，或用户名/邮箱已使用"})
		return
	}

	s.users.RecordAudit(auditActor(u), r, "auth.register", "user", u.ID, 201, nil)
	write(w, 201, map[string]any{"user": u, "message": "注册完成，请登录"})
}
func contains(list []string, code string) bool {
	for _, v := range list {
		if v == code {
			return true
		}
	}
	return false
}
func (s *Service) emailLogin(w http.ResponseWriter, r *http.Request, cfg model.SecuritySettings) {
	if !cfg.EmailLoginEnabled {
		write(w, 403, map[string]string{"error": "邮箱登录未开放"})
		return
	}
	var req payload
	if !read(w, r, &req) {
		return
	}
	if err := s.verifyTurnstile(r.Context(), r, cfg, req.TurnstileToken, "email_login"); err != nil {
		write(w, 403, map[string]string{"error": err.Error()})
		return
	}
	v, err := s.verifyCode(r.Context(), req.VerificationID, "email-login", req.Code, s.binding(r))
	if err != nil {
		write(w, 403, map[string]string{"error": "验证码无效"})
		return
	}
	u, err := s.store.GetUserByEmail(v.Subject)
	if err != nil || u.Status != "active" {
		write(w, 401, map[string]string{"error": "此账号不可登录"})
		return
	}
	if err = s.accounts.VerifyEmail(r.Context(), u.ID, u.Email); err != nil {
		write(w, 503, map[string]string{"error": "邮箱状态暂不可确认"})
		return
	}
	s.loginResponse(w, r, u)
}
func (s *Service) mfaLogin(w http.ResponseWriter, r *http.Request, challenge, code string) {
	v, err := s.consume(r.Context(), challenge, "mfa-login", s.binding(r))
	if err != nil {
		write(w, 403, map[string]string{"error": "登录验证已失效，请重新登录"})
		return
	}
	u, err := s.store.GetUserByID(v.UserID)
	if err != nil || u.Status != "active" || loginFingerprint(u) != v.Payload {
		write(w, 403, map[string]string{"error": "账号安全信息已变化，请重新登录"})
		return
	}
	if err = s.checkMFA(r.Context(), u.ID, code); err != nil {
		write(w, 403, map[string]string{"error": "动态码或恢复码无效，请重新登录"})
		return
	}

	http.SetCookie(w, &http.Cookie{Name: "api_manager_mfa", Path: "/account/v1", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	s.completeLogin(w, r, u)
}
func (s *Service) totp(w http.ResponseWriter, r *http.Request, u model.User, cfg model.SecuritySettings) {
	if r.Method == "GET" {
		a, e := s.accounts.Account(r.Context(), u.ID)
		if e != nil {
			write(w, 503, map[string]string{"error": "安全配置不可用"})
			return
		}
		write(w, 200, map[string]any{"enabled": a.TOTPSecret != "", "recovery_remaining": len(a.RecoveryHashes)})
		return
	}
	var req payload
	if !read(w, r, &req) {
		return
	}
	if err := s.verifyTurnstile(r.Context(), r, cfg, req.TurnstileToken, "sensitive"); err != nil {
		write(w, 403, map[string]string{"error": err.Error()})
		return
	}
	if _, err := s.users.VerifyPassword(u.Username, req.CurrentPassword); err != nil {
		write(w, 403, map[string]string{"error": "当前密码不正确"})
		return
	}
	a, err := s.accounts.Account(r.Context(), u.ID)
	if err != nil {
		write(w, 503, map[string]string{"error": "安全配置不可用"})
		return
	}
	if req.Purpose == "begin" {
		if a.TOTPSecret != "" {
			write(w, 409, map[string]string{"error": "双重验证已启用"})
			return
		}
		b := make([]byte, 20)
		if _, err = rand.Read(b); err != nil {
			write(w, 503, map[string]string{"error": "密钥创建失败"})
			return
		}
		secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
		encrypted, e := auth.EncryptSecret(s.key+":totp:"+u.ID, secret)
		if e != nil || s.accounts.BeginTOTP(r.Context(), u.ID, encrypted) != nil {
			write(w, 503, map[string]string{"error": "绑定暂不可用"})
			return
		}
		uri := "otpauth://totp/" + url.PathEscape("API Manager:"+u.Username) + "?secret=" + secret + "&issuer=API%20Manager&algorithm=SHA1&digits=6&period=30"
		write(w, 200, map[string]string{"secret": secret, "uri": uri})
		return
	}
	if req.Purpose == "enable" {
		secret, e := auth.DecryptSecret(s.key+":totp:"+u.ID, a.TOTPPending)
		step, valid := totpStep(secret, req.TOTPCode, time.Now())
		if e != nil || !valid {
			write(w, 403, map[string]string{"error": "绑定动态码不正确"})
			return
		}
		codes := []string{}
		hashes := []string{}
		for range 8 {
			raw := randomHex(16)
			if raw == "" {
				write(w, 503, map[string]string{"error": "恢复码创建失败"})
				return
			}
			code := "rc_" + raw
			codes = append(codes, code)
			hashes = append(hashes, auth.HashAPIKey(code))
		}
		if !time.Now().Before(a.TOTPPendingExpires) || s.accounts.EnableTOTP(r.Context(), u.ID, a.TOTPPending, step, hashes) != nil {
			write(w, 409, map[string]string{"error": "绑定已失效，请重新开始"})
			return
		}
		user.ClearSessionCookie(w, r, s.production)

		s.users.RecordAudit(auditActor(u), r, "auth.totp.enable", "user", u.ID, 200, nil)
		write(w, 200, map[string]any{"enabled": true, "recovery_codes": codes})
		return
	}
	if req.Purpose == "disable" {
		if a.TOTPSecret == "" || s.checkMFA(r.Context(), u.ID, req.TOTPCode) != nil {
			write(w, 403, map[string]string{"error": "请提供有效动态码或恢复码"})
			return
		}
		if err = s.accounts.DisableTOTP(r.Context(), u.ID, a.TOTPSecret); err != nil {
			write(w, 503, map[string]string{"error": "关闭失败"})
			return
		}
		s.users.RecordAudit(auditActor(u), r, "auth.totp.disable", "user", u.ID, 200, nil)
		user.ClearSessionCookie(w, r, s.production)
		write(w, 200, map[string]bool{"enabled": false})
		return
	}
	write(w, 400, map[string]string{"error": "未知安全操作"})
}
func sixDigit(b []byte) string { return fmt.Sprintf("%06d", binary.BigEndian.Uint32(b)%1000000) }
