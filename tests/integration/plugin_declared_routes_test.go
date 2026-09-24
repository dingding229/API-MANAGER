package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	adminapi "api-manager/internal/api"
	"api-manager/internal/auth"
	"api-manager/internal/gateway"
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/ratelimit"
	"api-manager/internal/store"
)

func TestDeclaredRoutesUploadPublishAndInvoke(t *testing.T) {
	memory := store.NewMemory()
	registry := plugin.NewRegistry()
	defer registry.Close(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := plugin.NewManager(memory, registry, t.TempDir(), 20<<20, logger)
	admin := adminapi.NewAdminWithUserAuthAndPluginManager(memory, registry, "test-admin", nil, manager, logger)
	gw := gateway.New(memory, registry, ratelimit.NewMemory(), logger)
	packageDir := filepath.Join("..", "..", "integrations", "game-discount", "wasm")
	manifestBytes, err := os.ReadFile(filepath.Join(packageDir, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	wasmBytes, err := os.ReadFile(filepath.Join(packageDir, "plugin.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	manifestPart, err := multipartWriter.CreateFormFile("manifest", "manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manifestPart.Write(manifestBytes); err != nil {
		t.Fatal(err)
	}
	wasmPart, err := multipartWriter.CreateFormFile("wasm", "plugin.wasm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wasmPart.Write(wasmBytes); err != nil {
		t.Fatal(err)
	}
	if err = multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}
	callAdmin := func(method, path string, payload []byte, contentType string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, bytes.NewReader(payload))
		request.Header.Set("X-Admin-Token", "test-admin")
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		response := httptest.NewRecorder()
		admin.ServeHTTP(response, request)
		return response
	}
	uploaded := callAdmin(http.MethodPost, "/admin/v1/plugins", body.Bytes(), multipartWriter.FormDataContentType())
	if uploaded.Code != http.StatusCreated {
		t.Fatalf("upload %d: %s", uploaded.Code, uploaded.Body.String())
	}
	var installed model.Plugin
	if err := json.Unmarshal(uploaded.Body.Bytes(), &installed); err != nil {
		t.Fatal(err)
	}
	var manifest plugin.Manifest
	if err := json.Unmarshal(installed.Manifest, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Routes) != 3 {
		t.Fatalf("expected 3 declared routes, got %d", len(manifest.Routes))
	}
	enabled := callAdmin(http.MethodPut, "/admin/v1/plugins/"+installed.ID+"/status", []byte(`{"enabled":true}`), "application/json")
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable %d: %s", enabled.Code, enabled.Body.String())
	}
	for _, route := range manifest.Routes {
		draft, err := json.Marshal(map[string]string{"name": route.Name, "method": route.Method, "path": route.Path, "auth_mode": route.AuthMode, "plugin": installed.Name})
		if err != nil {
			t.Fatal(err)
		}
		created := callAdmin(http.MethodPost, "/admin/v1/apis", draft, "application/json")
		if created.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", route.Path, created.Code, created.Body.String())
		}
		var api model.API
		if err := json.Unmarshal(created.Body.Bytes(), &api); err != nil {
			t.Fatal(err)
		}
		published := callAdmin(http.MethodPost, "/admin/v1/apis/"+api.ID+"/publish", nil, "")
		if published.Code != http.StatusOK {
			t.Fatalf("publish %s: %d %s", route.Path, published.Code, published.Body.String())
		}
	}
	if err := memory.CreateCredential(model.Credential{ID: "client", Hash: auth.HashAPIKey("test-client-key")}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path     string
		status   int
		contains string
	}{
		{"/api/game-discount-wasm/v1/status", 200, `"environment":"wasm-demo"`},
		{"/api/game-discount-wasm/v1/offers?region=HK", 200, `"wasm-demo-001"`},
		{"/api/game-discount-wasm/v1/offers/wasm-demo-001", 200, `"title":"WASM Demo Game"`},
		{"/api/game-discount-wasm/v1/offers/unknown", 404, `"NOT_FOUND"`},
		{"/api/game-discount-wasm/v1/offers?region=US", 400, `"region must be HK"`},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tc.path, nil)
			request.Header.Set("X-API-Key", "test-client-key")
			response := httptest.NewRecorder()
			gw.ServeHTTP(response, request)
			if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.contains) {
				t.Fatalf("got %d: %s", response.Code, response.Body.String())
			}
		})
	}
	unauthenticated := httptest.NewRecorder()
	gw.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/game-discount-wasm/v1/status", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status: %d", unauthenticated.Code)
	}
}
