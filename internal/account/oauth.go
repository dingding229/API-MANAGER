package account

import (
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type oauthState struct{ Provider, Verifier, Redirect, UserID, SessionHash, Fingerprint string }

func (s *Service) oauth(w http.ResponseWriter, r *http.Request, cfg model.SecuritySettings) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/account/v1/oauth/"), "/")
	if len(parts) != 2 {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	provider := parts[0]
	enabled := provider == "github" && cfg.GitHubEnabled || provider == "google" && cfg.GoogleEnabled
	if !enabled {
		write(w, 403, map[string]string{"error": "此登录方式未开放"})
		return
	}
	clientID := cfg.GitHubClientID
	secret := cfg.GitHubSecret
	authorize := "https://github.com/login/oauth/authorize"
	endpoint := "https://github.com/login/oauth/access_token"
	scope := "read:user user:email"
	if provider == "google" {
		clientID = cfg.GoogleClientID
		secret = cfg.GoogleSecret
		authorize = "https://accounts.google.com/o/oauth2/v2/auth"
		endpoint = "https://oauth2.googleapis.com/token"
		scope = "openid email profile"
	}
	redirect := strings.TrimRight(cfg.WebsiteURL, "/") + "/account/v1/oauth/" + provider + "/callback"
	cookieName := "api_manager_oauth_" + provider
	if parts[1] == "start" {
		if r.Method != "POST" || !auth.CookieMutationAllowed(r) {
			write(w, 403, map[string]string{"error": "请从本站开始登录"})
			return
		}
		var request payload
		if !read(w, r, &request) {
			return
		}
		if e := s.verifyTurnstile(r.Context(), r, cfg, request.TurnstileToken, "oauth"); e != nil {
			write(w, 403, map[string]string{"error": e.Error()})
			return
		}
		verifier := randomHex(32)
		nonce := randomHex(32)
		if verifier == "" || nonce == "" {
			write(w, 503, map[string]string{"error": "授权随机数不可用"})
			return
		}
		saved := oauthState{Provider: provider, Verifier: verifier, Redirect: redirect}
		if request.Purpose == "link" {
			token, _ := auth.SessionToken(r)
			u, e := s.users.ValidateSession(token)
			if e != nil {
				write(w, 401, map[string]string{"error": "请先登录"})
				return
			}
			if _, e = s.users.VerifyPassword(u.Username, request.CurrentPassword); e != nil || s.checkMFA(r.Context(), u.ID, request.TOTPCode) != nil {
				write(w, 403, map[string]string{"error": "请重新验证账号"})
				return
			}
			saved.UserID = u.ID
			saved.SessionHash = auth.HashAPIKey(token)
			saved.Fingerprint = loginFingerprint(u)
		}
		raw, _ := json.Marshal(saved)
		encrypted, e := auth.EncryptSecret(s.key+":oauth", string(raw))
		if e != nil {
			write(w, 503, map[string]string{"error": "授权暂不可用"})
			return
		}
		state, e := s.putChallenge(r.Context(), "oauth:"+provider, nonce, "", auth.HashAPIKey(nonce+"\n"+r.UserAgent()), encrypted)
		if e != nil {
			write(w, 503, map[string]string{"error": "授权暂不可用"})
			return
		}
		hash := sha256.Sum256([]byte(verifier))
		query := url.Values{"client_id": {clientID}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {scope}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "code_challenge_method": {"S256"}}
		// SameSite=Lax is required for the cross-site provider callback, not the main session.
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: nonce, Path: "/account/v1/oauth/" + provider, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 300})
		write(w, 200, map[string]string{"url": authorize + "?" + query.Encode()})
		return
	}
	if parts[1] != "callback" || r.Method != "GET" {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	cookies := r.CookiesNamed(cookieName)
	if len(cookies) != 1 {
		write(w, 403, map[string]string{"error": "授权状态丢失，请从本站重新登录"})
		return
	}
	state, err := s.consume(r.Context(), r.URL.Query().Get("state"), "oauth:"+provider, auth.HashAPIKey(cookies[0].Value+"\n"+r.UserAgent()))
	if err != nil || state.Subject != cookies[0].Value {
		write(w, 403, map[string]string{"error": "授权状态无效"})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/account/v1/oauth/" + provider, MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	plain, err := auth.DecryptSecret(s.key+":oauth", state.Payload)
	if err != nil {
		write(w, 403, map[string]string{"error": "授权状态无效"})
		return
	}
	var saved oauthState
	if json.Unmarshal([]byte(plain), &saved) != nil || saved.Provider != provider || saved.Redirect != redirect {
		write(w, 403, map[string]string{"error": "授权来源不匹配"})
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" || len(code) > 2048 {
		write(w, 400, map[string]string{"error": "授权未完成"})
		return
	}
	values := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {clientID}, "client_secret": {secret}, "code_verifier": {saved.Verifier}}
	req, _ := http.NewRequestWithContext(r.Context(), "POST", endpoint, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		write(w, 503, map[string]string{"error": "授权服务暂不可用"})
		return
	}
	var exchange struct {
		AccessToken string `json:"access_token"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 32<<10)).Decode(&exchange)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != 200 || exchange.AccessToken == "" {
		write(w, 403, map[string]string{"error": "授权兑换失败"})
		return
	}
	subject, email, nickname, err := s.providerIdentity(r, provider, exchange.AccessToken)
	if err != nil {
		write(w, 403, map[string]string{"error": "第三方未提供可验证的账号资料"})
		return
	}
	if saved.UserID != "" {
		st, ok := s.store.(identityStore)
		if !ok {
			write(w, 503, map[string]string{"error": "授权绑定不可用"})
			return
		}
		active, e := st.ActiveSession(r.Context(), saved.UserID, saved.SessionHash)
		u, userErr := s.store.GetUserByID(saved.UserID)
		if e != nil || userErr != nil || !active || u.Status != "active" || loginFingerprint(u) != saved.Fingerprint {
			write(w, 403, map[string]string{"error": "绑定验证已失效"})
			return
		}
		if e = s.accounts.LinkIdentity(r.Context(), model.Identity{Provider: provider, Subject: subject, UserID: u.ID}); e != nil {
			write(w, 409, map[string]string{"error": "此授权账号已绑定，不会合并账号"})
			return
		}
		s.users.RecordAudit(auditActor(u), r, "account.identity.link", "identity", provider, 200, nil)
		http.Redirect(w, r, strings.TrimRight(cfg.WebsiteURL, "/")+"/account?linked=1", 303)
		return
	}
	id, err := s.accounts.FindIdentity(r.Context(), provider, subject)
	var u model.User
	if err == nil {
		u, err = s.store.GetUserByID(id)
	} else if errors.Is(err, store.ErrNotFound) {
		if !cfg.RegistrationEnabled {
			write(w, 403, map[string]string{"error": "新用户注册尚未开放"})
			return
		}
		if _, exists := s.store.GetUserByEmail(email); exists == nil {
			write(w, 409, map[string]string{"error": "邮箱已有账号，请先使用原方式登录。不会自动合并账号。"})
			return
		}
		role := cfg.DefaultRole
		if role == "" {
			role = "member"
		}
		targetRole, roleErr := s.store.GetRoleByName(role)
		if roleErr != nil || !publicRole(targetRole) {
			write(w, 503, map[string]string{"error": "注册等级配置不安全"})
			return
		}
		uname := randomHex(8)
		password := randomHex(32)
		if uname == "" || password == "" {
			write(w, 503, map[string]string{"error": "注册暂不可用"})
			return
		}
		if len(nickname) > 256 {
			nickname = ""
		}
		u, err = s.users.CreateVerifiedUser(provider+"-"+uname, email, nickname, password, []string{role}, &model.Identity{Provider: provider, Subject: subject})

	}
	if err != nil || u.Status != "active" {
		write(w, 403, map[string]string{"error": "账号无法登录"})
		return
	}
	// Return no access tokens. A two-factor account gets a short challenge page,
	// and never a full session until the second factor succeeds.
	account, accountErr := s.accounts.Account(r.Context(), u.ID)
	if accountErr != nil {
		write(w, 503, map[string]string{"error": "安全配置无法读取，登录未完成"})
		return
	}
	if account.TOTPSecret != "" {
		challenge, e := s.putChallenge(r.Context(), "mfa-login", u.ID, u.ID, s.binding(r), loginFingerprint(u))
		if e != nil {
			write(w, 503, map[string]string{"error": "二次验证不可用"})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "api_manager_mfa", Value: challenge, Path: "/account/v1", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 300})
		http.Redirect(w, r, strings.TrimRight(cfg.WebsiteURL, "/")+"/account?mfa=1", 303)
		return
	}
	_, session, err := s.users.StartSession(u, r)
	if err != nil {
		write(w, 503, map[string]string{"error": "登录不可用"})
		return
	}
	user.SetSessionCookie(w, r, session, s.production)
	http.Redirect(w, r, strings.TrimRight(cfg.WebsiteURL, "/")+"/account", 303)
}
func (s *Service) providerIdentity(r *http.Request, provider, token string) (string, string, string, error) {
	url := "https://api.github.com/user"
	if provider == "google" {
		url = "https://openidconnect.googleapis.com/v1/userinfo"
	}
	req, _ := http.NewRequestWithContext(r.Context(), "GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "API-Manager-Account")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	var data struct {
		ID               int64
		Sub, Name, Email string
		EmailVerified    bool `json:"email_verified"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 32<<10)).Decode(&data) != nil {
		return "", "", "", errors.New("profile unavailable")
	}
	if provider == "google" {
		if data.Sub == "" || !data.EmailVerified || !validEmail(data.Email) {
			return "", "", "", errors.New("unverified email")
		}
		return data.Sub, strings.ToLower(data.Email), data.Name, nil
	}
	req, _ = http.NewRequestWithContext(r.Context(), "GET", "https://api.github.com/user/emails", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "API-Manager-Account")
	emails, e := s.client.Do(req)
	if e != nil {
		return "", "", "", e
	}
	defer emails.Body.Close()
	var list []struct {
		Email             string
		Primary, Verified bool
	}
	if emails.StatusCode != 200 || json.NewDecoder(io.LimitReader(emails.Body, 32<<10)).Decode(&list) != nil {
		return "", "", "", errors.New("email unavailable")
	}
	for _, v := range list {
		if v.Primary && v.Verified && validEmail(v.Email) && data.ID > 0 {
			return strconv.FormatInt(data.ID, 10), strings.ToLower(v.Email), data.Name, nil
		}
	}
	return "", "", "", errors.New("unverified email")
}
