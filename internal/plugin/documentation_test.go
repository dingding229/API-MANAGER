package plugin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"api-manager/internal/model"
	"api-manager/internal/store"
	"gopkg.in/yaml.v3"
)

// This fixture persists settings like the optional production settingsStore.
// Memory itself intentionally does not implement that persistence boundary.
type documentationSettingsStore struct {
	*store.Memory
	mu       sync.Mutex
	data     map[string]string
	versions map[string]int64
}

func (s *documentationSettingsStore) PluginSettings(ctx context.Context, id string) (string, int64, error) {
	if e := ctx.Err(); e != nil {
		return "", 0, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[id], s.versions[id], nil
}
func (s *documentationSettingsStore) SavePluginSettings(ctx context.Context, id, encrypted string, expected int64) (int64, error) {
	if e := ctx.Err(); e != nil {
		return 0, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.versions[id] != expected {
		return 0, store.ErrConflict
	}
	s.data[id] = encrypted
	s.versions[id]++
	return s.versions[id], nil
}

func documentationBlock(t *testing.T, source, language string, index int) string {
	t.Helper()
	blocks := regexp.MustCompile("(?s)```"+regexp.QuoteMeta(language)+"\\n(.*?)\\n```").FindAllStringSubmatch(source, -1)
	if index >= len(blocks) {
		t.Fatalf("missing %s example %d", language, index)
	}
	return blocks[index][1]
}
func pluginDocumentation(t *testing.T) string {
	t.Helper()
	data, e := os.ReadFile(filepath.Join("..", "..", "PLUGIN_DEVELOPMENT.md"))
	if e != nil {
		t.Fatal(e)
	}
	return string(data)
}
func TestPluginDevelopmentDocumentationMatchesRuntimeContract(t *testing.T) {
	source := pluginDocumentation(t)
	if !strings.HasPrefix(source, "# 插件开发规范\n") || strings.Contains(source, "type sessionAdministration") || strings.Contains(source, "func (a *Admin) manageSessions") {
		t.Fatal("unrelated backend source leaked into developer documentation")
	}
	manifest, e := ParseManifest([]byte(documentationBlock(t, source, "yaml", 0)))
	if e != nil {
		t.Fatal(e)
	}
	if manifest.Name != "hello-tools" || manifest.Version != "1.0.0" || manifest.Entrypoint != "plugin.wasm" || manifest.Limits.TimeoutMS != 3000 || manifest.Limits.MemoryMB != 64 || len(manifest.Capabilities) != 0 || len(manifest.Routes) != 1 || manifest.Routes[0].AuthMode != "api_key" {
		t.Fatalf("example manifest drifted: %+v", manifest)
	}
	program := documentationBlock(t, source, "go", 0)
	tree, e := parser.ParseFile(token.NewFileSet(), "plugin.go", program, parser.ParseComments|parser.AllErrors)
	if e != nil || tree.Name.Name != "main" {
		t.Fatal("Go example syntax", e)
	}
	for _, export := range []string{"//go:wasmexport alloc", "//go:wasmexport handle"} {
		if !strings.Contains(program, export) {
			t.Fatal("missing export", export)
		}
	}
	var request wasmRequest
	if e = json.Unmarshal([]byte(documentationBlock(t, source, "json", 0)), &request); e != nil || request.Method != "GET" || request.Query["name"][0] != "Alice" || request.Settings["prefix"] != "你好" {
		t.Fatal("request example contract", e)
	}
	var response wasmResponse
	if e = json.Unmarshal([]byte(documentationBlock(t, source, "json", 1)), &response); e != nil || response.Status != 200 {
		t.Fatal("response example contract", e)
	}
	body, e := base64.StdEncoding.DecodeString(response.Body)
	if e != nil || string(body) != `{"message":"ok"}` {
		t.Fatal("response example base64", e)
	}
	var settings struct {
		Schema map[string]any `yaml:"settings_schema"`
	}
	if e = yaml.Unmarshal([]byte(documentationBlock(t, source, "yaml", 1)), &settings); e != nil {
		t.Fatal(e)
	}
	raw, e := yaml.Marshal(Manifest{Name: "settings-doc", Version: "1.0.0", Runtime: "wasm", Entrypoint: "plugin.wasm", SettingsSchema: settings.Schema})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ParseManifest(raw); e != nil {
		t.Fatal("settings schema example", e)
	}
	for _, required := range []string{"没有为 WASM 注册任意数据库写入 host 函数", "version", "remove_fields", "plugin_cache", "Cache-Control", "GOOS=wasip1 GOARCH=wasm", "-buildmode=c-shared"} {
		if !strings.Contains(source, required) {
			t.Fatal("missing documented contract", required)
		}
	}
}

// Opt-in to the cross-compilation integration test: no compiler or fixture WASM is bundled.
func TestPluginDocumentationExampleCompilesAndRuns(t *testing.T) {
	if os.Getenv("TEST_PLUGIN_DOCS_WASM") != "1" {
		t.Skip("set TEST_PLUGIN_DOCS_WASM=1 to compile and run the documented Go example")
	}
	source := pluginDocumentation(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "plugin.go")
	if e := os.WriteFile(file, []byte(documentationBlock(t, source, "go", 0)), 0600); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-trimpath", "-o", filepath.Join(dir, "plugin.wasm"), file)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("compile documented plugin: %v\n%s", e, out)
	}
	wasm, e := os.ReadFile(filepath.Join(dir, "plugin.wasm"))
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	registry := NewRegistry()
	defer registry.Close(context.Background())
	settingsStore := &documentationSettingsStore{Memory: store.NewMemory(), data: map[string]string{}, versions: map[string]int64{}}
	manager := NewManager(settingsStore, registry, filepath.Join(dir, "installed"), 20<<20, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manager.SetSettingsKey("documentation-example-key-0123456789")
	item, e := manager.Upload(ctx, []byte(documentationBlock(t, source, "yaml", 0)), wasm)
	if e != nil {
		t.Fatal("upload and ABI verification", e)
	}
	if _, e = manager.Enable(ctx, item.ID); e != nil {
		t.Fatal("enable documented plugin", e)
	}
	invoke := func(method, path string, status int, want string) {
		t.Helper()
		handler, release, ok := registry.Acquire(item.Name)
		if !ok {
			t.Fatal("plugin not registered")
		}
		defer release()
		r := httptest.NewRequest(method, path, nil)
		w := httptest.NewRecorder()
		if e = handler.Handle(ctx, w, r, model.API{ID: "doc-example-api"}); e != nil {
			t.Fatal(e)
		}
		if w.Code != status || !bytes.Contains(w.Body.Bytes(), []byte(want)) {
			t.Fatalf("%s: status %d body %s", path, w.Code, w.Body.String())
		}
	}
	invoke(http.MethodGet, "/api/hello?name=Alice", 200, "你好，Alice")
	if e = manager.SaveSettings(ctx, item.ID, map[string]any{"prefix": "您好"}, nil, 0); e != nil {
		t.Fatal("update documented settings", e)
	}
	invoke(http.MethodGet, "/api/hello?name=Bob", 200, "您好，Bob")
	invoke(http.MethodGet, "/api/hello?name=%3Cscript%3E", 200, `\u003cscript\u003e`)
	invoke(http.MethodPost, "/api/hello", 405, "此插件仅支持 GET")
	invoke(http.MethodGet, "/api/hello?name="+strings.Repeat("x", 65), 400, "name 不能超过 64 个字符")
	invoke(http.MethodGet, "/api/hello", 200, "您好，访客")
}

func TestPluginDocumentedHostExampleCompilesAndRuns(t *testing.T) {
	if os.Getenv("TEST_PLUGIN_DOCS_WASM") != "1" {
		t.Skip("set TEST_PLUGIN_DOCS_WASM=1 to compile the documented host example")
	}
	source := pluginDocumentation(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "host-plugin.go")
	if e := os.WriteFile(file, []byte(documentationBlock(t, source, "go", 2)), 0600); e != nil {
		t.Fatal(e)
	}
	command := exec.Command("go", "build", "-buildmode=c-shared", "-o", filepath.Join(dir, "plugin.wasm"), file)
	command.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if output, e := command.CombinedOutput(); e != nil {
		t.Fatalf("documented host example did not compile: %s %v", output, e)
	}
	wasm, e := os.ReadFile(filepath.Join(dir, "plugin.wasm"))
	if e != nil {
		t.Fatal(e)
	}
	manifest := []byte(documentationBlock(t, source, "yaml", 2))
	parsed, e := ParseManifest(manifest)
	if e != nil {
		t.Fatal(e)
	}
	registry := NewRegistry()
	registry.services, _ = testHostServices()
	registry.services.setPolicy(parsed.Name, "plugin-doc-host", defaultRuntimePolicy(model.PluginRuntimePolicy{Version: 1, SessionCache: true}))
	if e = registry.LoadWASMBytes(context.Background(), manifest, wasm); e != nil {
		t.Fatal(e)
	}
	defer registry.Close(context.Background())
	handler, _ := registry.Get(parsed.Name)
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "https://example.test/api/demo", nil)
		if e = handler.Handle(context.Background(), w, r, model.API{ID: "test"}); e != nil {
			t.Fatal(e)
		}
		var result map[string]bool
		if json.Unmarshal(w.Body.Bytes(), &result) != nil || !result["storage_ok"] || !result["session_found"] || result["network_ok"] {
			t.Fatal("documented host session/CAS/network denial behavior differs", w.Body.String())
		}
	}
}
