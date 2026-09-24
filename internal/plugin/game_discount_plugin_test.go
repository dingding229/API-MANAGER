package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-manager/internal/model"
	"api-manager/internal/store"
)

func TestGameDiscountWASMUploadAndHandle(t *testing.T) {
	packageDir := filepath.Join("..", "..", "integrations", "game-discount", "wasm")
	manifest, err := os.ReadFile(filepath.Join(packageDir, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	module, err := os.ReadFile(filepath.Join(packageDir, "plugin.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	defer registry.Close(context.Background())
	manager := NewManager(store.NewMemory(), registry, t.TempDir(), 20<<20, nil)
	item, err := manager.Upload(context.Background(), manifest, module)
	if err != nil {
		t.Fatal(err)
	}
	if item.Enabled {
		t.Fatal("upload should be disabled by default")
	}
	if _, err := manager.Enable(context.Background(), item.ID); err != nil {
		t.Fatal(err)
	}
	handler, ok := registry.Get("game-discount-wasm")
	if !ok {
		t.Fatal("plugin did not register")
	}
	for _, tc := range []struct {
		path     string
		status   int
		contains string
	}{
		{"/api/wasm/v1/status", 200, `"environment":"wasm-demo"`},
		{"/api/wasm/v1/offers?region=HK", 200, `"wasm-demo-001"`},
		{"/api/wasm/v1/offers/wasm-demo-001", 200, `"title":"WASM Demo Game"`},
		{"/api/wasm/v1/offers/unknown", 404, `"NOT_FOUND"`},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		w := httptest.NewRecorder()
		if err := handler.Handle(context.Background(), w, req, model.API{ID: "test"}); err != nil {
			t.Fatal(err)
		}
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		var decoded any
		if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
	}
}
