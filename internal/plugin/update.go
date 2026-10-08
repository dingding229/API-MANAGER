package plugin

import (
	"api-manager/internal/model"
	"api-manager/internal/schema"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (m *Manager) Update(ctx context.Context, id string, manifestBytes, wasmBytes []byte) (model.Plugin, error) {
	m.changeMu.Lock()
	defer m.changeMu.Unlock()
	current, e := m.store.GetPlugin(id)
	if e != nil {
		return model.Plugin{}, e
	}
	if len(manifestBytes) == 0 || int64(len(manifestBytes)) > maxManifestBytes || int64(len(manifestBytes)+len(wasmBytes)) > m.maxUpload || len(wasmBytes) == 0 {
		return current, ErrInvalidPackage
	}
	manifest, e := ParseManifest(manifestBytes)
	if e != nil {
		return current, e
	}
	if e = ValidateRequestContracts(manifest); e != nil {
		return current, e
	}
	if manifest.Name != current.Name || manifest.Version == current.Version || !safePluginComponent.MatchString(manifest.Version) {
		return current, errors.New("更新必须保留插件名称，并使用新的有效版本号")
	}
	if hasCapability(manifest, "database_write") && !m.databaseWritesEnabled {
		return current, errors.New("插件数据库写入能力未开放")
	}
	updater, ok := m.store.(interface {
		UpdateManagedPlugin(context.Context, model.Plugin, string, []model.API) error
	})
	if !ok {
		return current, errors.New("插件更新不可用")
	}
	// Reject contract removal and incompatible business settings before touching
	// the active runtime, files, credentials or plugin-owned persisted sessions.
	_, data, version, e := m.settingsFor(ctx, id)
	if e != nil {
		return current, e
	}
	data = mergeSettings(defaults(manifest.SettingsSchema), data)
	raw, _ := json.Marshal(data)
	if len(manifest.SettingsSchema) > 0 && schema.ValidateInstance(settingsSchema(manifest), raw) != nil {
		return current, errors.New("新版本设置结构与已保存设置不兼容，请先处理设置后更新")
	}
	var bound []model.API
	if st, ok := m.store.(interface{ ListAPIsChecked() ([]model.API, error) }); ok {
		all, e := st.ListAPIsChecked()
		if e != nil {
			return current, e
		}
		for _, a := range all {
			if a.Plugin != current.Name {
				continue
			}
			r, e := contractForManifest(manifest, a.Path, apiMethods(a))
			if e != nil {
				return current, fmt.Errorf("接口 %s 与新版本不兼容: %w", a.Path, e)
			}
			a.Description = r.Description
			a.ParametersSchema, _ = json.Marshal(r.ParametersSchema)
			a.RequestSchema, _ = json.Marshal(r.RequestSchema)
			a.ResponseSchema, _ = json.Marshal(r.ResponseSchema)
			a.UpdatedAt = time.Now().UTC()
			bound = append(bound, a)
		}
	} else if st, ok := m.store.(interface{ ListAPIs() []model.API }); ok {
		for _, a := range st.ListAPIs() {
			if a.Plugin != current.Name {
				continue
			}
			r, e := contractForManifest(manifest, a.Path, apiMethods(a))
			if e != nil {
				return current, e
			}
			a.Description = r.Description
			a.ParametersSchema, _ = json.Marshal(r.ParametersSchema)
			a.RequestSchema, _ = json.Marshal(r.RequestSchema)
			a.ResponseSchema, _ = json.Marshal(r.ResponseSchema)
			bound = append(bound, a)
		}
	}
	dir := filepath.Join(m.root, manifest.Name, manifest.Version)
	if !isWithinRoot(m.root, dir) {
		return current, ErrInvalidPackage
	}
	if _, e = os.Stat(dir); e == nil {
		return current, errors.New("此版本目录已存在，请使用新版本号")
	}
	staging, e := os.MkdirTemp(m.root, ".update-")
	if e != nil {
		return current, e
	}
	defer os.RemoveAll(staging)
	if e = os.WriteFile(filepath.Join(staging, "manifest.yaml"), manifestBytes, 0600); e != nil {
		return current, e
	}
	if e = os.WriteFile(filepath.Join(staging, manifest.Entrypoint), wasmBytes, 0600); e != nil {
		return current, e
	}
	if manifest.Limits.MemoryMB < 1 || manifest.Limits.MemoryMB > 256 {
		return current, ErrInvalidPackage
	}
	candidate, e := m.registry.prepareWASM(ctx, manifestBytes, wasmBytes)
	if e != nil {
		return current, e
	}
	keep := false
	defer func() {
		if !keep {
			closeHandler(candidate)
		}
	}()
	if e = os.MkdirAll(filepath.Dir(dir), 0750); e != nil {
		return current, e
	}
	if e = os.Rename(staging, dir); e != nil {
		return current, e
	}
	sum := sha256.Sum256(wasmBytes)
	next := current
	next.Version = manifest.Version
	next.Manifest, _ = json.Marshal(manifest)
	next.Checksum = hex.EncodeToString(sum[:])
	next.StoragePath = dir
	next.UpdatedAt = time.Now().UTC()
	if e = updater.UpdateManagedPlugin(ctx, next, current.Checksum, bound); e != nil {
		_ = os.RemoveAll(dir)
		return current, e
	}
	if current.Enabled {
		m.registry.setSettings(next.Name, next.ID, data, version)
		m.registry.Register(candidate)
		keep = true
	}
	m.log("plugin updated", next)
	return next, nil
}
func apiMethods(a model.API) []string {
	if len(a.Methods) > 0 {
		return a.Methods
	}
	return []string{a.Method}
}
func contractForManifest(manifest Manifest, path string, methods []string) (Route, error) {
	var found *Route
	for _, method := range methods {
		var match *Route
		for _, r := range manifest.Routes {
			if r.Path == path && r.Method == method {
				copy := r
				match = &copy
				break
			}
		}
		if match == nil {
			return Route{}, fmt.Errorf("插件未声明 %s %s", method, path)
		}
		if found == nil {
			found = match
		} else {
			a, _ := json.Marshal([]any{found.ParametersSchema, found.RequestSchema})
			b, _ := json.Marshal([]any{match.ParametersSchema, match.RequestSchema})
			if string(a) != string(b) {
				return Route{}, errors.New("同一路径的请求参数结构不一致")
			}
		}
	}
	if found == nil {
		return Route{}, errors.New("未选择请求方式")
	}
	return *found, nil
}
