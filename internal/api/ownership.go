package api

import (
	"api-manager/internal/audit"
	"api-manager/internal/model"
	"net/http"
	"strings"
)

type requestAccountKey struct{}
type requestPermissionsKey struct{}

func (a *Admin) actorID(r *http.Request) string {
	actor, _ := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	return actor.ID
}

// A global permission never silently removes the developer's ownership boundary.
func (a *Admin) globalAPIScope(r *http.Request) bool {
	u, ok := r.Context().Value(requestAccountKey{}).(model.User)
	var err error
	if !ok {
		u, err = a.store.GetUserByID(a.actorID(r))
	}
	return err == nil && u.Status == "active" && (u.Role == "super_admin" || containsRole(u.Roles, "super_admin"))
}
func (a *Admin) visibleAPIs(r *http.Request, list []model.API) []model.API {
	if a.globalAPIScope(r) {
		return list
	}
	id := a.actorID(r)
	result := make([]model.API, 0, len(list))
	for _, v := range list {
		if v.OwnerUserID != "" && v.OwnerUserID == id {
			result = append(result, v)
		}
	}
	return result
}
func (a *Admin) authorizeAPIOwner(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/admin/v1/apis/") {
		return true
	}
	id := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/v1/apis/"), "/")[0]
	v, err := a.store.GetAPI(id)
	if err != nil || (!a.globalAPIScope(r) && (v.OwnerUserID == "" || v.OwnerUserID != a.actorID(r))) {
		writeJSON(w, 404, map[string]string{"error": "接口不存在或不属于当前账号"})
		return false
	}
	return true
}

func (a *Admin) listAPIsForActor(r *http.Request) ([]model.API, error) {
	if !a.globalAPIScope(r) {
		if st, ok := a.store.(interface {
			ListOwnedAPIsChecked(string) ([]model.API, error)
		}); ok {
			return st.ListOwnedAPIsChecked(a.actorID(r))
		}
	}
	list, e := a.listAPIsChecked()
	if e != nil {
		return nil, e
	}
	return a.visibleAPIs(r, list), nil
}

// Shared plugin state is also an indirect write to every API using that plugin.
func (a *Admin) authorizeSharedPlugin(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/admin/v1/plugins/") || a.globalAPIScope(r) {
		return true
	}
	if r.Method == "GET" && !strings.HasSuffix(r.URL.Path, "/settings") {
		return true
	}
	id := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/v1/plugins/"), "/")[0]
	p, e := a.store.GetPlugin(id)
	if e != nil {
		writeJSON(w, 404, nil)
		return false
	}
	apis, e := a.listAPIsChecked()
	if e != nil {
		writeJSON(w, 503, nil)
		return false
	}
	for _, v := range apis {
		if v.Plugin == p.Name && v.OwnerUserID != a.actorID(r) {
			writeJSON(w, 403, map[string]string{"error": "此共享插件影响其他用户的接口，请联系管理员处理"})
			return false
		}
	}
	return true
}
