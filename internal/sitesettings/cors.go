package sitesettings

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var corsHeaderName = regexp.MustCompile(`^[a-zA-Z0-9-]{1,80}$`)

// PublicAPICORS enables credential-free browser testing from the configured
// website only. It never opens auth/admin routes or permits session cookies.
func (s *Service) PublicAPICORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		site := s.Public()
		configured := strings.TrimRight(site.WebsiteURL, "/")
		u, err := url.Parse(origin)
		allowed := strings.HasPrefix(r.URL.Path, "/api/") && configured != "" && err == nil && u.User == nil && origin == configured
		if allowed {
			headers := []string{"Content-Type", "X-API-Key"}
			valid := true
			requested := strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",")
			if len(requested) > 50 {
				valid = false
			}
			for _, name := range requested {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				if !corsHeaderName.MatchString(name) {
					valid = false
					break
				}
				switch strings.ToLower(name) {
				case "authorization", "cookie", "host", "proxy-authorization", "connection", "transfer-encoding", "content-length":
					valid = false
				}
				headers = append(headers, name)
			}
			if valid {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", strings.Join(headers, ", "))
				w.Header().Set("Access-Control-Expose-Headers", "Content-Type, X-Request-ID, Retry-After")
				if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
					w.WriteHeader(204)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
