package httpx

import (
	"api-manager/internal/ratelimit"
	"net"
	"net/http"
	"time"
)

// ThrottleAdmin protects the management plane independently of API route quotas.
// Only the actual TCP peer is used; user-controlled forwarded headers are untrusted.
func ThrottleAdmin(limiter ratelimit.Limiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, scope := 0, ""
		switch {
		case r.URL.Path == "/auth/v1/login" || r.URL.Path == "/auth/v1/setup":
			limit, scope = 10, "login"
		case r.URL.Path == "/auth/v1/me" && r.Method == http.MethodPut:
			limit, scope = 10, "profile"
		case len(r.URL.Path) >= 7 && r.URL.Path[:7] == "/admin/":
			limit, scope = 120, "admin"
		}
		if limit > 0 {
			peer, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				peer = r.RemoteAddr
			}
			if peer == "" {
				peer = "unknown"
			}
			if !limiter.Allow("control:"+scope+":"+peer, limit, time.Minute, time.Now()) {
				w.Header().Set("Retry-After", "60")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"management rate limit exceeded"}`))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
