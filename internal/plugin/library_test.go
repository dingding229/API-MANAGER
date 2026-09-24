package plugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"api-manager/internal/store"
)

func TestUnsignedUploadLibraryPublishInstallAndTamper(t *testing.T) {
	memory := store.NewMemory()
	registry := NewRegistry()
	defer registry.Close(context.Background())
	manager := NewManager(memory, registry, t.TempDir(), 1<<20, nil)
	library := NewLibrary(t.TempDir(), manager)
	manifest := []byte("name: library-test\nversion: 1.0.0\nruntime: wasm\nentrypoint: plugin.wasm\n")
	wasm := validWASMModule()
	item, err := manager.Upload(context.Background(), manifest, wasm)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Enable(context.Background(), item.ID); err != nil {
		t.Fatal(err)
	}
	entry, err := library.Publish(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "library-test" || entry.InstalledID != item.ID {
		t.Fatalf("publish: %+v", entry)
	}
	entries, err := library.List()
	if err != nil || len(entries) != 1 || !entries[0].Installed {
		t.Fatalf("list: %+v %v", entries, err)
	}
	if _, err := manager.Disable(item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Delete(item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := library.Install(context.Background(), "library-test", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := library.Install(context.Background(), "../etc", "1.0.0"); err == nil {
		t.Fatal("accepted traversal")
	}
	path := filepath.Join(library.root, "library-test", "1.0.0", "plugin.wasm")
	if err := os.WriteFile(path, append(wasm, '!'), 0o600); err != nil {
		t.Fatal(err)
	}
	if entries, err := library.List(); err != nil || len(entries) != 0 {
		t.Fatalf("invalid package still listed: %+v %v", entries, err)
	}
	if _, err := library.Install(context.Background(), "library-test", "1.0.0"); err == nil {
		t.Fatal("installed tampered package")
	}
}

func TestPluginChecksumRecheckedOnEnableAndStartup(t *testing.T) {
	memory := store.NewMemory()
	registry := NewRegistry()
	defer registry.Close(context.Background())
	manager := NewManager(memory, registry, t.TempDir(), 1<<20, nil)
	manifest := []byte("name: checksum-test\nversion: 1.0.0\nruntime: wasm\nentrypoint: plugin.wasm\n")
	wasm := validWASMModule()
	item, err := manager.Upload(context.Background(), manifest, wasm)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(item.StoragePath, "plugin.wasm")
	if err := os.WriteFile(path, append(wasm, '!'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Enable(context.Background(), item.ID); err == nil {
		t.Fatal("enabled modified package")
	}
	if err := os.WriteFile(path, wasm, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Enable(context.Background(), item.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(wasm, '!'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.LoadEnabled(context.Background()); err == nil {
		t.Fatal("started with invalid package")
	}
	stored, err := memory.GetPlugin(item.ID)
	if err != nil || stored.Enabled {
		t.Fatal("tampered package not disabled")
	}
	if _, ok := registry.Get(item.Name); ok {
		t.Fatal("tampered package remains loaded")
	}
}

func TestLibraryRejectsSymlinkAndOversizeFiles(t *testing.T) {
	memory := store.NewMemory()
	registry := NewRegistry()
	defer registry.Close(context.Background())
	manager := NewManager(memory, registry, t.TempDir(), 1024, nil)
	library := NewLibrary(t.TempDir(), manager)
	manifest := []byte("name: sample\nversion: 1.0.0\nruntime: wasm\nentrypoint: plugin.wasm\n")
	wasm := validWASMModule()
	dir := filepath.Join(library.root, "sample", "1.0.0")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for file, data := range map[string][]byte{"manifest.yaml": manifest, "plugin.wasm": wasm} {
		if err := os.WriteFile(filepath.Join(dir, file), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "external.wasm")
	if err := os.WriteFile(outside, wasm, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "plugin.wasm")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "plugin.wasm")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := library.Install(context.Background(), "sample", "1.0.0"); err == nil {
		t.Fatal("installed symlinked WASM")
	}
	if err := os.Remove(filepath.Join(dir, "plugin.wasm")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.wasm"), make([]byte, 1025), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := library.Install(context.Background(), "sample", "1.0.0"); err == nil {
		t.Fatal("installed oversized WASM")
	}
}
