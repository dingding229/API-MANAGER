package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	adminapi "api-manager/internal/api"
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/store"
)

func TestConsoleEditAndCredentialRotation(t *testing.T) {
	memory := store.NewMemory()
	admin := adminapi.NewAdmin(memory, plugin.NewRegistry(), "admin-secret", slog.New(slog.NewTextHandler(io.Discard, nil)))
	admin.SetCredentialEncryptionKey("independent-test-encryption-key")
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("X-Admin-Token", "admin-secret")
		w := httptest.NewRecorder()
		admin.ServeHTTP(w, r)
		return w
	}
	created := request(http.MethodPost, "/admin/v1/apis", `{"name":"hello","method":"GET","path":"/api/hello","auth_mode":"api_key","response_body":"before"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create API: %d %s", created.Code, created.Body.String())
	}
	var api model.API
	if err := json.Unmarshal(created.Body.Bytes(), &api); err != nil {
		t.Fatal(err)
	}
	published := request(http.MethodPost, "/admin/v1/apis/"+api.ID+"/publish", "")
	if published.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", published.Code, published.Body.String())
	}
	update := request(http.MethodPut, "/admin/v1/apis/"+api.ID, `{"name":"updated","method":"GET","path":"/api/hello","auth_mode":"api_key","response_body":"after","rate_limit_per_minute":42}`)
	if update.Code != http.StatusOK {
		t.Fatalf("update API: %d %s", update.Code, update.Body.String())
	}
	fetched := request(http.MethodGet, "/admin/v1/apis/"+api.ID, "")
	if fetched.Code != http.StatusOK {
		t.Fatalf("get API: %d %s", fetched.Code, fetched.Body.String())
	}
	if err := json.Unmarshal(fetched.Body.Bytes(), &api); err != nil {
		t.Fatal(err)
	}
	if !api.Enabled || api.Name != "updated" || api.ResponseBody != "after" || api.RateLimitPerMinute != 42 {
		t.Fatalf("updated API lost fields or publication: %+v", api)
	}

	createdCredential := request(http.MethodPost, "/admin/v1/credentials", `{"name":"client"}`)
	if createdCredential.Code != http.StatusCreated {
		t.Fatalf("create credential: %d %s", createdCredential.Code, createdCredential.Body.String())
	}
	var first struct {
		Credential model.Credential `json:"credential"`
		APIKey     string           `json:"api_key"`
	}
	if err := json.Unmarshal(createdCredential.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	rotated := request(http.MethodPost, "/admin/v1/credentials/"+first.Credential.ID+"/rotate", "")
	if rotated.Code != http.StatusOK {
		t.Fatalf("rotate credential: %d %s", rotated.Code, rotated.Body.String())
	}
	var second struct {
		Credential model.Credential `json:"credential"`
		APIKey     string           `json:"api_key"`
	}
	if err := json.Unmarshal(rotated.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.APIKey == "" || first.APIKey == second.APIKey {
		t.Fatal("rotation did not issue a fresh key")
	}
	if _, ok := auth.ValidateAPIKey(memory, first.APIKey); ok {
		t.Fatal("old key remains valid")
	}
	if _, ok := auth.ValidateAPIKey(memory, second.APIKey); !ok {
		t.Fatal("new key is invalid")
	}
	listed := request(http.MethodGet, "/admin/v1/credentials", "")
	if bytes.Contains(listed.Body.Bytes(), []byte(second.APIKey)) {
		t.Fatal("list leaked the new key")
	}
	revoked := request(http.MethodPost, "/admin/v1/credentials/"+first.Credential.ID+"/revoke", "")
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke credential: %d", revoked.Code)
	}
	if got := request(http.MethodPost, "/admin/v1/credentials/"+first.Credential.ID+"/rotate", ""); got.Code != http.StatusConflict {
		t.Fatalf("rotated revoked credential: %d", got.Code)
	}
}
