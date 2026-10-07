package api

import (
	"api-manager/internal/audit"
	"net/http"
	"strings"
)

func (a *Admin) pluginSettings(w http.ResponseWriter, r *http.Request) {
	if a.pluginManager == nil {
		writeJSON(w, 503, nil)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/v1/plugins/"), "/settings")
	if id == "" || strings.Contains(id, "/") {
		writeJSON(w, 404, nil)
		return
	}
	if r.Method == "GET" {
		result, e := a.pluginManager.Settings(r.Context(), id)
		if e != nil {
			writeJSON(w, 400, map[string]string{"error": e.Error()})
			return
		}
		writeJSON(w, 200, result)
		return
	}
	var req struct {
		Version      int64          `json:"version"`
		Changes      map[string]any `json:"changes"`
		RemoveFields []string       `json:"remove_fields"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	actor, _ := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	if e := a.auditor.RecordChecked(r.Context(), actor, r, "plugin.settings.update.requested", "plugin", id, 202, nil); e != nil {
		writeJSON(w, 503, nil)
		return
	}
	if e := a.pluginManager.SaveSettings(r.Context(), id, req.Changes, req.RemoveFields, req.Version); e != nil {
		writeJSON(w, 409, map[string]string{"error": e.Error()})
		return
	}
	a.recordAudit(r, "plugin.settings.update", "plugin", id, 200, nil)
	writeJSON(w, 200, map[string]bool{"updated": true})
}
