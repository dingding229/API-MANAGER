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

type HTTP struct {
	resetGuard     func(*http.Request, string, string) (int64, error)
	service        *Service
	production     bool
	accountHandler http.Handler
	criticalGuard  func(*http.Request, string, string) error
}

func (h *HTTP) SetAccountHandler(handler http.Handler) { h.accountHandler = handler }
func (h *HTTP) SetProductionMode(value bool)           { h.production = value }

func NewHTTP(service *Service) *HTTP      { return &HTTP{service: service} }
func sessionToken(r *http.Request) string { token, _ := auth.SessionToken(r); return token }
func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.accountHandler != nil && (r.URL.Path == "/auth/v1/login" || r.URL.Path == "/test/v1/login" || strings.HasPrefix(r.URL.Path, "/account/v1/")) {
		h.accountHandler.ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/test/v1/") {
		h.testing(w, r)
		return
	}
	if _, cookieAuth := auth.SessionToken(r); cookieAuth && auth.UnsafeMethod(r.Method) && !auth.CookieMutationAllowed(r) && r.URL.Path != "/auth/v1/login" {
		writeJSON(w, 403, map[string]string{"error": "请在本站重新提交操作。"})
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/auth/v1/setup":
		available, err := h.service.SetupAvailable()
		if err != nil {
			writeJSON(w, 503, map[string]string{"error": "registration unavailable"})
			return
		}
		writeJSON(w, 200, map[string]bool{"available": available})
	case r.Method == http.MethodPost && r.URL.Path == "/auth/v1/setup":
		var request struct {
			Key      string `json:"key"`
			Username string `json:"username"`
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON body"})
			return
		}
		u, err := h.service.RegisterInitialAdmin(request.Key, request.Username, request.Email, request.Password)
		if err != nil {
			status := 403
			if errors.Is(err, ErrInvalidProfile) {
				status = 400
			}
			writeJSON(w, status, map[string]string{"error": "registration unavailable, invalid key or invalid profile"})
			return
		}
		h.service.RecordAudit(audit.Actor{ID: u.ID, Type: "user", Email: u.Username}, r, "auth.setup", "user", u.ID, 201, nil)
		writeJSON(w, 201, map[string]any{"user": u})

	case r.Method == http.MethodGet && r.URL.Path == "/auth/v1/recovery":
		h.recoveryStatus(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/auth/v1/forgot-password":
		h.forgotPassword(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/auth/v1/reset-password":
		h.resetPassword(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/auth/v1/session":
		if len(r.Header.Values("Authorization")) != 1 {
			writeJSON(w, 401, map[string]string{"error": "invalid credentials"})
			return
		}
		if _, err := h.service.ValidateSession(sessionToken(r)); err != nil {
			writeJSON(w, 401, map[string]string{"error": "invalid credentials"})
			return
		}
		if r.Header.Get("Origin") != "" && !auth.CookieMutationAllowed(r) {
			writeJSON(w, 403, map[string]string{"error": "请在本站重新登录。"})
			return
		}
		setTestingCookie(w, r, sessionToken(r), h.production)
		w.WriteHeader(204)
	case r.Method == http.MethodPost && r.URL.Path == "/auth/v1/login":
		if r.Header.Get("Origin") != "" && !auth.CookieMutationAllowed(r) {
			writeJSON(w, 403, map[string]string{"error": "请在本站登录。"})
			return
		}
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
		user, token, err := h.service.AuthenticateRequest(username, request.Password, r)
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
		if previous := sessionToken(r); previous != "" && previous != token {
			_ = h.service.Logout(previous)
		}
		h.service.RecordAudit(audit.Actor{ID: user.ID, Type: "user", Email: user.Username}, r, "auth.login", "user", user.ID, 200, nil)
		setTestingCookie(w, r, token, h.production)
		if r.Header.Get("X-API-Request") == "1" {
			writeJSON(w, 200, map[string]any{"user": user, "permissions": h.service.store.GetUserPermissions(user.ID)})
		} else {
			writeJSON(w, 200, map[string]any{"token": token, "user": user, "permissions": h.service.store.GetUserPermissions(user.ID)})
		}
	case r.Method == http.MethodGet && r.URL.Path == "/auth/v1/me":
		user, err := h.service.ValidateSession(sessionToken(r))
		if err != nil {
			writeJSON(w, 401, map[string]string{"error": "invalid credentials"})
			return
		}
		h.service.TouchSession(sessionToken(r), r)
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
		clearTestingCookie(w, r, h.production)
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

func (h *HTTP) SetCriticalGuard(guard func(*http.Request, string, string) error) {
	h.criticalGuard = guard
}

func (h *HTTP) SetResetGuard(guard func(*http.Request, string, string) (int64, error)) {
	h.resetGuard = guard
}
