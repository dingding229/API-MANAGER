package web

import (
	"net/http"
	"regexp"
	"strings"
)

var adminPathPattern = regexp.MustCompile(`^/[a-zA-Z0-9_-]+(/[a-zA-Z0-9_-]+)*$`)

func ValidAdminPath(value string) bool {
	if len(value) > 100 || !adminPathPattern.MatchString(value) {
		return false
	}
	for _, path := range []string{"/api", "/auth", "/health", "/metrics", "/public", "/_next", "/catalog.json", "/admin/v1"} {
		if value == path || strings.HasPrefix(value, path+"/") {
			return false
		}
	}
	return true
}

// MountAdministration separates the fixed authenticated API routes from the
// configurable UI path, including the default /admin prefix.
func MountAdministration(mux *http.ServeMux, path string, admin, auth http.Handler) {
	mux.Handle("/admin/v1/", admin)
	mux.Handle("/auth/", auth)

	console := ConsoleAt(path)
	mux.Handle(path+"/", console)
	mux.Handle(path, console)
}
