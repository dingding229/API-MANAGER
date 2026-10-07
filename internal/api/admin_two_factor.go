package api

import (
	"api-manager/internal/audit"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"
)

func (a *Admin) userTwoFactor(w http.ResponseWriter, r *http.Request) {
	if !a.globalAPIScope(r) {
		writeJSON(w, 403, map[string]string{"error": "仅管理员可处理用户双重验证"})
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/v1/users/"), "/two-factor")
	if id == "" || strings.Contains(id, "/") {
		writeJSON(w, 404, nil)
		return
	}
	target, e := a.store.GetUserByID(id)
	if e != nil || target.Status == "deleted" {
		writeJSON(w, 404, nil)
		return
	}
	st, ok := a.store.(interface {
		Account(context.Context, string) (model.AccountRecord, error)
		ResetUserTwoFactor(context.Context, string, int64) error
	})
	if !ok {
		writeJSON(w, 503, nil)
		return
	}
	if r.Method == "GET" {
		record, e := st.Account(r.Context(), id)
		if e != nil {
			writeJSON(w, 503, nil)
			return
		}
		writeJSON(w, 200, map[string]any{"enabled": record.TOTPSecret != "", "pending": record.TOTPPending != "", "recovery_count": len(record.RecoveryHashes), "revision": target.AuthRevision})
		return
	}
	if id == a.actorID(r) {
		writeJSON(w, 403, map[string]string{"error": "不能在用户管理重置当前管理员的双重验证，请在账号安全中验证后关闭"})
		return
	}
	var q struct {
		Confirm         bool   `json:"confirm"`
		Username        string `json:"username"`
		Reason          string `json:"reason"`
		Revision        *int64 `json:"revision"`
		CurrentPassword string `json:"current_password"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	q.Reason = strings.TrimSpace(q.Reason)
	if !q.Confirm || q.Username != target.Username || q.Revision == nil || utf8.RuneCountInString(q.Reason) < 4 || utf8.RuneCountInString(q.Reason) > 200 {
		writeJSON(w, 400, map[string]string{"error": "请确认用户名、填写至少 4 字的处理原因，并重新加载安全状态"})
		return
	}
	if a.credentialGuard == nil || a.credentialGuard(r, q.CurrentPassword, "") != nil {
		writeJSON(w, 403, map[string]string{"error": "请验证当前管理员密码"})
		return
	}
	actor, _ := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	if e = a.auditor.RecordChecked(r.Context(), actor, r, "user.two_factor.reset.requested", "user", id, 202, map[string]any{"reason": q.Reason}); e != nil {
		writeJSON(w, 503, nil)
		return
	}
	if e = st.ResetUserTwoFactor(r.Context(), id, *q.Revision); e != nil {
		status := 503
		if errors.Is(e, store.ErrConflict) {
			status = 409
		}
		writeJSON(w, status, map[string]string{"error": "安全状态已变化，请刷新后重试"})
		return
	}
	a.recordAudit(r, "user.two_factor.reset", "user", id, 200, map[string]any{"reason": q.Reason})
	writeJSON(w, 200, map[string]bool{"reset": true, "sessions_revoked": true, "current_user": id == a.actorID(r)})
}
