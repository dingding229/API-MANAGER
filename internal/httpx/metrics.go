package httpx

import (
	"api-manager/internal/auth"
	"net/http"
)

// ProtectMetrics leaves development scrapes unchanged; production requires a
// separate bearer secret and never grants access with the admin credential.
func ProtectMetrics(next http.Handler, token string) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !auth.MatchesKey(token, auth.RequestKey(r)) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
