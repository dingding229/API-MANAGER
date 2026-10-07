package api

import (
	"api-manager/internal/audit"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"context"
	"net/http"
	"strings"
	"unicode/utf8"
)

type roleManagement interface {
	DeleteRole(context.Context, string) error
	SaveRoleDetails(context.Context, model.Role) error
}

func (a *Admin) deleteRole(w http.ResponseWriter, r *http.Request) {
	if !a.hasPermission(r, "*") {
		writeJSON(w, 403, map[string]string{"error": "仅管理员可删除角色"})
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/admin/v1/roles/")
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !req.Confirm || name == "super_admin" || !store.SupportedRole(name) {
		writeJSON(w, 409, map[string]string{"error": "管理员角色不可删除；请确认删除未使用角色"})
		return
	}
	st, ok := a.store.(roleManagement)
	if !ok {
		writeJSON(w, 503, map[string]string{"error": "角色服务不可用"})
		return
	}
	actor, _ := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	if e := a.auditor.RecordChecked(r.Context(), actor, r, "role.delete.requested", "role", name, 202, nil); e != nil {
		writeJSON(w, 503, map[string]string{"error": "审计不可用，未删除"})
		return
	}
	if e := st.DeleteRole(r.Context(), name); e != nil {
		writeJSON(w, 409, map[string]string{"error": "角色仍被用户或注册配置使用，请先完成调整"})
		return
	}
	a.recordAudit(r, "role.delete", "role", name, 200, nil)
	writeJSON(w, 200, map[string]bool{"deleted": true})
}
func (a *Admin) saveRoleDetails(w http.ResponseWriter, r *http.Request) {
	if !a.hasPermission(r, "*") {
		writeJSON(w, 403, map[string]string{"error": "仅管理员可编辑角色"})
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/admin/v1/roles/")
	var req struct {
		DisplayName string   `json:"display_name"`
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.DisplayName) == "" || utf8.RuneCountInString(req.DisplayName) > 64 || len(req.Description) > 512 || !store.RolePermissionsAllowed(name, req.Permissions) {
		writeJSON(w, 400, map[string]string{"error": "角色名称或权限配置无效"})
		return
	}
	known := map[string]bool{"*": true}
	for _, p := range a.store.ListPermissions() {
		known[p.Code] = true
	}
	for _, p := range req.Permissions {
		if !known[p] {
			writeJSON(w, 400, map[string]string{"error": "存在无效权限"})
			return
		}
	}
	st, ok := a.store.(roleManagement)
	if !ok {
		writeJSON(w, 503, map[string]string{"error": "角色服务不可用"})
		return
	}
	actor, _ := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	if e := a.auditor.RecordChecked(r.Context(), actor, r, "role.update.requested", "role", name, 202, nil); e != nil {
		writeJSON(w, 503, map[string]string{"error": "审计不可用，未保存"})
		return
	}
	if e := st.SaveRoleDetails(r.Context(), model.Role{Name: name, DisplayName: strings.TrimSpace(req.DisplayName), Description: strings.TrimSpace(req.Description), Permissions: req.Permissions}); e != nil {
		writeJSON(w, 409, map[string]string{"error": "角色无法更新，请刷新"})
		return
	}
	a.recordAudit(r, "role.update", "role", name, 200, nil)
	writeJSON(w, 200, map[string]bool{"saved": true})
}
