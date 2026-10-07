package api

import (
	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"context"
	"errors"
	"net/http"
	"strings"
)

type versionSettingsStore interface {
	VersionCheckSettings(context.Context) (model.VersionCheckSettings, error)
	SaveVersionCheckSettings(context.Context, int64, string) (model.VersionCheckSettings, error)
}

func (a *Admin) LoadVersionSettings(ctx context.Context) error {
	st, ok := a.store.(versionSettingsStore)
	if !ok || a.versionChecker == nil {
		return nil
	}
	v, e := st.VersionCheckSettings(ctx)
	if errors.Is(e, store.ErrNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	token := ""
	if v.EncryptedToken != "" {
		token, e = auth.DecryptSecret(a.credentialEncryptionKey+":version", v.EncryptedToken)
		if e != nil {
			return errors.New("cannot decrypt version source credential")
		}
	}
	a.versionChecker.SetToken(token)
	return nil
}
func (a *Admin) versionSettings(w http.ResponseWriter, r *http.Request) {
	if !a.globalAPIScope(r) {
		writeJSON(w, 403, nil)
		return
	}
	st, ok := a.store.(versionSettingsStore)
	if !ok || a.versionChecker == nil {
		writeJSON(w, 503, nil)
		return
	}
	v, e := st.VersionCheckSettings(r.Context())
	if e != nil && !errors.Is(e, store.ErrNotFound) {
		writeJSON(w, 503, nil)
		return
	}
	if r.Method == "GET" {
		writeJSON(w, 200, map[string]any{"version": v.Version, "token_set": a.versionChecker.TokenConfigured()})
		return
	}
	var q struct {
		Version         int64   `json:"version"`
		Token           *string `json:"token"`
		Clear           bool    `json:"clear_token"`
		CurrentPassword string  `json:"current_password"`
		TurnstileToken  string  `json:"turnstile_token"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	if a.credentialGuard == nil || a.credentialGuard(r, q.CurrentPassword, q.TurnstileToken) != nil {
		writeJSON(w, 403, map[string]string{"error": "请验证管理员密码"})
		return
	}
	if q.Version != v.Version {
		writeJSON(w, 409, map[string]string{"error": "设置已更新，请重新加载"})
		return
	}
	if !q.Clear && (q.Token == nil || *q.Token == "") {
		writeJSON(w, 200, map[string]any{"version": v.Version, "token_set": a.versionChecker.TokenConfigured()})
		return
	}
	encrypted := v.EncryptedToken
	if q.Clear {
		if q.Token != nil && *q.Token != "" {
			writeJSON(w, 400, nil)
			return
		}
		encrypted = ""
	} else if q.Token != nil && *q.Token != "" {
		token := strings.TrimSpace(*q.Token)
		if len(token) < 20 || len(token) > 4096 || strings.ContainsAny(token, "\r\n\x00 \t") {
			writeJSON(w, 400, map[string]string{"error": "请填写有效的仓库读取凭据"})
			return
		}
		encrypted, e = auth.EncryptSecret(a.credentialEncryptionKey+":version", token)
		if e != nil {
			writeJSON(w, 503, nil)
			return
		}
	}
	actor, _ := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	if e = a.auditor.RecordChecked(r.Context(), actor, r, "version.credential.update.requested", "settings", "version", 202, nil); e != nil {
		writeJSON(w, 503, nil)
		return
	}
	_, e = st.SaveVersionCheckSettings(r.Context(), q.Version, encrypted)
	if e != nil {
		writeJSON(w, 409, nil)
		return
	}
	if e = a.LoadVersionSettings(r.Context()); e != nil {
		writeJSON(w, 503, nil)
		return
	}
	writeJSON(w, 200, map[string]any{"version": q.Version + 1, "token_set": a.versionChecker.TokenConfigured()})
}
