package account

import (
	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *Service) adminKeys(w http.ResponseWriter, r *http.Request, u model.User) {
	var req payload
	if !read(w, r, &req) {
		return
	}
	if e := s.reauthenticate(r, u, req); e != nil {
		write(w, 403, map[string]string{"error": "请重新验证管理员密码"})
		return
	}
	tail := strings.TrimPrefix(r.URL.Path, "/account/v1/admin/keys")
	if tail == "" && r.Method == "POST" {
		owner := req.UserID
		if owner == "" {
			owner = u.ID
		}
		target, e := s.store.GetUserByID(owner)
		if e != nil || target.Status != "active" {
			write(w, 400, map[string]string{"error": "请选择有效用户"})
			return
		}
		s.createKey(w, r, u, owner, req)
		return
	}
	parts := strings.Split(strings.Trim(tail, "/"), "/")
	if len(parts) != 2 || r.Method != "POST" {
		write(w, 404, nil)
		return
	}
	key, e := s.store.GetCredential(parts[0])
	if e != nil {
		write(w, 404, nil)
		return
	}
	if e = audit.New(s.store, nil).RecordChecked(r.Context(), auditActor(u), r, "credential."+parts[1]+".requested", "credential", key.ID, 202, nil); e != nil {
		write(w, 503, nil)
		return
	}
	switch parts[1] {
	case "reveal":
		s.revealKey(w, key)
	case "rotate":
		if !req.Confirm {
			write(w, 400, map[string]string{"error": "请确认旧密钥立即失效"})
			return
		}
		s.rotateKey(w, r, u, key)
	case "revoke":
		if !req.Confirm {
			write(w, 400, nil)
			return
		}
		key.Revoked = true
		if e = s.store.UpdateCredential(key); e != nil {
			write(w, 503, nil)
			return
		}
		write(w, 200, map[string]bool{"revoked": true})
	default:
		write(w, 404, nil)
	}
}
func (s *Service) createKey(w http.ResponseWriter, r *http.Request, u model.User, owner string, req payload) {
	ranges, err := auth.NormalizeIPRanges(req.AllowedIPRanges)
	if err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	name := strings.TrimSpace(req.KeyName)
	if name == "" || utf8.RuneCountInString(name) > 64 || (req.ExpiresAt != nil && (!req.ExpiresAt.After(time.Now()) || req.ExpiresAt.After(time.Now().AddDate(5, 0, 0)))) {
		write(w, 400, map[string]string{"error": "凭据名称须为 1–64 个字符，有效期须在未来五年内"})
		return
	}
	if e := audit.New(s.store, nil).RecordChecked(r.Context(), auditActor(u), r, "credential.create.requested", "user", owner, 202, nil); e != nil {
		write(w, 503, nil)
		return
	}
	secret := "ak_" + randomHex(24)
	if len(secret) != 51 {
		write(w, 503, nil)
		return
	}
	encrypted, e := auth.EncryptSecret(s.key, secret)
	if e != nil {
		write(w, 503, nil)
		return
	}
	v := model.Credential{AllowedIPRanges: ranges, ID: ids.NewUUID(), OwnerUserID: owner, Name: name, Prefix: secret[:10], Hash: auth.HashAPIKey(secret), EncryptedKey: encrypted, KeyAvailable: true, CreatedAt: time.Now().UTC(), ExpiresAt: req.ExpiresAt}
	st, ok := s.store.(interface {
		CreateOwnedCredential(context.Context, model.Credential) error
	})
	if !ok {
		write(w, 503, nil)
		return
	}
	if e = st.CreateOwnedCredential(r.Context(), v); e != nil {
		write(w, 409, map[string]string{"error": "创建失败，每个用户最多 20 个有效凭据"})
		return
	}
	write(w, 201, map[string]any{"credential": v, "api_key": secret})
}
func (s *Service) revealKey(w http.ResponseWriter, key model.Credential) {
	if key.Revoked || key.EncryptedKey == "" {
		write(w, 409, map[string]string{"error": "密钥不可查看，请重置密钥"})
		return
	}
	secret, e := auth.DecryptSecret(s.key, key.EncryptedKey)
	if e != nil || subtle.ConstantTimeCompare([]byte(auth.HashAPIKey(secret)), []byte(key.Hash)) != 1 {
		write(w, 409, map[string]string{"error": "密钥不可恢复，请重置密钥"})
		return
	}
	write(w, 200, map[string]string{"api_key": secret})
}
func (s *Service) rotateKey(w http.ResponseWriter, r *http.Request, u model.User, v model.Credential) {
	if v.Revoked {
		write(w, 409, map[string]string{"error": "已吊销的凭据不可重置"})
		return
	}
	secret := "ak_" + randomHex(24)
	if len(secret) != 51 {
		write(w, 503, nil)
		return
	}
	encrypted, e := auth.EncryptSecret(s.key, secret)
	if e != nil {
		write(w, 503, nil)
		return
	}
	v, e = s.store.RotateCredential(v.ID, secret[:10], auth.HashAPIKey(secret), encrypted)
	if e != nil {
		write(w, 409, nil)
		return
	}
	s.users.RecordAudit(auditActor(u), r, "credential.rotate", "credential", v.ID, 200, nil)
	write(w, 200, map[string]any{"api_key": secret, "credential": v})
}

// ReauthenticateAdmin protects legacy administration endpoints as well as the new shared directory.
func (s *Service) ReauthenticateAdmin(r *http.Request, password, token string) error {
	session, _ := auth.SessionToken(r)
	u, e := s.users.ValidateSession(session)
	if e != nil {
		return e
	}
	_, e = s.users.VerifyPassword(u.Username, password)
	return e
}
func (s *Service) userUsage(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("user_id")
	if _, e := s.store.GetUserByID(id); e != nil {
		write(w, 404, nil)
		return
	}
	st, ok := s.store.(interface {
		Wallet(context.Context, string) (model.Wallet, error)
		Subscription(context.Context, string) (*model.Subscription, error)
		Usage(context.Context, string, time.Time) (map[string]int64, error)
		Subscriptions(context.Context, string) ([]model.Subscription, error)
	})
	if !ok {
		write(w, 503, nil)
		return
	}
	wallet, e := st.Wallet(r.Context(), id)
	if e != nil {
		write(w, 503, nil)
		return
	}
	sub, e := st.Subscription(r.Context(), id)
	if e != nil {
		write(w, 503, nil)
		return
	}
	usage, e := st.Usage(r.Context(), id, time.Now())
	if e != nil {
		write(w, 503, nil)
		return
	}
	subs, e := st.Subscriptions(r.Context(), id)
	if e != nil {
		write(w, 503, nil)
		return
	}
	write(w, 200, map[string]any{"wallet": wallet, "subscription": sub, "subscriptions": subs, "usage": usage})
}
