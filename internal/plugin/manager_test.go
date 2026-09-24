package plugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"api-manager/internal/store"
)

func TestManagerUploadEnableDisableAndDelete(t *testing.T) {
	root := t.TempDir()
	memory := store.NewMemory()
	registry := NewRegistry()
	defer registry.Close(context.Background())
	manager := NewManager(memory, registry, root, 1<<20, nil)
	manifest := []byte("id: test.hello\nname: hello\nversion: 1.0.0\napi_version: v1\nruntime: wasm\nentrypoint: plugin.wasm\n")
	module := validWASMModule()
	item, err := manager.Upload(context.Background(), manifest, module)
	if err != nil {
		t.Fatal(err)
	}
	if item.Enabled || item.Checksum == "" {
		t.Fatalf("unexpected uploaded item: %#v", item)
	}
	if _, err := os.Stat(filepath.Join(root, "hello", "1.0.0", "plugin.wasm")); err != nil {
		t.Fatal(err)
	}
	item, err = manager.Enable(context.Background(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !item.Enabled {
		t.Fatal("plugin should be enabled")
	}
	if _, ok := registry.Get("hello"); !ok {
		t.Fatal("enabled plugin not loaded")
	}
	item, err = manager.Disable(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Enabled {
		t.Fatal("plugin should be disabled")
	}
	if _, ok := registry.Get("hello"); ok {
		t.Fatal("disabled plugin remains loaded")
	}
	if _, err := manager.Delete(item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "hello", "1.0.0")); !os.IsNotExist(err) {
		t.Fatalf("plugin directory still exists: %v", err)
	}
}

func TestManagerRejectsInvalidWASMABI(t *testing.T) {
	manager := NewManager(store.NewMemory(), NewRegistry(), t.TempDir(), 1<<20, nil)
	manifest := []byte("name: invalid\nversion: 1.0.0\nruntime: wasm\nentrypoint: plugin.wasm\n")
	invalid := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	if _, err := manager.Upload(context.Background(), manifest, invalid); err == nil {
		t.Fatal("invalid ABI accepted")
	}
}

// validWASMModule exports the required ABI but intentionally has a minimal
// implementation; it is enough to exercise upload-time module validation.
func validWASMModule() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x0c, 0x02, 0x60, 0x01, 0x7f, 0x01, 0x7f, 0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7e,
		0x03, 0x03, 0x02, 0x00, 0x01,
		0x05, 0x03, 0x01, 0x00, 0x01,
		0x07, 0x1b, 0x03, 0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00, 0x05, 'a', 'l', 'l', 'o', 'c', 0x00, 0x00, 0x06, 'h', 'a', 'n', 'd', 'l', 'e', 0x00, 0x01,
		0x0a, 0x0b, 0x02, 0x04, 0x00, 0x41, 0x00, 0x0b, 0x04, 0x00, 0x42, 0x00, 0x0b,
	}
}
