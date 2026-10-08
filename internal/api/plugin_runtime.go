package api

import (
	"api-manager/internal/audit"
	"api-manager/internal/model"
	"net/http"
	"strings"
)

func (a *Admin) pluginRuntimePolicy(w http.ResponseWriter, r *http.Request) {
	if a.pluginManager == nil {
		writeJSON(w, 503, nil)
		return
	}
	// A per-plugin switch grants new network/data privileges. Only the site owner
	// can grant them, even if an API developer owns every API using the plugin.
	if !a.globalAPIScope(r) {
		writeJSON(w, 403, map[string]string{"error": "仅超级管理员可授权插件外部网络与会话存储能力"})
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/v1/plugins/"), "/runtime")
	if id == "" || strings.Contains(id, "/") {
		writeJSON(w, 404, nil)
		return
	}
	if r.Method == "GET" {
		p, e := a.pluginManager.RuntimePolicy(r.Context(), id)
		if e != nil {
			writeJSON(w, 404, nil)
			return
		}
		writeJSON(w, 200, p)
		return
	}
	var req struct {
		Policy          model.PluginRuntimePolicy `json:"policy"`
		CurrentPassword string                    `json:"current_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if a.credentialGuard == nil || a.credentialGuard(r, req.CurrentPassword, "") != nil {
		writeJSON(w, 403, map[string]string{"error": "请使用密码或通行密钥确认"})
		return
	}
	actor, _ := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	if e := a.auditor.RecordChecked(r.Context(), actor, r, "plugin.runtime.grant.requested", "plugin", id, 202, nil); e != nil {
		writeJSON(w, 503, nil)
		return
	}
	if e := a.pluginManager.SaveRuntimePolicy(r.Context(), id, req.Policy, req.Policy.Version); e != nil {
		writeJSON(w, 409, map[string]string{"error": e.Error()})
		return
	}
	a.recordAudit(r, "plugin.runtime.grant", "plugin", id, 200, nil)
	writeJSON(w, 200, map[string]bool{"updated": true})
}
