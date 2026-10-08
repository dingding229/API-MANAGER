package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"api-manager/internal/ids"
	"api-manager/internal/model"
)

const defaultMaxUploadBytes int64 = 20 << 20
const maxManifestBytes int64 = 256 << 10

// ErrInvalidPackage identifies caller-correctable plugin package validation failures.
var ErrInvalidPackage = errors.New("invalid plugin package")

var safePluginComponent = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type PluginStore interface {
	CreatePlugin(model.Plugin) error
	GetPlugin(string) (model.Plugin, error)
	ListPlugins() []model.Plugin
	SetPluginEnabled(string, bool) error
	DeletePlugin(string) error
}

type Manager struct {
	settingsKey           string
	store                 PluginStore
	registry              *Registry
	root                  string
	maxUpload             int64
	logger                *slog.Logger
	databaseWritesEnabled bool
}

type Options struct {
	DatabaseWritesEnabled bool
}

func NewManager(store PluginStore, registry *Registry, root string, maxUploadBytes int64, logger *slog.Logger) *Manager {
	return NewManagerWithOptions(store, registry, root, maxUploadBytes, logger, Options{})
}

func NewManagerWithOptions(store PluginStore, registry *Registry, root string, maxUploadBytes int64, logger *slog.Logger, options Options) *Manager {
	if maxUploadBytes <= 0 {
		maxUploadBytes = defaultMaxUploadBytes
	}
	return &Manager{store: store, registry: registry, root: root, maxUpload: maxUploadBytes, logger: logger, databaseWritesEnabled: options.DatabaseWritesEnabled}
}

func (m *Manager) MaxUploadBytes() int64   { return m.maxUpload }
func (m *Manager) MaxManifestBytes() int64 { return maxManifestBytes }
func (m *Manager) List() []model.Plugin    { return m.store.ListPlugins() }

func (m *Manager) Upload(ctx context.Context, manifestBytes, wasmBytes []byte) (model.Plugin, error) {
	return m.upload(ctx, manifestBytes, wasmBytes)
}

func (m *Manager) upload(ctx context.Context, manifestBytes, wasmBytes []byte) (model.Plugin, error) {
	if len(manifestBytes) == 0 || len(wasmBytes) == 0 {
		return model.Plugin{}, fmt.Errorf("%w: manifest.yaml and wasm module are required", ErrInvalidPackage)
	}
	if int64(len(manifestBytes)) > maxManifestBytes {
		return model.Plugin{}, fmt.Errorf("%w: plugin manifest exceeds %d byte limit", ErrInvalidPackage, maxManifestBytes)
	}
	if int64(len(manifestBytes)+len(wasmBytes)) > m.maxUpload {
		return model.Plugin{}, fmt.Errorf("%w: plugin upload exceeds %d byte limit", ErrInvalidPackage, m.maxUpload)
	}
	if strings.TrimSpace(m.root) == "" {
		return model.Plugin{}, errors.New("plugin storage directory is not configured")
	}
	if err := os.MkdirAll(m.root, 0o750); err != nil {
		return model.Plugin{}, fmt.Errorf("create plugin storage directory: %w", err)
	}
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return model.Plugin{}, fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	if !safePluginComponent.MatchString(manifest.Name) || !safePluginComponent.MatchString(manifest.Version) {
		return model.Plugin{}, fmt.Errorf("%w: plugin name and version may contain only letters, digits, dots, underscores, and hyphens", ErrInvalidPackage)
	}
	checksumBytes := sha256.Sum256(wasmBytes)
	checksum := hex.EncodeToString(checksumBytes[:])
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return model.Plugin{}, fmt.Errorf("encode plugin manifest: %w", err)
	}
	now := time.Now().UTC()
	item := model.Plugin{ID: "plugin_" + ids.NewUUID(), Name: manifest.Name, Version: manifest.Version, Runtime: manifest.Runtime, Manifest: manifestJSON, Checksum: checksum, Enabled: false, CreatedAt: now, UpdatedAt: now}
	finalPath := filepath.Join(m.root, manifest.Name, manifest.Version)
	if !isWithinRoot(m.root, finalPath) {
		return model.Plugin{}, errors.New("invalid plugin storage path")
	}
	if _, err := m.store.GetPlugin(item.ID); err == nil {
		return model.Plugin{}, errors.New("plugin ID collision")
	}
	staging, err := os.MkdirTemp(m.root, ".staging-")
	if err != nil {
		return model.Plugin{}, fmt.Errorf("create plugin staging directory: %w", err)
	}
	defer os.RemoveAll(staging)
	if err := os.WriteFile(filepath.Join(staging, "manifest.yaml"), manifestBytes, 0o600); err != nil {
		return model.Plugin{}, fmt.Errorf("write manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, manifest.Entrypoint), wasmBytes, 0o600); err != nil {
		return model.Plugin{}, fmt.Errorf("write wasm module: %w", err)
	}
	if err := validateWASMDirectory(ctx, staging); err != nil {
		return model.Plugin{}, fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o750); err != nil {
		return model.Plugin{}, fmt.Errorf("create plugin directory: %w", err)
	}
	if _, err := os.Stat(finalPath); err == nil {
		return model.Plugin{}, errors.New("plugin package already exists on disk")
	} else if !errors.Is(err, os.ErrNotExist) {
		return model.Plugin{}, err
	}
	if err := os.Rename(staging, finalPath); err != nil {
		return model.Plugin{}, fmt.Errorf("store plugin package: %w", err)
	}
	item.StoragePath = finalPath
	if err := m.store.CreatePlugin(item); err != nil {
		_ = os.RemoveAll(finalPath)
		return model.Plugin{}, err
	}
	m.log("plugin uploaded", item)
	return item, nil
}

