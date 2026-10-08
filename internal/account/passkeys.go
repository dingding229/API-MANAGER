package account

import (
	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

type passkeyUser struct {
	user        model.User
	credentials []webauthn.Credential
}

func (u passkeyUser) WebAuthnID() []byte                         { return []byte(u.user.ID) }
func (u passkeyUser) WebAuthnName() string                       { return u.user.Username }
func (u passkeyUser) WebAuthnDisplayName() string                { return u.user.Username }
func (u passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

type passkeyState struct {
	Session                                                              webauthn.SessionData `json:"session"`
	UserID, SessionHash, Fingerprint, Origin, Name, Method, Path, Digest string
}
type passkeyRequest struct {
	CurrentPassword string          `json:"current_password"`
	TOTPCode        string          `json:"totp_code"`
	Name            string          `json:"name"`
	ChallengeID     string          `json:"challenge_id"`
	Credential      json.RawMessage `json:"credential"`
	Method          string          `json:"method"`
	Path            string          `json:"path"`
	Digest          string          `json:"digest"`
}

func (s *Service) passkeyClient(cfg model.SecuritySettings) (*webauthn.WebAuthn, error) {
	u, e := url.Parse(cfg.WebsiteURL)
	if e != nil || u.Hostname() == "" || u.User != nil || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return nil, errors.New("invalid passkey origin")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && !s.production && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")) {
		return nil, errors.New("passkeys require HTTPS")
	}
	origin := (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
	name := s.siteInfo().Name
	if name == "" {
		name = "API Manager"
	}
	return webauthn.New(&webauthn.Config{RPID: u.Hostname(), RPDisplayName: name, RPOrigins: []string{origin}, AuthenticatorSelection: protocol.AuthenticatorSelection{UserVerification: protocol.VerificationRequired, ResidentKey: protocol.ResidentKeyRequirementRequired}, AttestationPreference: protocol.PreferNoAttestation})
}
func (s *Service) passkeyUser(ctx context.Context, u model.User, rpid string) (passkeyUser, error) {
	st, ok := s.store.(store.PasskeyStore)
	if !ok {
		return passkeyUser{}, errors.New("passkey storage unavailable")
	}
	keys, e := st.Passkeys(ctx, u.ID, rpid)
	if e != nil {
		return passkeyUser{}, e
	}
	out := passkeyUser{user: u}
	for _, key := range keys {
		var c webauthn.Credential
		if e = json.Unmarshal(key.Credential, &c); e != nil {
			return out, e
		}
		out.credentials = append(out.credentials, c)
	}
	return out, nil
}
func (s *Service) savePasskeyState(r *http.Request, purpose, nonce string, state passkeyState) (string, error) {
	raw, e := json.Marshal(state)
	if e != nil {
		return "", e
	}
	encrypted, e := auth.EncryptSecret(s.key+":passkey", string(raw))
	if e != nil {
		return "", e
	}
	binding := auth.HashAPIKey(nonce + "\n" + r.UserAgent() + "\n" + state.SessionHash)
	return s.putChallenge(r.Context(), "passkey-"+purpose, nonce, state.UserID, binding, encrypted)
}
func (s *Service) consumePasskeyState(r *http.Request, purpose, nonce, challenge, sessionHash string) (passkeyState, error) {
	v, e := s.consume(r.Context(), challenge, "passkey-"+purpose, auth.HashAPIKey(nonce+"\n"+r.UserAgent()+"\n"+sessionHash))
	if e != nil || v.Subject != nonce {
		return passkeyState{}, store.ErrConflict
	}
	plain, e := auth.DecryptSecret(s.key+":passkey", v.Payload)
	if e != nil {
		return passkeyState{}, e
	}
	var saved passkeyState
	e = json.Unmarshal([]byte(plain), &saved)
	return saved, e
}
func (s *Service) passkeys(w http.ResponseWriter, r *http.Request, cfg model.SecuritySettings) {
	wa, e := s.passkeyClient(cfg)
	if e != nil {
		write(w, 503, map[string]string{"error": "请先配置正确的 HTTPS 网站地址以使用通行密钥"})
		return
	}
	st, ok := s.store.(store.PasskeyStore)
	if !ok {
		write(w, 503, nil)
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/account/v1/passkeys")
	token, _ := auth.SessionToken(r)
	u, userErr := s.users.ValidateSession(token)
	login := strings.HasPrefix(action, "/login/")
	if !login && (userErr != nil || !s.users.Can(u.ID, "account.security")) {
		write(w, 403, map[string]string{"error": "请先登录并确认账号安全权限"})
		return
	}
	if action == "" && r.Method == http.MethodGet {
		list, e := st.Passkeys(r.Context(), u.ID, wa.Config.RPID)
		if e != nil {
			write(w, 503, nil)
			return
		}
		write(w, 200, map[string]any{"items": list, "rp_id": wa.Config.RPID})
		return
	}
	if !auth.CookieMutationAllowed(r) || r.Header.Get("Origin") != wa.Config.RPOrigins[0] {
		write(w, 403, map[string]string{"error": "请从网站登录页面使用通行密钥"})
		return
	}
	if strings.HasPrefix(action, "/") && r.Method == http.MethodDelete {
		var req payload
		if !read(w, r, &req) {
			return
		}
		if s.reauthenticate(r, u, req) != nil {
			write(w, 403, map[string]string{"error": "请确认当前账号身份"})
			return
		}
		id := strings.TrimPrefix(action, "/")
		if len(id) > 2048 || strings.Contains(id, "/") {
			write(w, 400, nil)
			return
		}
		if e = audit.New(s.store, nil).RecordChecked(r.Context(), auditActor(u), r, "account.passkey.delete.requested", "passkey", id, 202, nil); e != nil {
			write(w, 503, nil)
			return
		}
		if e = st.DeletePasskey(r.Context(), u.ID, id); e != nil {
			write(w, 404, nil)
			return
		}
		s.users.RecordAudit(auditActor(u), r, "account.passkey.delete", "passkey", id, 200, nil)
		write(w, 200, map[string]any{"deleted": true, "login_required": true})
		return
	}
	parts := strings.Split(strings.TrimPrefix(action, "/"), "/")
	if r.Method != http.MethodPost || len(parts) != 2 || (parts[0] != "register" && parts[0] != "login" && parts[0] != "confirm") || (parts[1] != "begin" && parts[1] != "finish") {
		write(w, 404, nil)
		return
	}
	purpose := parts[0]
	var req passkeyRequest
	if !readPasskeyRequest(w, r, &req) {
		return
	}
	state := passkeyState{Origin: wa.Config.RPOrigins[0]}
	if !login {
		state.UserID = u.ID
		state.Fingerprint = loginFingerprint(u)
		state.SessionHash = auth.HashAPIKey(token)
	}
	cookieName := "api_manager_passkey_" + purpose
	nonce := ""
	if parts[1] == "begin" {
		nonce = randomHex(32)
		if nonce == "" {
			write(w, 503, nil)
			return
		}
		var options any
		if purpose == "register" {
			if s.reauthenticate(r, u, payload{CurrentPassword: req.CurrentPassword, TOTPCode: req.TOTPCode}) != nil {
				write(w, 403, map[string]string{"error": "请先验证密码或已有通行密钥"})
				return
			}
			req.Name = strings.TrimSpace(req.Name)
			if req.Name == "" || !utf8.ValidString(req.Name) || utf8.RuneCountInString(req.Name) > 60 {
				write(w, 400, map[string]string{"error": "请填写 1–60 个字符的通行密钥名称"})
				return
			}
			owner, e := s.passkeyUser(r.Context(), u, wa.Config.RPID)
			if e != nil || len(owner.credentials) >= 20 {
				write(w, 409, map[string]string{"error": "通行密钥数量已达上限或暂不可读取"})
				return
			}
			exclusions := []protocol.CredentialDescriptor{}
			for _, c := range owner.credentials {
				exclusions = append(exclusions, c.Descriptor())
			}
			creation, session, e := wa.BeginRegistration(owner, webauthn.WithExclusions(exclusions))
			if e != nil {
				write(w, 503, nil)
				return
			}
			options = creation
			state.Session = *session
			state.Name = req.Name
		} else if purpose == "login" {
			assertion, session, e := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
			if e != nil {
				write(w, 503, nil)
				return
			}
			options = assertion
			state.Session = *session
		} else {
			if !validPasskeyTarget(req.Method, req.Path, req.Digest) {
				write(w, 400, map[string]string{"error": "此操作不能使用通行密钥确认"})
				return
			}
			owner, e := s.passkeyUser(r.Context(), u, wa.Config.RPID)
			if e != nil || len(owner.credentials) == 0 {
				write(w, 409, map[string]string{"error": "请先在账号安全中添加通行密钥"})
				return
			}
			assertion, session, e := wa.BeginLogin(owner, webauthn.WithUserVerification(protocol.VerificationRequired))
			if e != nil {
				write(w, 503, nil)
				return
			}
			options = assertion
			state.Session = *session
			state.Method = req.Method
			state.Path = req.Path
			state.Digest = req.Digest
		}
		state.Session.Expires = time.Now().Add(3 * time.Minute)
		challenge, e := s.savePasskeyState(r, purpose, nonce, state)
		if e != nil {
			write(w, 503, nil)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: nonce, Path: "/account/v1/passkeys/" + purpose, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: 180})
		write(w, 200, map[string]any{"challenge_id": challenge, "options": options})
		return
	}
	cookies := r.CookiesNamed(cookieName)
	if len(cookies) != 1 {
		write(w, 403, map[string]string{"error": "验证已失效，请重新使用通行密钥"})
		return
	}
	state, e = s.consumePasskeyState(r, purpose, cookies[0].Value, req.ChallengeID, state.SessionHash)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/account/v1/passkeys/" + purpose, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	if e != nil || state.Origin != wa.Config.RPOrigins[0] || (!login && (state.UserID != u.ID || state.Fingerprint != loginFingerprint(u) || state.SessionHash != auth.HashAPIKey(token))) {
		write(w, 403, map[string]string{"error": "验证已失效，请重新确认"})
		return
	}
	ceremony := r.Clone(r.Context())
	ceremony.Body = http.MaxBytesReader(w, ioBody(req.Credential), 64<<10)
	if purpose == "register" {
		owner, e := s.passkeyUser(r.Context(), u, wa.Config.RPID)
		if e != nil {
			write(w, 503, nil)
			return
		}
		credential, e := wa.FinishRegistration(owner, state.Session, ceremony)
		if e != nil || credential == nil || !credential.Flags.UserVerified {
			write(w, 403, map[string]string{"error": "通行密钥验证失败，请重新添加"})
			return
		}
		raw, e := json.Marshal(credential)
		if e != nil {
			write(w, 503, nil)
			return
		}
		if e = audit.New(s.store, nil).RecordChecked(r.Context(), auditActor(u), r, "account.passkey.create.requested", "passkey", "", 202, nil); e != nil {
			write(w, 503, nil)
			return
		}
		e = st.AddPasskey(r.Context(), model.Passkey{ID: base64.RawURLEncoding.EncodeToString(credential.ID), UserID: u.ID, RPID: wa.Config.RPID, Name: state.Name, Credential: raw}, u.AuthRevision)
		if e != nil {
			write(w, 409, map[string]string{"error": "此通行密钥已绑定或账号状态已变化"})
			return
		}
		s.users.RecordAudit(auditActor(u), r, "account.passkey.create", "passkey", "", 201, nil)
		write(w, 201, map[string]bool{"created": true})
		return
	}
	var credential *webauthn.Credential
	if login {
		_, credential, e = wa.FinishPasskeyLogin(func(rawID, handle []byte) (webauthn.User, error) {
			record, e := st.Passkey(r.Context(), base64.RawURLEncoding.EncodeToString(rawID), wa.Config.RPID)
			if e != nil || !bytes.Equal(handle, []byte(record.UserID)) {
				return nil, store.ErrNotFound
			}
			candidate, e := s.store.GetUserByID(record.UserID)
			if e != nil || candidate.Status != "active" {
				return nil, store.ErrNotFound
			}
			u = candidate
			return s.passkeyUser(r.Context(), u, wa.Config.RPID)
		}, state.Session, ceremony)
	} else {
		owner, err := s.passkeyUser(r.Context(), u, wa.Config.RPID)
		if err != nil {
			write(w, 503, nil)
			return
		}
		credential, e = wa.FinishLogin(owner, state.Session, ceremony)
	}
	if e != nil || credential == nil || !credential.Flags.UserVerified || credential.Authenticator.CloneWarning {
		write(w, 403, map[string]string{"error": "通行密钥验证未通过"})
		return
	}
	record, e := st.Passkey(r.Context(), base64.RawURLEncoding.EncodeToString(credential.ID), wa.Config.RPID)
	if e != nil || record.UserID != u.ID {
		write(w, 403, nil)
		return
	}
	// Reload and CAS against the stored revision: an overlapping assertion or
	// deleted key must not overwrite a newer counter or re-create the credential.
	original := record.Credential
	var prior webauthn.Credential
	if json.Unmarshal(original, &prior) != nil {
		write(w, 503, nil)
		return
	}
	if prior.Authenticator.SignCount != 0 && credential.Authenticator.SignCount <= prior.Authenticator.SignCount {
		write(w, 403, map[string]string{"error": "通行密钥已变化，请重新验证"})
		return
	}
	record.Credential, e = json.Marshal(credential)
	if e != nil || st.UsePasskey(r.Context(), record, u.AuthRevision) != nil {
		write(w, 403, map[string]string{"error": "验证已失效，请重新确认"})
		return
	}
	if login {
		s.users.RecordAudit(auditActor(u), r, "auth.passkey.login", "user", u.ID, 200, nil)
		s.completeLogin(w, r, u)
		return
	}
	proof, e := s.savePasskeyConfirmation(r, u, state, record.ID)
	if e != nil {
		write(w, 503, nil)
		return
	}
	write(w, 200, map[string]string{"confirmation": proof})
}

func readPasskeyRequest(w http.ResponseWriter, r *http.Request, q *passkeyRequest) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 96<<10))
	d.DisallowUnknownFields()
	if d.Decode(q) != nil || d.Decode(&struct{}{}) != io.EOF {
		write(w, 400, map[string]string{"error": "通行密钥请求格式不正确"})
		return false
	}
	return true
}
