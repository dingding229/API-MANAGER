package plugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/schema"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"golang.org/x/net/http/httpguts"
	"gopkg.in/yaml.v3"
)

// Route is documentation for a public API that the plugin author supports.
// Uploading a plugin does not automatically publish or authorize this route.
type Route struct {
	Description       string            `yaml:"description" json:"description"`
	ParametersSchema  map[string]any    `yaml:"parameters_schema" json:"parameters_schema"`
	RequestSchema     map[string]any    `yaml:"request_schema" json:"request_schema"`
	ResponseSchema    map[string]any    `yaml:"response_schema,omitempty" json:"response_schema,omitempty"`
	Name              string            `yaml:"name" json:"name"`
	Method            string            `yaml:"method" json:"method"`
	Path              string            `yaml:"path" json:"path"`
	AuthMode          string            `yaml:"auth_mode" json:"auth_mode"`
	ExamplePathParams map[string]string `yaml:"example_path_params,omitempty" json:"example_path_params,omitempty"`
	ExampleQuery      map[string]string `yaml:"example_query,omitempty" json:"example_query,omitempty"`
}

var routeParameter = regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9_]*)\}`)
var routeMethod = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true}

func validRoute(r Route) error {
	if !routeMethod[r.Method] || !strings.HasPrefix(r.Path, "/api/") || strings.Contains(r.Path, "//") || strings.ContainsAny(r.Path, "?#\\ \t\r\n") || len(r.Path) > 512 {
		return errors.New("route requires a supported uppercase method and a clean /api/ path")
	}
	if r.AuthMode != "api_key" && r.AuthMode != "none" {
		return errors.New("route auth_mode must be api_key or none")
	}
	params := make(map[string]bool)
	parts := strings.Split(r.Path, "/")
	for _, part := range parts {
		if strings.ContainsAny(part, "{}") {
			match := routeParameter.FindStringSubmatch(part)
			if match == nil || match[0] != part || params[match[1]] {
				return errors.New("route path has an invalid or duplicate parameter")
			}
			params[match[1]] = true
		}
	}
	for key, value := range r.ExamplePathParams {
		if !params[key] || value == "" || strings.ContainsAny(value, "/?#\r\n") {
			return errors.New("route example_path_params must match path placeholders and contain safe segment values")
		}
	}
	for key, value := range r.ExampleQuery {
		if key == "" || strings.ContainsAny(key, "&=#?\r\n") || strings.ContainsAny(value, "\r\n") {
			return errors.New("route example_query contains invalid key or value")
		}
	}
	return nil
}

// Manifest describes a WebAssembly plugin package. The runtime purposefully
// exposes no host filesystem or environment variables. Optional network and
// session host calls require both a declared capability and a runtime grant.
type Manifest struct {
	SettingsSchema map[string]any `yaml:"settings_schema,omitempty" json:"settings_schema,omitempty"`
	ID             string         `yaml:"id" json:"id"`
	Name           string         `yaml:"name" json:"name"`
	Version        string         `yaml:"version" json:"version"`
	APIVersion     string         `yaml:"api_version" json:"api_version"`
	Runtime        string         `yaml:"runtime" json:"runtime"`
	Entrypoint     string         `yaml:"entrypoint" json:"entrypoint"`
	Routes         []Route        `yaml:"routes,omitempty" json:"routes,omitempty"`
	Capabilities   []string       `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	Limits         struct {
		TimeoutMS int `yaml:"timeout_ms" json:"timeout_ms"`
		MemoryMB  int `yaml:"memory_mb" json:"memory_mb"`
	} `yaml:"limits" json:"limits"`
}

var ErrRequestTooLarge = errors.New("wasm request exceeds 1 MiB")

type wasmHandler struct {
	registry *Registry
	revision string
	manifest Manifest
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	timeout  time.Duration
}

type wasmRequest struct {
	Settings        map[string]any             `json:"settings,omitempty"`
	HostPermissions *model.PluginRuntimePolicy `json:"host_permissions,omitempty"`
	Method          string                     `json:"method"`
	Path            string                     `json:"path"`
	Query           map[string][]string        `json:"query"`
	Headers         map[string][]string        `json:"headers"`
	Body            string                     `json:"body_base64"`
	APIID           string                     `json:"api_id"`
}

type wasmResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body_base64"`
}

// ParseManifest validates and decodes a WASM plugin manifest.
func ParseManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse plugin manifest: %w", err)
	}
	if strings.TrimSpace(manifest.Name) == "" || manifest.Runtime != "wasm" || strings.TrimSpace(manifest.Entrypoint) == "" {
		return Manifest{}, errors.New("plugin manifest must include name, runtime: wasm, and entrypoint")
	}
	if filepath.Base(manifest.Entrypoint) != manifest.Entrypoint || !strings.HasSuffix(strings.ToLower(manifest.Entrypoint), ".wasm") {
		return Manifest{}, errors.New("plugin entrypoint must be a local .wasm filename")
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return Manifest{}, errors.New("plugin manifest version is required")
	}
	if len(manifest.SettingsSchema) > 0 {
		raw, _ := json.Marshal(manifest.SettingsSchema)
		if len(raw) > 32768 || manifest.SettingsSchema["type"] != "object" || manifest.SettingsSchema["additionalProperties"] != false || !safeSettings(manifest.SettingsSchema, 0) || !validSettingsSchema(manifest.SettingsSchema) {
			return manifest, errors.New("settings_schema must be a bounded object schema")
		}
		if err := schema.ValidateSchema(raw); err != nil {
			return manifest, err
		}
	}
	seenCapabilities := make(map[string]bool)
	for _, capability := range manifest.Capabilities {
		if capability != "database_write" && capability != "network" && capability != "session_storage" {
			return Manifest{}, fmt.Errorf("unsupported plugin capability: %s", capability)
		}
		if seenCapabilities[capability] {
			return Manifest{}, fmt.Errorf("duplicate plugin capability: %s", capability)
		}
		seenCapabilities[capability] = true
	}
	if len(manifest.Routes) > 100 {
		return Manifest{}, errors.New("plugin routes exceed 100 entries")
	}
	seen := make(map[string]bool)
	for index, route := range manifest.Routes {
		if route.AuthMode == "" {
			route.AuthMode = "api_key"
			manifest.Routes[index].AuthMode = route.AuthMode
		}
		if err := validRoute(route); err != nil {
			return Manifest{}, fmt.Errorf("invalid plugin route %q: %w", route.Path, err)
		}
		key := route.Method + " " + route.Path
		if seen[key] {
			return Manifest{}, fmt.Errorf("duplicate plugin route: %s", key)
		}
		seen[key] = true
	}
	// Legacy manifests without explicit limits receive secure defaults.
	if manifest.Limits.MemoryMB == 0 {
		manifest.Limits.MemoryMB = 64
	}
	if manifest.Limits.TimeoutMS == 0 {
		manifest.Limits.TimeoutMS = 3000
	}
	if manifest.Limits.MemoryMB < 1 || manifest.Limits.MemoryMB > 256 || manifest.Limits.TimeoutMS < 1 || manifest.Limits.TimeoutMS > 30000 {
		return Manifest{}, errors.New("plugin limits must be 1..256 MB and 1..30000 ms")
	}
	return manifest, nil
}

// LoadWASMDirectory loads one package directory containing manifest.yaml and
// plugin.wasm. The plugin ABI is intentionally small:
//
//   - exported memory named "memory"
//   - exported alloc(size i32) -> i32
//   - exported handle(request_ptr i32, request_len i32) -> i64
//
// The handle result packs the response pointer in its high 32 bits and response
// length in its low 32 bits. The response is JSON matching wasmResponse.
func (r *Registry) LoadWASMDirectory(ctx context.Context, directory string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return fmt.Errorf("open plugin directory: %w", err)
	}
	defer root.Close()
	manifestBytes, err := fs.ReadFile(root.FS(), "manifest.yaml")
	if err != nil {
		return fmt.Errorf("read plugin manifest: %w", err)
	}
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return err
	}
	wasmBytes, err := fs.ReadFile(root.FS(), manifest.Entrypoint)
	if err != nil {
		return fmt.Errorf("read wasm module: %w", err)
	}
	return r.LoadWASMBytes(ctx, manifestBytes, wasmBytes)
}

