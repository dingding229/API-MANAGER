package api

import (
	"api-manager/internal/audit"
	"api-manager/internal/store"
	"context"
	"errors"
	"net/http"
	"strings"
)

func (a *Admin) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/admin/v1/users/")
	if id == "" || strings.Contains(id, "/") {
		writeJSON(w, 404, nil)
		return
	}
	if id == a.actorID(r) || !a.mayManageRoles(r, id, nil) {
		writeJSON(w, 403, map[string]string{"error": "不能删除当前账号或更高权限账号"})
		return
	}
	var q struct {
		Confirm         bool   `json:"confirm"`
		Username        string `json:"username"`
		CurrentPassword string `json:"current_password"`
		TurnstileToken  string `json:"turnstile_token"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	target, e := a.store.GetUserByID(id)
	if e != nil || target.Status == "deleted" {
		writeJSON(w, 404, nil)
		return
	}
	if !q.Confirm || q.Username != target.Username {
		writeJSON(w, 400, map[string]string{"error": "请填写待删除用户的用户名并确认"})
		return
	}
	if a.credentialGuard == nil || a.credentialGuard(r, q.CurrentPassword, q.TurnstileToken) != nil {
		writeJSON(w, 403, map[string]string{"error": "请重新验证管理员密码"})
		return
	}
	st, ok := a.store.(interface {
		DeleteUser(context.Context, string) error
	})
	if !ok {
		writeJSON(w, 503, nil)
		return
	}
	actor, _ := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	if e = a.auditor.RecordChecked(r.Context(), actor, r, "user.delete.requested", "user", id, 202, nil); e != nil {
		writeJSON(w, 503, nil)
		return
	}
	if e = st.DeleteUser(r.Context(), id); e != nil {
		code := 503
		if errors.Is(e, store.ErrConflict) {
			code = 409
		}
		writeJSON(w, code, map[string]string{"error": "删除未完成：不能删除最后一个管理员，或账号尚有调用结算中的金额"})
		return
	}
	a.recordAudit(r, "user.delete", "user", id, 200, nil)
	writeJSON(w, 200, map[string]bool{"deleted": true})
}
