package user

import (
	"encoding/json"
	"net/http"

	"api-manager/internal/auth"
	"api-manager/internal/model"
)

// HTTP exposes only the KEY-authenticated console profile. No account login,
// bootstrap or client-issued sessions are needed.
type HTTP struct{ adminKey string }

func NewHTTP(adminKey string) *HTTP { return &HTTP{adminKey: adminKey} }
func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != "/auth/v1/me" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "auth route not found"})
		return
	}
	if !auth.MatchesKey(h.adminKey, auth.RequestKey(r)) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid API Key"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":        model.User{ID: "admin-key", Email: "KEY 管理员", Role: "super_admin", Roles: []string{"super_admin"}, Status: "active"},
		"permissions": []string{"*"},
	})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