// LoadWASMBytes compiles and validates the exact plugin bytes.
func (r *Registry) LoadWASMBytes(ctx context.Context, manifestBytes, wasmBytes []byte) error {
	handler, e := r.prepareWASM(ctx, manifestBytes, wasmBytes)
	if e != nil {
		return e
	}
	r.Register(handler)
	return nil
}
func (r *Registry) prepareWASM(ctx context.Context, manifestBytes, wasmBytes []byte) (*wasmHandler, error) {
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return nil, err
	}
	runtimeConfig := wazero.NewRuntimeConfig().WithCloseOnContextDone(true)
	if r.cache != nil {
		runtimeConfig = runtimeConfig.WithCompilationCache(r.cache)
	}
	if manifest.Limits.MemoryMB > 0 {
		// #nosec G115 -- ParseManifest bounds MemoryMB to 1..256 before this conversion.
		pages64 := uint64((manifest.Limits.MemoryMB*1024*1024 + 65535) / 65536)
		if pages64 > math.MaxUint32 {
			return nil, errors.New("plugin memory limit exceeds runtime maximum")
		}
		runtimeConfig = runtimeConfig.WithMemoryLimitPages(uint32(pages64))
	}
	runtime := wazero.NewRuntimeWithConfig(ctx, runtimeConfig)
	// WASI provides clocks/random and stdio to Go reactor modules, without
	// preopened files, environment variables, or network access.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		_ = runtime.Close(ctx)
		return nil, fmt.Errorf("initialize restricted WASI: %w", err)
	}
	if err := r.instantiateHost(ctx, runtime, manifest); err != nil {
		_ = runtime.Close(ctx)
		return nil, fmt.Errorf("initialize plugin host: %w", err)
	}
	compiled, err := runtime.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = runtime.Close(ctx)
		return nil, fmt.Errorf("compile wasm module: %w", err)
	}
	if err := validateWASMABI(compiled); err != nil {
		_ = compiled.Close(ctx)
		_ = runtime.Close(ctx)
		return nil, err
	}
	timeout := time.Duration(manifest.Limits.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	sum := sha256.Sum256(append(append([]byte(nil), manifestBytes...), wasmBytes...))
	return &wasmHandler{registry: r, revision: hex.EncodeToString(sum[:]), manifest: manifest, runtime: runtime, compiled: compiled, timeout: timeout}, nil
}

func validateWASMABI(compiled wazero.CompiledModule) error {
	if _, ok := compiled.ExportedMemories()["memory"]; !ok {
		return errors.New("wasm plugin must export memory")
	}
	functions := compiled.ExportedFunctions()
	allocate, allocated := functions["alloc"]
	handle, handled := functions["handle"]
	if !allocated || !handled {
		return errors.New("wasm plugin must export alloc and handle functions")
	}
	allocParams, allocResults := allocate.ParamTypes(), allocate.ResultTypes()
	handleParams, handleResults := handle.ParamTypes(), handle.ResultTypes()
	if len(allocParams) != 1 || len(allocResults) != 1 || len(handleParams) != 2 || len(handleResults) != 1 ||
		allocParams[0] != api.ValueTypeI32 || allocResults[0] != api.ValueTypeI32 ||
		handleParams[0] != api.ValueTypeI32 || handleParams[1] != api.ValueTypeI32 || handleResults[0] != api.ValueTypeI64 {
		return errors.New("wasm plugin exports do not match the required ABI")
	}
	return nil
}

func (w *wasmHandler) Name() string { return w.manifest.Name }

