package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfigurableAdminRouting(t *testing.T) {
	for _, base := range []string{"/admin", "/ops", "/staff/console"} {
		mux := http.NewServeMux()
		deny := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
		MountAdministration(mux, base, deny, deny, deny, deny)
		for _, tc := range []struct {
			path   string
			status int
		}{{base + "/", 200}, {base + "/app.js", 200}, {base + "/app.css", 200}, {"/admin/v1/apis", 401}, {base + "/grafana/", 401}} {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			if w.Code != tc.status {
				t.Fatalf("%s: %d want %d", tc.path, w.Code, tc.status)
			}
			if tc.path == base+"/" && (!strings.Contains(w.Body.String(), base+"/app.js") || strings.Contains(w.Body.String(), "__ADMIN_PATH__")) {
				t.Fatal("custom assets/base not rendered")
			}
		}
	}
}
func TestUnsafeAdminPathsAreRejected(t *testing.T) {
	for _, path := range []string{"/", "/api", "/auth/v1", "/admin/v1", "/admin/", "/admin/../api", "/admin?secret", "//admin", "/health", "/metrics", "/_next", "/admin%2f"} {
		if ValidAdminPath(path) {
			t.Fatalf("unsafe path accepted: %s", path)
		}
	}
}
