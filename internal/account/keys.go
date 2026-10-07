package account

import (
	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

func (s *Service) revealOwnKey(w http.ResponseWriter, r *http.Request, u model.User) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/account/v1/keys/"), "/reveal")
	if id == "" || strings.Contains(id, "/") {
		write(w, 404, map[string]string{"error": "凭据不存在"})
		return
	}
	// Check ownership before reauthentication: a session can never request another user's secret.
	key, e := s.store.GetCredential(id)
	if e != nil || key.OwnerUserID != u.ID {
		write(w, 404, map[string]string{"error": "凭据不存在"})
		return
	}
	var req payload
	if !read(w, r, &req) {
		return
	}
	if e = s.reauthenticate(r, u, req); e != nil {
		write(w, 403, map[string]string{"error": "请验证当前密码与双重验证"})
		return
	}
	if key.Revoked || key.EncryptedKey == "" {
		write(w, 409, map[string]string{"error": "此凭据已吊销或没有可恢复的密钥，请新建凭据"})
		return
	}
	secret, e := auth.DecryptSecret(s.key, key.EncryptedKey)
	if e != nil || subtle.ConstantTimeCompare([]byte(auth.HashAPIKey(secret)), []byte(key.Hash)) != 1 {
		write(w, 409, map[string]string{"error": "密钥不可恢复，请新建凭据"})
		return
	}
	if e = audit.New(s.store, nil).RecordChecked(r.Context(), auditActor(u), r, "account.key.reveal", "credential", id, 200, nil); e != nil {
		write(w, 503, map[string]string{"error": "审计不可用，未展示密钥"})
		return
	}
	write(w, 200, map[string]string{"api_key": secret})
}

// The administration directory lists all users, but never decrypts secrets in its list response.
func (s *Service) credentialDirectory(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store.(interface {
		CredentialDirectory(context.Context) ([]model.Credential, error)
	})
	if !ok {
		write(w, 503, map[string]string{"error": "凭据列表不可用"})
		return
	}
	list, e := st.CredentialDirectory(r.Context())
	if e != nil {
		write(w, 503, map[string]string{"error": "凭据列表读取失败"})
		return
	}
	write(w, 200, list)
}
