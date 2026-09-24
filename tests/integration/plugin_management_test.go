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
	"testing"

	adminapi "api-manager/internal/api"
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/store"
)

func TestPluginManagementHTTPFlow(t *testing.T) {
	memory := store.NewMemory()
	registry := plugin.NewRegistry()
	defer registry.Close(context.Background())
	manager := plugin.NewManager(memory, registry, t.TempDir(), 1<<20, nil)
	admin := adminapi.NewAdminWithUserAuthAndPluginManager(memory, registry, "root-token", nil, manager, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	manifestBytes := []byte("name: http-plugin\nversion: 1.0.0\nruntime: wasm\nentrypoint: plugin.wasm\n")
	manifest, err := writer.CreateFormFile("manifest", "manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = manifest.Write(manifestBytes)
	wasm, err := writer.CreateFormFile("wasm", "plugin.wasm")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = wasm.Write(minimalWASMPlugin())
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	upload := httptest.NewRequest(http.MethodPost, "/admin/v1/plugins", &body)
	upload.Header.Set("X-Admin-Token", "root-token")
	upload.Header.Set("Content-Type", writer.FormDataContentType())
	uploadResponse := httptest.NewRecorder()
	admin.ServeHTTP(uploadResponse, upload)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload returned %d: %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var item model.Plugin
	if err := json.Unmarshal(uploadResponse.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.Enabled || item.StoragePath != "" {
		t.Fatalf("unexpected upload response: %#v", item)
	}
	enable := httptest.NewRequest(http.MethodPut, "/admin/v1/plugins/"+item.ID+"/status", bytes.NewBufferString(`{"enabled":true}`))
	enable.Header.Set("X-Admin-Token", "root-token")
	enableResponse := httptest.NewRecorder()
	admin.ServeHTTP(enableResponse, enable)
	if enableResponse.Code != http.StatusOK {
		t.Fatalf("enable returned %d: %s", enableResponse.Code, enableResponse.Body.String())
	}
	if _, ok := registry.Get("http-plugin"); !ok {
		t.Fatal("enabled plugin not available in registry")
	}
	disable := httptest.NewRequest(http.MethodPut, "/admin/v1/plugins/"+item.ID+"/status", bytes.NewBufferString(`{"enabled":false}`))
	disable.Header.Set("X-Admin-Token", "root-token")
	disableResponse := httptest.NewRecorder()
	admin.ServeHTTP(disableResponse, disable)
	if disableResponse.Code != http.StatusOK {
		t.Fatalf("disable returned %d: %s", disableResponse.Code, disableResponse.Body.String())
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/admin/v1/plugins/"+item.ID, nil)
	deleteRequest.Header.Set("X-Admin-Token", "root-token")
	deleteResponse := httptest.NewRecorder()
	admin.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete returned %d: %s", deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestPluginLibraryHTTPFlow(t *testing.T) {
	memory := store.NewMemory()
	registry := plugin.NewRegistry()
	defer registry.Close(context.Background())
	manager := plugin.NewManager(memory, registry, t.TempDir(), 1<<20, nil)
	library := plugin.NewLibrary(t.TempDir(), manager)
	admin := adminapi.NewAdminWithUserAuthAndPluginManager(memory, registry, "root-token", nil, manager, slog.New(slog.NewTextHandler(io.Discard, nil)))
	admin.SetPluginLibrary(library)
	manifest := []byte("name: library-http\nversion: 1.0.0\nruntime: wasm\nentrypoint: plugin.wasm\n")
	item, err := manager.Upload(context.Background(), manifest, minimalWASMPlugin())
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, url, body string, token bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, url, bytes.NewBufferString(body))
		if token {
			req.Header.Set("X-Admin-Token", "root-token")
		}
		response := httptest.NewRecorder()
		admin.ServeHTTP(response, req)
		return response
	}
	if got := request(http.MethodPost, "/admin/v1/plugin-library", `{"plugin_id":"`+item.ID+`"}`, false); got.Code != http.StatusUnauthorized {
		t.Fatalf("publish unauthorized: %d", got.Code)
	}
	if got := request(http.MethodPost, "/admin/v1/plugin-library", `{"plugin_id":"`+item.ID+`"}`, true); got.Code != http.StatusCreated {
		t.Fatalf("publish: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, "/admin/v1/plugin-library", "", true); got.Code != http.StatusOK || !bytes.Contains(got.Body.Bytes(), []byte(`"name":"library-http"`)) {
		t.Fatalf("list: %d %s", got.Code, got.Body.String())
	}
	if _, err := manager.Delete(item.ID); err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodPost, "/admin/v1/plugin-library/install?name=library-http&version=1.0.0", "", true); got.Code != http.StatusCreated {
		t.Fatalf("install: %d %s", got.Code, got.Body.String())
	}
}

func minimalWASMPlugin() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x0c, 0x02, 0x60, 0x01, 0x7f, 0x01, 0x7f, 0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7e,
		0x03, 0x03, 0x02, 0x00, 0x01,
		0x05, 0x03, 0x01, 0x00, 0x01,
		0x07, 0x1b, 0x03, 0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00, 0x05, 'a', 'l', 'l', 'o', 'c', 0x00, 0x00, 0x06, 'h', 'a', 'n', 'd', 'l', 'e', 0x00, 0x01,
		0x0a, 0x0b, 0x02, 0x04, 0x00, 0x41, 0x00, 0x0b, 0x04, 0x00, 0x42, 0x00, 0x0b,
	}
}
