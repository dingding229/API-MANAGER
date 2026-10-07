package web

import (
	"api-manager/internal/model"
	"net/http"
	"regexp"
	"strings"
)

var adminPathPattern = regexp.MustCompile(`^/[a-zA-Z0-9_-]+(/[a-zA-Z0-9_-]+)*$`)

func ValidAdminPath(value string) bool {
	if len(value) > 100 || !adminPathPattern.MatchString(value) {
		return false
	}
	for _, path := range []string{"/api", "/auth", "/test", "/account", "/playground", "/docs", "/guide", "/login", "/register", "/complete-registration", "/health", "/metrics", "/public", "/ui", "/_next", "/catalog.json", "/admin/v1"} {
		if value == path || strings.HasPrefix(value, path+"/") {
			return false
		}
	}
	return true
}

// MountAdministration separates the fixed authenticated API routes from the
// configurable UI path, including the default /admin prefix.
func MountAdministration(mux *http.ServeMux, path string, admin, auth http.Handler) {
	MountAdministrationWithSite(mux, path, admin, auth, nil)
}
func MountAdministrationWithSite(mux *http.ServeMux, path string, admin, auth http.Handler, provider func() model.PublicSiteInfo, protect ...func(http.Handler) http.Handler) {
	mux.Handle("/ui/", SharedStyles())
	mux.Handle("/admin/v1/", admin)
	mux.Handle("/auth/", auth)
	mux.Handle("/test/v1/", auth)

	var console http.Handler = ConsoleWithSite(path, provider)
	for _, guard := range protect {
		console = guard(console)
	}
	mux.Handle(path+"/", console)
	mux.Handle(path, console)
}