func (m *Manager) Enable(ctx context.Context, id string) (model.Plugin, error) {
	item, err := m.store.GetPlugin(id)
	if err != nil {
		return model.Plugin{}, err
	}
	if item.Runtime != "wasm" {
		return model.Plugin{}, errors.New("unsupported plugin runtime")
	}
	if !isWithinRoot(m.root, item.StoragePath) {
		return model.Plugin{}, errors.New("plugin storage path is outside configured root")
	}
	manifestBytes, wasmBytes, err := m.verifiedFiles(item)
	if err != nil {
		return model.Plugin{}, err
	}
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return model.Plugin{}, err
	}
	if hasCapability(manifest, "database_write") && !m.databaseWritesEnabled {
		return model.Plugin{}, errors.New("plugin requests database_write but plugin database writes are disabled")
	}
	if err := m.activateRuntime(ctx, id); err != nil {
		return model.Plugin{}, err
	}
	if err := m.activateSettings(ctx, id); err != nil {
		return model.Plugin{}, err
	}
	if err := m.registry.LoadWASMBytes(ctx, manifestBytes, wasmBytes); err != nil {
		return model.Plugin{}, fmt.Errorf("load plugin: %w", err)
	}
	if err := m.store.SetPluginEnabled(id, true); err != nil {
		m.registry.Unregister(item.Name)
		return model.Plugin{}, err
	}
	item, _ = m.store.GetPlugin(id)
	m.log("plugin enabled", item)
	return item, nil
}

func (m *Manager) Disable(id string) (model.Plugin, error) {
	item, err := m.store.GetPlugin(id)
	if err != nil {
		return model.Plugin{}, err
	}
	if err := m.store.SetPluginEnabled(id, false); err != nil {
		return model.Plugin{}, err
	}
	m.registry.Unregister(item.Name)
	item, _ = m.store.GetPlugin(id)
	m.log("plugin disabled", item)
	return item, nil
}

func (m *Manager) Delete(id string) (model.Plugin, error) {
	item, err := m.store.GetPlugin(id)
	if err != nil {
		return model.Plugin{}, err
	}
	if item.Enabled {
		return model.Plugin{}, errors.New("disable the plugin before deleting it")
	}
	if err := m.store.DeletePlugin(id); err != nil {
		return model.Plugin{}, err
	}
	if isWithinRoot(m.root, item.StoragePath) {
		if err := os.RemoveAll(item.StoragePath); err != nil {
			return model.Plugin{}, fmt.Errorf("remove plugin package: %w", err)
		}
	}
	m.log("plugin deleted", item)
	return item, nil
}

