package user

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"api-manager/internal/audit"
	"api-manager/internal/model"
	"api-manager/internal/store"
)

// DecodeProfileUpdate is strict so a basic-info request cannot mutate roles or status.
func DecodeProfileUpdate(w http.ResponseWriter, r *http.Request) (model.UpdateUserProfileRequest, bool) {
	var request model.UpdateUserProfileRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body", "code": "invalid_profile"})
		return request, false
	}
	return request, true
}

func WriteProfileError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusServiceUnavailable, "profile_unavailable", "profile update unavailable"
	switch {
	case errors.Is(err, ErrCurrentPassword):
		status, code, message = http.StatusForbidden, "current_password_invalid", "current password is incorrect"
	case errors.Is(err, ErrProfileForbidden):
		status, code, message = http.StatusForbidden, "profile_forbidden", "insufficient permission to modify this account"
	case errors.Is(err, ErrInvalidProfile):
		status, code, message = http.StatusBadRequest, "invalid_profile", err.Error()
	case errors.Is(err, store.ErrNotFound):
		status, code, message = http.StatusNotFound, "user_not_found", "user not found"
	case errors.Is(err, store.ErrConflict):
		status, code, message = http.StatusConflict, "profile_conflict", "username/email already exists or account was modified; refresh and retry"
	}
	writeJSON(w, status, map[string]string{"error": message, "code": code})
}

func ProfileUpdateResponse(actorID string, user model.User, changed bool) map[string]any {
	return map[string]any{"user": user, "sessions_revoked": changed, "reauthentication_required": actorID == user.ID && changed}
}
func ProfileUpdateAudit(user model.User, request model.UpdateUserProfileRequest, changed bool) map[string]any {
	return map[string]any{"username": user.Username, "email_changed": request.Email != nil, "password_changed": request.Password != nil, "sessions_revoked": changed}
}
func (h *HTTP) updateOwnProfile(w http.ResponseWriter, r *http.Request) {
	actor, err := h.service.ValidateSession(sessionToken(r))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	request, ok := DecodeProfileUpdate(w, r)
	if !ok {
		return
	}
	if h.criticalGuard != nil {
		if err := h.criticalGuard(r, "sensitive", request.TurnstileToken); err != nil {
			writeJSON(w, 403, map[string]string{"error": err.Error()})
			return
		}
	}
	updated, changed, err := h.service.UpdateProfile(actor.ID, actor.ID, request)
	if err != nil {
		WriteProfileError(w, err)
		return
	}
	h.service.RecordAudit(audit.Actor{ID: actor.ID, Type: "user", Email: actor.Username}, r, "user.profile.update", "user", updated.ID, http.StatusOK, ProfileUpdateAudit(updated, request, changed))
	writeJSON(w, http.StatusOK, ProfileUpdateResponse(actor.ID, updated, changed))
}
