package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"api-manager/internal/model"
)

var ErrLibraryPackageExists = errors.New("library package already exists")
var ErrInvalidLibraryPackage = errors.New("invalid library package")

type LibraryEntry struct {
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Runtime     string          `json:"runtime"`
	Checksum    string          `json:"checksum"`
	Manifest    json.RawMessage `json:"manifest"`
	Installed   bool            `json:"installed"`
	InstalledID string          `json:"installed_id,omitempty"`
}

type Library struct {
	root    string
	manager *Manager
	mu      sync.Mutex
}

func NewLibrary(root string, manager *Manager) *Library {
	return &Library{root: root, manager: manager}
}
func (l *Library) packagePath(name, version string) (string, error) {
	if l == nil || l.root == "" || !safePluginComponent.MatchString(name) || !safePluginComponent.MatchString(version) {
		return "", fmt.Errorf("%w: invalid plugin library coordinates", ErrInvalidLibraryPackage)
	}
	path := filepath.Join(l.root, name, version)
	if !isWithinRoot(l.root, path) {
		return "", fmt.Errorf("%w: invalid plugin library path", ErrInvalidLibraryPackage)
	}
	return path, nil
}
func (l *Library) readPackage(name, version string) ([]byte, []byte, Manifest, error) {
	dir, err := l.packagePath(name, version)
	if err != nil {
		return nil, nil, Manifest{}, err
	}
	if err := l.verifyPackageDirectory(dir); err != nil {
		return nil, nil, Manifest{}, err
	}
	manifestBytes, err := readLibraryFile(filepath.Join(dir, "manifest.yaml"), l.manager.maxUpload)
	if err != nil {
		return nil, nil, Manifest{}, err
	}
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return nil, nil, Manifest{}, err
	}
	if manifest.Name != name || manifest.Version != version {
		return nil, nil, Manifest{}, fmt.Errorf("%w: library manifest identity mismatch", ErrInvalidLibraryPackage)
	}
	wasmBytes, err := readLibraryFile(filepath.Join(dir, manifest.Entrypoint), l.manager.maxUpload)
	if err != nil {
		return nil, nil, Manifest{}, err
	}
	if int64(len(manifestBytes)+len(wasmBytes)) > l.manager.maxUpload {
		return nil, nil, Manifest{}, fmt.Errorf("%w: library package is too large", ErrInvalidLibraryPackage)
	}
	if err := l.verifyPackageChecksum(dir, manifest, wasmBytes); err != nil {
		return nil, nil, Manifest{}, err
	}
	return manifestBytes, wasmBytes, manifest, nil
}

// verifyPackageChecksum uses a small sidecar written at publication time.
// Older packages without the sidecar remain readable for backwards compatibility.
func (l *Library) verifyPackageChecksum(dir string, manifest Manifest, wasmBytes []byte) error {
	path := filepath.Join(dir, manifest.Entrypoint+".sha256")
	data, err := readLibraryFile(path, 128)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: read WASM checksum: %v", ErrInvalidLibraryPackage, err)
	}
	expected := strings.TrimSpace(string(data))
	actualBytes := sha256.Sum256(wasmBytes)
	actual := hex.EncodeToString(actualBytes[:])
	if expected == "" || expected != actual {
		return fmt.Errorf("%w: WASM checksum mismatch", ErrInvalidLibraryPackage)
	}
	return nil
}