func (m *Manager) LoadEnabled(ctx context.Context) error {
	var failures []string
	for _, item := range m.store.ListPlugins() {
		if !item.Enabled {
			continue
		}
		manifestBytes, wasmBytes, err := m.verifiedFiles(item)
		if err == nil {
			manifest, parseErr := ParseManifest(manifestBytes)
			if parseErr != nil {
				err = parseErr
			} else if hasCapability(manifest, "database_write") && !m.databaseWritesEnabled {
				err = errors.New("plugin requests database_write but plugin database writes are disabled")
			}
			if err == nil {
				if err = m.activateRuntime(ctx, item.ID); err == nil {
					err = m.activateSettings(ctx, item.ID)
				}
				if err == nil {
					err = m.registry.LoadWASMBytes(ctx, manifestBytes, wasmBytes)
				}
			}
		}
		if err != nil {
			m.registry.Unregister(item.Name)
			if disableErr := m.store.SetPluginEnabled(item.ID, false); disableErr != nil {
				failures = append(failures, item.ID+": "+err.Error()+"; failed to disable: "+disableErr.Error())
			} else {
				failures = append(failures, item.ID+": "+err.Error())
			}
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func hasCapability(manifest Manifest, capability string) bool {
	for _, value := range manifest.Capabilities {
		if value == capability {
			return true
		}
	}
	return false
}

func (m *Manager) verifiedFiles(item model.Plugin) ([]byte, []byte, error) {
	if err := m.verifyStorageDirectory(item.StoragePath); err != nil {
		return nil, nil, err
	}
	manifestBytes, err := readManagedPluginFile(item.StoragePath, "manifest.yaml", maxManifestBytes)
	if err != nil {
		return nil, nil, err
	}
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return nil, nil, err
	}
	if manifest.Name != item.Name || manifest.Version != item.Version {
		return nil, nil, errors.New("plugin manifest identity changed")
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, nil, err
	}
	if !jsonEqual(encoded, item.Manifest) {
		return nil, nil, errors.New("plugin manifest differs from stored record")
	}
	wasmBytes, err := readManagedPluginFile(item.StoragePath, manifest.Entrypoint, m.maxUpload)
	if err != nil {
		return nil, nil, err
	}
	checksum := sha256.Sum256(wasmBytes)
	if hex.EncodeToString(checksum[:]) != item.Checksum {
		return nil, nil, errors.New("plugin binary checksum changed")
	}
	return manifestBytes, wasmBytes, nil
}

func (m *Manager) verifyStorageDirectory(path string) error {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(m.root) == "" {
		return errors.New("plugin storage path is outside configured root")
	}
	root, err := filepath.EvalSymlinks(m.root)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if !isWithinRoot(root, resolved) || resolved == root {
		return errors.New("plugin storage path is outside configured root")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("plugin storage path is not a regular directory")
	}
	return nil
}

func readManagedPluginFile(directory, name string, max int64) ([]byte, error) {
	if max <= 0 || filepath.Base(name) != name {
		return nil, errors.New("invalid plugin file request")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > max {
		return nil, errors.New("plugin package file is invalid or too large")
	}
	data, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errors.New("plugin package file is too large")
	}
	return data, nil
}

func jsonEqual(a, b []byte) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	encodedLeft, _ := json.Marshal(left)
	encodedRight, _ := json.Marshal(right)
	return string(encodedLeft) == string(encodedRight)
}

func validateWASMDirectory(ctx context.Context, directory string) error {
	registry := NewRegistry()
	defer registry.Close(context.Background())
	if err := registry.LoadWASMDirectory(ctx, directory); err != nil {
		return fmt.Errorf("validate WASM plugin: %w", err)
	}
	return nil
}

func isWithinRoot(root, target string) bool {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absoluteTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(absoluteRoot, absoluteTarget)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func (m *Manager) log(message string, item model.Plugin) {
	if m.logger != nil {
		m.logger.Info(message, "plugin_id", item.ID, "plugin", item.Name, "version", item.Version)
	}
}
