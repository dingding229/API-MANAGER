package integration

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	adminapi "api-manager/internal/api"
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/store"
	"api-manager/internal/user"
)

func securityAdmin(t *testing.T) (*store.Memory, *user.Service, *adminapi.Admin) {
	t.Helper()
	s := store.NewMemory()
	users := user.NewService(s, "a-dedicated-test-signing-secret-long-enough", time.Hour)
	admin := adminapi.NewAdminWithUserAuth(s, plugin.NewRegistry(), "emergency-admin-token", users, slog.New(slog.NewTextHandler(io.Discard, nil)))
	admin.SetCredentialEncryptionKey("unrelated-credential-encryption-secret")
	return s, users, admin
}

func asUser(t *testing.T, svc *user.Service, role string) string {
	t.Helper()
	u, err := svc.Create(role+"@example.test", "password123", role)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := svc.Authenticate(u.Email, "password123")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func adminRequest(admin *adminapi.Admin, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, r)
	return w
}

func TestPublishedAPIRequiresPublishPermissionToEdit(t *testing.T) {
	s, users, admin := securityAdmin(t)
	token := asUser(t, users, "api_developer")
	now := time.Now()
	original := model.API{ID: "published", Name: "published", Method: "GET", Path: "/api/safe", AuthMode: "api_key", ResponseBody: `{}`, Enabled: true, PublishedAt: &now}
	if err := s.CreateAPI(original); err != nil {
		t.Fatal(err)
	}
	w := adminRequest(admin, "PUT", "/admin/v1/apis/published", token, `{"name":"public","method":"GET","path":"/api/safe","auth_mode":"none","response_body":"{}"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("edit live API returned %d: %s", w.Code, w.Body.String())
	}
	after, err := s.GetAPI("published")
	if err != nil || after.AuthMode != "api_key" {
		t.Fatal("unauthorized edit changed live authentication")
	}
}

func TestNonSuperCannotEscalateRolesOrRevealKey(t *testing.T) {
	s, users, admin := securityAdmin(t)
	token := asUser(t, users, "tenant_admin")
	me, err := s.GetUserByEmail("tenant_admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ method, path, body string }{
		{"POST", "/admin/v1/users", `{"email":"new@example.test","password":"password123","roles":["super_admin"]}`},
		{"PUT", "/admin/v1/users/" + me.ID + "/roles", `{"roles":["super_admin"]}`},
		{"PUT", "/admin/v1/roles/viewer/permissions", `{"permissions":["*"]}`},
	} {
		w := adminRequest(admin, test.method, test.path, token, test.body)
		if w.Code != http.StatusForbidden {
			t.Fatalf("privilege escalation %s returned %d: %s", test.path, w.Code, w.Body.String())
		}
	}
	viewer := asUser(t, users, "viewer")
	if err := s.CreateCredential(model.Credential{ID: "secret", Name: "secret", Hash: "test"}); err != nil {
		t.Fatal(err)
	}
	w := adminRequest(admin, "GET", "/admin/v1/credentials/secret/key", viewer, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer could reveal key: %d", w.Code)
	}
}

func TestAmbiguousTemplateRoutesAreRejected(t *testing.T) {
	_, users, admin := securityAdmin(t)
	token := asUser(t, users, "api_developer")
	first := adminRequest(admin, "POST", "/admin/v1/apis", token, `{"name":"public","method":"GET","path":"/api/{id}","response_body":"{}"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first route: %d %s", first.Code, first.Body.String())
	}
	second := adminRequest(admin, "POST", "/admin/v1/apis", token, `{"name":"other","method":"GET","path":"/api/{name}","response_body":"{}"}`)
	if second.Code != http.StatusConflict {
		t.Fatalf("ambiguous route: %d %s", second.Code, second.Body.String())
	}
	exact := adminRequest(admin, "POST", "/admin/v1/apis", token, `{"name":"specific","method":"GET","path":"/api/exact","response_body":"{}"}`)
	if exact.Code != http.StatusCreated {
		t.Fatalf("specific route incorrectly rejected: %d %s", exact.Code, exact.Body.String())
	}
}
