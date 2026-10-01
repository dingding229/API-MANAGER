package api

import (
	"net/http"
	"time"
)

func (a *Admin) overview(w http.ResponseWriter, r *http.Request) {
	resources := map[string]any{}
	if a.hasPermission(r, "api.read") {
		items, err := a.listAPIsChecked()
		if err != nil {
			writeStoreError(w, err)
			return
		}
		published := 0
		for _, v := range items {
			if v.Enabled {
				published++
			}
		}
		resources["apis"] = len(items)
		resources["published"] = published
	}
	if a.hasPermission(r, "credential.read") {
		items := a.store.ListCredentials()
		active := 0
		for _, v := range items {
			if !v.Revoked && (v.ExpiresAt == nil || v.ExpiresAt.After(time.Now())) {
				active++
			}
		}
		resources["credentials"] = len(items)
		resources["active_credentials"] = active
	}
	if a.hasPermission(r, "plugin.read") {
		items := a.store.ListPlugins()
		enabled := 0
		for _, v := range items {
			if v.Enabled {
				enabled++
			}
		}
		resources["plugins"] = len(items)
		resources["enabled_plugins"] = enabled
	}
	if a.hasPermission(r, "user.read") {
		resources["users"] = a.store.CountUsers()
	}
	result := map[string]any{"resources": resources, "timestamp": time.Now().UTC(), "counter_scope": "process_lifetime"}
	if a.metrics != nil && a.hasPermission(r, "observability.read") {
		snapshot := a.metrics.Snapshot()
		result["metrics"] = snapshot
		recent := snapshot.Recent(5 * time.Minute)
		result["recent_requests"] = recent.Requests
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}