// List shows only well-formed local packages within the configured directory.
func (l *Library) List() ([]LibraryEntry, error) {
	if l == nil || l.manager == nil || l.root == "" {
		return []LibraryEntry{}, nil
	}
	names, err := os.ReadDir(l.root)
	if errors.Is(err, os.ErrNotExist) {
		return []LibraryEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	installed := make(map[string]model.Plugin)
	for _, item := range l.manager.List() {
		installed[item.Name+"\x00"+item.Version] = item
	}
	entries := make([]LibraryEntry, 0)
	for _, nameDir := range names {
		if !nameDir.IsDir() || !safePluginComponent.MatchString(nameDir.Name()) {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(l.root, nameDir.Name()))
		if err != nil {
			return nil, err
		}
		for _, versionDir := range versions {
			if !versionDir.IsDir() || !safePluginComponent.MatchString(versionDir.Name()) {
				continue
			}
			_, wasmBytes, manifest, err := l.readPackage(nameDir.Name(), versionDir.Name())
			if err != nil {
				continue
			}
			checksum := sha256.Sum256(wasmBytes)
			item := LibraryEntry{Name: manifest.Name, Version: manifest.Version, Runtime: manifest.Runtime, Checksum: hex.EncodeToString(checksum[:])}
			item.Manifest, _ = json.Marshal(manifest)
			if plugin, ok := installed[manifest.Name+"\x00"+manifest.Version]; ok {
				item.Installed = true
				item.InstalledID = plugin.ID
			}
			entries = append(entries, item)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Name == entries[j].Name {
			return entries[i].Version < entries[j].Version
		}
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

// Publish copies a previously verified installed package into the local library.
// This is a local operation and never performs a remote download.
func (l *Library) Publish(pluginID string) (LibraryEntry, error) {
	if l == nil || l.manager == nil {
		return LibraryEntry{}, errors.New("plugin library is disabled")
	}
	item, err := l.manager.store.GetPlugin(pluginID)
	if err != nil {
		return LibraryEntry{}, err
	}
	manifestBytes, wasmBytes, err := l.manager.verifiedFiles(item)
	if err != nil {
		return LibraryEntry{}, err
	}
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return LibraryEntry{}, err
	}
	dest, err := l.packagePath(item.Name, item.Version)
	if err != nil {
		return LibraryEntry{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.root, 0o750); err != nil {
		return LibraryEntry{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return LibraryEntry{}, err
	}
	parentInfo, err := os.Lstat(filepath.Dir(dest))
	if err != nil {
		return LibraryEntry{}, err
	}
	if !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return LibraryEntry{}, errors.New("library package parent must be a regular directory")
	}
	rootResolved, err := filepath.EvalSymlinks(l.root)
	if err != nil {
		return LibraryEntry{}, err
	}
	parentResolved, err := filepath.EvalSymlinks(filepath.Dir(dest))
	if err != nil {
		return LibraryEntry{}, err
	}
	if !isWithinRoot(rootResolved, parentResolved) || parentResolved == rootResolved {
		return LibraryEntry{}, errors.New("library package parent is outside configured root")
	}
	tmp, err := os.MkdirTemp(l.root, ".staging-")
	if err != nil {
		return LibraryEntry{}, err
	}
	defer os.RemoveAll(tmp)
	wasmChecksum := sha256.Sum256(wasmBytes)
	for path, data := range map[string][]byte{
		"manifest.yaml":                 manifestBytes,
		manifest.Entrypoint:             wasmBytes,
		manifest.Entrypoint + ".sha256": []byte(hex.EncodeToString(wasmChecksum[:]) + "\n"),
	} {
		if err := os.WriteFile(filepath.Join(tmp, path), data, 0o600); err != nil {
			return LibraryEntry{}, err
		}
	}
	if _, err := os.Stat(dest); err == nil {
		return LibraryEntry{}, ErrLibraryPackageExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return LibraryEntry{}, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return LibraryEntry{}, fmt.Errorf("publish library package: %w", err)
	}
	return LibraryEntry{Name: item.Name, Version: item.Version, Runtime: item.Runtime, Checksum: item.Checksum, Manifest: item.Manifest, Installed: true, InstalledID: item.ID}, nil
}

func (l *Library) verifyPackageDirectory(dir string) error {
	root, err := filepath.EvalSymlinks(l.root)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if !isWithinRoot(root, resolved) || resolved == root {
		return errors.New("library package path is outside configured root")
	}
	for _, path := range []string{filepath.Dir(dir), dir} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("library package directory is not a regular directory")
		}
	}
	return nil
}

func readLibraryFile(path string, max int64) ([]byte, error) {
	if max <= 0 {
		return nil, errors.New("invalid library file limit")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("library package file is not a regular file")
	}
	if info.Size() > max {
		return nil, errors.New("library package file is too large")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errors.New("library package file is too large")
	}
	return data, nil
}

func (l *Library) Install(ctx context.Context, name, version string) (model.Plugin, error) {
	if l == nil || l.manager == nil {
		return model.Plugin{}, errors.New("plugin library is disabled")
	}
	manifest, wasm, _, err := l.readPackage(name, version)
	if err != nil {
		return model.Plugin{}, err
	}
	return l.manager.Upload(ctx, manifest, wasm)
}
