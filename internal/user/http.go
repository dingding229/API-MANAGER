package user

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"api-manager/internal/model"
)

type HTTP struct {
	service     *Service
	adminToken  string
	bootstrapMu sync.Mutex // Single-replica guard for concurrent first-use requests.
}

func NewHTTP(service *Service, adminToken string) *HTTP {
	return &HTTP{service: service, adminToken: adminToken}
}

func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/auth/v1/login":
		h.login(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/auth/v1/bootstrap/status":
		h.bootstrapStatus(w)
	case r.Method == http.MethodPost && r.URL.Path == "/auth/v1/bootstrap":
		h.bootstrap(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/auth/v1/me":
		h.me(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "auth route not found"})
	}
}

func (h *HTTP) login(w http.ResponseWriter, r *http.Request) {
	var request model.LoginRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	user, token, err := h.service.Authenticate(request.Email, request.Password)
	if errors.Is(err, ErrInvalidCredentials) {
		h.service.RecordAudit(audit.Actor{Type: "anonymous"}, r, "auth.login.denied", "user", "", http.StatusUnauthorized, nil)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	h.service.RecordAudit(audit.Actor{ID: user.ID, Type: "user", Email: user.Email}, r, "auth.login", "user", user.ID, http.StatusOK, map[string]any{"email": user.Email})
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": user, "permissions": h.service.store.GetUserPermissions(user.ID)})
}

func (h *HTTP) me(w http.ResponseWriter, r *http.Request) {
	user, err := h.service.ValidateToken(auth.ExtractAPIKey(r.Header.Get("Authorization")))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.Profile(user))
}

func (h *HTTP) bootstrapStatus(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	count, err := h.service.CountChecked()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "bootstrap state unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"available": count == 0})
}

func (h *HTTP) bootstrap(w http.ResponseWriter, r *http.Request) {
	h.bootstrapMu.Lock()
	defer h.bootstrapMu.Unlock()

	count, err := h.service.CountChecked()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "bootstrap state unavailable"})
		return
	}
	if count != 0 {
		h.service.RecordAudit(audit.Actor{Type: "anonymous"}, r, "auth.bootstrap.denied", "user", "", http.StatusNotFound, nil)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "auth route not found"})
		return
	}

	provided := r.Header.Get("X-Admin-Token")
	if provided == "" {
		provided = auth.ExtractAPIKey(r.Header.Get("Authorization"))
	}
	if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(h.adminToken)) != 1 {
		h.service.RecordAudit(audit.Actor{Type: "anonymous"}, r, "auth.bootstrap.denied", "user", "", http.StatusForbidden, nil)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "bootstrap is not available"})
		return
	}
	var request model.CreateUserRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	user, err := h.service.Create(request.Email, request.Password, "super_admin")
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "conflict") {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	h.service.RecordAudit(audit.Actor{Type: "bootstrap"}, r, "user.bootstrap", "user", user.ID, http.StatusCreated, map[string]any{"email": user.Email, "roles": user.Roles})
	writeJSON(w, http.StatusCreated, user)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
