package account

import (
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"fmt"
	"net/http"
)

func (s *Service) confirmationSettings(w http.ResponseWriter, r *http.Request, u model.User, cfg model.SecuritySettings) {
	if r.Method != "GET" && !s.users.Can(u.ID, "account.security") {
		write(w, 403, nil)
		return
	}
	st, ok := s.store.(store.ConfirmationStore)
	if !ok {
		write(w, 503, nil)
		return
	}
	method, e := st.ConfirmationMethod(r.Context(), u.ID)
	if e != nil {
		write(w, 503, nil)
		return
	}
	wa, waErr := s.passkeyClient(cfg)
	keysAvailable := false
	if waErr == nil {
		if keys, err := s.passkeyUser(r.Context(), u, wa.Config.RPID); err == nil {
			keysAvailable = len(keys.credentials) > 0
		}
	}
	if r.Method == "GET" {
		write(w, 200, map[string]any{"method": method, "passkey_available": keysAvailable})
		return
	}
	if r.Method != "PUT" || !auth.CookieMutationAllowed(r) {
		write(w, 403, nil)
		return
	}
	var q struct {
		Method          string `json:"method"`
		CurrentPassword string `json:"current_password"`
		TOTPCode        string `json:"totp_code"`
	}
	if !read(w, r, &q) {
		return
	}
	if q.Method != "password" && q.Method != "passkey" {
		write(w, 400, nil)
		return
	}
	if e = s.reauthenticate(r, u, payload{CurrentPassword: q.CurrentPassword, TOTPCode: q.TOTPCode}); e != nil {
		write(w, 403, map[string]string{"error": "请先确认当前账号身份"})
		return
	}
	rpid := ""
	if waErr == nil {
		rpid = wa.Config.RPID
	}
	if q.Method == "passkey" && !keysAvailable {
		write(w, 409, map[string]string{"error": "请先添加当前网站的通行密钥"})
		return
	}
	if e = st.SetConfirmationMethod(r.Context(), u.ID, q.Method, u.AuthRevision, rpid); e != nil {
		write(w, 409, map[string]string{"error": "账号已变化，请刷新后重试"})
		return
	}
	s.users.RecordAudit(auditActor(u), r, "account.confirmation.preference", "user", u.ID, 200, map[string]any{"method": q.Method})
	write(w, 200, map[string]bool{"updated": true})
}

func (s *Service) verifyConfirmation(r *http.Request, u model.User, password string) error {
	if auth.PasskeyConfirmed(r.Context(), u.ID) {
		return nil
	}
	if st, ok := s.store.(store.ConfirmationStore); ok {
		method, e := st.ConfirmationMethod(r.Context(), u.ID)
		if e != nil {
			return e
		}
		if method == "passkey" {
			return fmt.Errorf("请使用用户中心设置的通行密钥确认")
		}
	}
	_, e := s.users.VerifyPassword(u.Username, password)
	return e
}
