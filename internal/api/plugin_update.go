package api

import (
	"api-manager/internal/audit"
	"io"
	"net/http"
	"strings"
)

func (a *Admin) updateManagedPlugin(w http.ResponseWriter, r *http.Request) {
	if a.pluginManager == nil {
		writeJSON(w, 503, nil)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/v1/plugins/"), "/update")
	if id == "" || strings.Contains(id, "/") {
		writeJSON(w, 404, nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.pluginManager.MaxUploadBytes()+(64<<10))
	// #nosec G120 -- Whole body is capped above; only 1 MiB metadata is buffered and file parts use bounded temporary storage.
	if e := r.ParseMultipartForm(1 << 20); e != nil {
		writeJSON(w, 400, nil)
		return
	}
	defer r.MultipartForm.RemoveAll()
	if a.credentialGuard == nil || a.credentialGuard(r, r.FormValue("current_password"), "") != nil {
		writeJSON(w, 403, map[string]string{"error": "请按用户中心设置确认身份"})
		return
	}
	mf, _, e := r.FormFile("manifest")
	if e != nil {
		writeJSON(w, 400, nil)
		return
	}
	defer mf.Close()
	wf, _, e := r.FormFile("wasm")
	if e != nil {
		writeJSON(w, 400, nil)
		return
	}
	defer wf.Close()
	manifest, e := io.ReadAll(io.LimitReader(mf, a.pluginManager.MaxManifestBytes()+1))
	if e != nil {
		writeJSON(w, 400, nil)
		return
	}
	wasm, e := io.ReadAll(io.LimitReader(wf, a.pluginManager.MaxUploadBytes()+1))
	if e != nil {
		writeJSON(w, 400, nil)
		return
	}
	actor, _ := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	if e = a.auditor.RecordChecked(r.Context(), actor, r, "plugin.update.requested", "plugin", id, 202, nil); e != nil {
		writeJSON(w, 503, nil)
		return
	}
	item, e := a.pluginManager.Update(r.Context(), id, manifest, wasm)
	if e != nil {
		writeJSON(w, 409, map[string]string{"error": e.Error()})
		return
	}
	a.recordAudit(r, "plugin.update", "plugin", id, 200, map[string]any{"version": item.Version})
	writeJSON(w, 200, item)
}