func (w *wasmHandler) Handle(ctx context.Context, writer http.ResponseWriter, request *http.Request, configuredAPI model.API) error {
	ctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	body, err := io.ReadAll(io.LimitReader(request.Body, (1<<20)+1))
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	if len(body) > 1<<20 {
		return ErrRequestTooLarge
	}
	headers := request.Header.Clone()
	headers.Del("Authorization")
	headers.Del("X-API-Key")
	headers.Del("X-Passkey-Confirmation")
	auth.StripUserSessionCookies(request)
	headers.Set("Cookie", request.Header.Get("Cookie"))
	if headers.Get("Cookie") == "" {
		headers.Del("Cookie")
	}
	w.registry.mu.RLock()
	services := w.registry.services
	w.registry.mu.RUnlock()
	permissions := services.snapshot(w.Name()).Policy
	payload, err := json.Marshal(wasmRequest{HostPermissions: &permissions, Settings: w.registry.getSettings(w.Name()).Data, Method: request.Method, Path: request.URL.Path, Query: request.URL.Query(), Headers: headers, Body: base64.StdEncoding.EncodeToString(body), APIID: configuredAPI.ID})
	if err != nil {
		return fmt.Errorf("marshal wasm request: %w", err)
	}
	module, err := w.runtime.InstantiateModule(ctx, w.compiled, wazero.NewModuleConfig().WithStartFunctions("_initialize").WithStdin(bytes.NewReader(payload)))
	if err != nil {
		return fmt.Errorf("instantiate wasm module: %w", err)
	}
	defer module.Close(ctx)
	memory := module.Memory()
	if memory == nil {
		return errors.New("wasm plugin must export memory")
	}
	allocate := module.ExportedFunction("alloc")
	handle := module.ExportedFunction("handle")
	if allocate == nil || handle == nil {
		return errors.New("wasm plugin must export alloc and handle functions")
	}
	allocation, err := allocate.Call(ctx, uint64(len(payload)))
	if err != nil || len(allocation) != 1 {
		return fmt.Errorf("wasm alloc failed: %w", err)
	}
	if allocation[0] > math.MaxUint32 {
		return errors.New("wasm alloc returned an invalid pointer")
	}
	// #nosec G115 -- the explicit MaxUint32 check above makes this conversion safe.
	requestPtr := uint32(allocation[0])
	if !memory.Write(requestPtr, payload) {
		return errors.New("wasm memory write failed")
	}
	ctx = context.WithValue(ctx, hostBudgetKey{}, &guestHostBudget{})
	result, err := handle.Call(ctx, uint64(requestPtr), uint64(len(payload)))
	if err != nil || len(result) != 1 {
		return fmt.Errorf("wasm handle failed: %w", err)
	}
	responsePtr64, responseLen64 := result[0]>>32, result[0]&math.MaxUint32
	if responsePtr64 > math.MaxUint32 || responseLen64 > 2<<20 {
		return errors.New("wasm response pointer or length is invalid")
	}
	responsePtr, responseLen := uint32(responsePtr64), uint32(responseLen64)
	if responseLen > 2<<20 {
		return errors.New("wasm response exceeds 2 MiB")
	}
	responseBytes, ok := memory.Read(responsePtr, responseLen)
	if !ok {
		return errors.New("wasm response outside memory bounds")
	}
	var response wasmResponse
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		return fmt.Errorf("decode wasm response: %w", err)
	}
	if response.Status == 0 {
		response.Status = http.StatusOK
	}
	if response.Status < 200 || response.Status > 599 {
		return errors.New("wasm response has invalid HTTP status")
	}
	headerBytes := 0
	for key, values := range response.Headers {
		if !httpguts.ValidHeaderFieldName(key) || len(values) > 16 {
			return errors.New("wasm response has invalid headers")
		}
		for _, value := range values {
			if !httpguts.ValidHeaderFieldValue(value) {
				return errors.New("wasm response has invalid headers")
			}
			headerBytes += len(key) + len(value)
		}
	}
	if len(response.Headers) > 64 || headerBytes > 16<<10 {
		return errors.New("wasm response headers exceed limit")
	}
	if len(response.Body) > 2<<20 {
		return errors.New("wasm response body exceeds limit")
	}
	for key, values := range response.Headers {
		for _, value := range values {
			writer.Header().Add(key, value)
		}
	}
	decodedBody, err := base64.StdEncoding.DecodeString(response.Body)
	if err != nil {
		return fmt.Errorf("decode wasm response body: %w", err)
	}
	writer.WriteHeader(response.Status)
	_, err = writer.Write(decodedBody)
	return err
}

var _ Handler = (*wasmHandler)(nil)

func (w *wasmHandler) CacheRevision() string {
	w.registry.mu.RLock()
	services := w.registry.services
	w.registry.mu.RUnlock()
	policy := services.snapshot(w.Name()).Policy
	return w.revision + fmt.Sprint(w.registry.getSettings(w.Name()).Version) + ":" + fmt.Sprint(policy.Version)
}
