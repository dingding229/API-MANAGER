package user

import (
	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type HTTP struct{ service *Service }

func NewHTTP(service *Service) *HTTP { return &HTTP{service: service} }
func sessionToken(r *http.Request) string {
	if len(r.Header.Values("Authorization")) != 1 || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || r.Header.Get("X-API-Key") != "" {
		return ""
	}
	return auth.ExtractAPIKey(r.Header.Get("Authorization"))
}
func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/auth/v1/login":
		var request model.LoginRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON body"})
			return
		}
		username := request.Username
		if username == "" {
			username = request.Email
		}
		user, token, err := h.service.Authenticate(username, request.Password)
		if err != nil {
			status := http.StatusServiceUnavailable
			message := "authentication unavailable"
			if errors.Is(err, ErrInvalidCredentials) {
				status = http.StatusUnauthorized
				message = "invalid credentials"
			}
			h.service.RecordAudit(audit.Actor{Type: "anonymous"}, r, "auth.login.denied", "user", "", status, nil)
			writeJSON(w, status, map[string]string{"error": message})
			return
		}
		h.service.RecordAudit(audit.Actor{ID: user.ID, Type: "user", Email: user.Email}, r, "auth.login", "user", user.ID, 200, nil)
		writeJSON(w, 200, map[string]any{"token": token, "user": user, "permissions": h.service.store.GetUserPermissions(user.ID)})
	case r.Method == http.MethodGet && r.URL.Path == "/auth/v1/me":
		user, err := h.service.ValidateSession(sessionToken(r))
		if err != nil {
			writeJSON(w, 401, map[string]string{"error": "invalid credentials"})
			return
		}
		writeJSON(w, 200, h.service.Profile(user))
	case r.Method == http.MethodPut && r.URL.Path == "/auth/v1/me":
		h.updateOwnProfile(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/auth/v1/logout":
		token := sessionToken(r)
		if _, err := h.service.ValidateSession(token); err != nil {
			writeJSON(w, 401, map[string]string{"error": "invalid credentials"})
			return
		}
		if err := h.service.Logout(token); err != nil {
			writeJSON(w, 503, map[string]string{"error": "logout unavailable"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, 404, map[string]string{"error": "auth route not found"})
	}
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
