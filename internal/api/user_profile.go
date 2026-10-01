package api

import (
	"net/http"
	"strings"

	"api-manager/internal/audit"
	"api-manager/internal/user"
)

func (a *Admin) updateUserProfile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/v1/users/"), "/profile")
	if id == "" || strings.Contains(id, "/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user route not found"})
		return
	}
	request, ok := user.DecodeProfileUpdate(w, r)
	if !ok {
		return
	}
	actor, ok := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "admin authentication required"})
		return
	}
	updated, changed, err := a.userManager.UpdateProfile(actor.ID, id, request)
	if err != nil {
		user.WriteProfileError(w, err)
		return
	}
	a.recordAudit(r, "user.profile.update", "user", updated.ID, http.StatusOK, user.ProfileUpdateAudit(updated, request, changed))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, user.ProfileUpdateResponse(actor.ID, updated, changed))
}
