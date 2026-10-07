package plugin

import (
	"api-manager/internal/auth"
	"api-manager/internal/schema"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

type settingsStore interface {
	PluginSettings(context.Context, string) (string, int64, error)
	SavePluginSettings(context.Context, string, string, int64) (int64, error)
}
type settingSnapshot struct {
	Data    map[string]any
	Version int64
	ID      string
}

var settingsMu sync.RWMutex

// Snapshots are immutable. Secrets are delivered only to the named plugin, never public metadata.
func (r *Registry) setSettings(name, id string, data map[string]any, version int64) {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	if r.settings == nil {
		r.settings = map[string]settingSnapshot{}
	}
	r.settings[name] = settingSnapshot{Data: data, Version: version, ID: id}
}
func (r *Registry) getSettings(name string) settingSnapshot {
	if r == nil {
		return settingSnapshot{}
	}
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return r.settings[name]
}
func safeSettings(value any, depth int) bool {
	if depth > 16 {
		return false
	}
	switch v := value.(type) {
	case map[string]any:
		for k, x := range v {
			if k == "__proto__" || k == "constructor" || k == "prototype" || !safeSettings(x, depth+1) {
				return false
			}
		}
	case []any:
		for _, x := range v {
			if !safeSettings(x, depth+1) {
				return false
			}
		}
	}
	return true
}
func settingsSchema(manifest Manifest) json.RawMessage {
	data, _ := json.Marshal(manifest.SettingsSchema)
	return data
}
func defaults(schema map[string]any) map[string]any {
	out := map[string]any{}
	props, _ := schema["properties"].(map[string]any)
	for k, v := range props {
		p, _ := v.(map[string]any)
		if value, ok := p["default"]; ok {
			out[k] = value
		}
	}
	return out
}
func redactSettings(data map[string]any, schema map[string]any) map[string]any {
	out := map[string]any{}
	props, _ := schema["properties"].(map[string]any)
	for k, v := range data {
		p, _ := props[k].(map[string]any)
		if p["writeOnly"] == true {
			continue
		}
		out[k] = redactSettingValue(v, p)
	}
	return out
}
func mergeSettings(old, patch map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range old {
		out[k] = v
	}
	for k, v := range patch {
		if child, ok := v.(map[string]any); ok {
			prior, _ := old[k].(map[string]any)
			out[k] = mergeSettings(prior, child)
		} else {
			out[k] = v
		}
	}
	return out
}
func (m *Manager) SetSettingsKey(key string) { m.settingsKey = key }
func (m *Manager) settingsFor(ctx context.Context, id string) (Manifest, map[string]any, int64, error) {
	item, e := m.store.GetPlugin(id)
	if e != nil {
		return Manifest{}, nil, 0, e
	}
	var manifest Manifest
	raw, e := json.Marshal(item.Manifest)
	if e != nil {
		return manifest, nil, 0, e
	}
	if e = json.Unmarshal(raw, &manifest); e != nil {
		return manifest, nil, 0, e
	}
	data := defaults(manifest.SettingsSchema)
	st, ok := m.store.(settingsStore)
	if !ok {
		return manifest, data, 0, nil
	}
	encrypted, version, e := st.PluginSettings(ctx, id)
	if e != nil {
		return manifest, nil, version, e
	}
	if encrypted != "" {
		text, e := auth.DecryptSecret(m.settingsKey+":plugin-settings:"+id, encrypted)
		if e != nil {
			return manifest, nil, version, errors.New("插件配置不可解密")
		}
		if e = json.Unmarshal([]byte(text), &data); e != nil {
			return manifest, nil, version, e
		}
	}
	return manifest, data, version, nil
}
func (m *Manager) Settings(ctx context.Context, id string) (map[string]any, error) {
	manifest, data, version, e := m.settingsFor(ctx, id)
	if e != nil {
		return nil, e
	}
	secrets := []string{}
	props, _ := manifest.SettingsSchema["properties"].(map[string]any)
	for name, v := range props {
		p, _ := v.(map[string]any)
		if p["writeOnly"] == true && data[name] != nil {
			secrets = append(secrets, name)
		}
	}
	return map[string]any{"schema": manifest.SettingsSchema, "values": redactSettings(data, manifest.SettingsSchema), "version": version, "secret_fields_set": secrets}, nil
}
func (m *Manager) SaveSettings(ctx context.Context, id string, patch map[string]any, remove []string, expected int64) error {
	st, ok := m.store.(settingsStore)
	if !ok || m.settingsKey == "" {
		return errors.New("插件配置服务不可用")
	}
	manifest, old, version, e := m.settingsFor(ctx, id)
	if e != nil {
		return e
	}
	if version != expected {
		return errors.New("配置已变更，请刷新")
	}
	if len(manifest.SettingsSchema) == 0 {
		return errors.New("插件未声明设置字段")
	}
	if !safeSettings(patch, 0) {
		return errors.New("配置含不允许的字段")
	}
	data := mergeSettings(old, patch)
	for _, key := range remove {
		delete(data, key)
	}
	raw, e := json.Marshal(data)
	if e != nil || len(raw) > 32768 {
		return errors.New("插件配置不能超过 32 KiB")
	}
	if e = schema.ValidateInstance(settingsSchema(manifest), raw); e != nil {
		return errors.New("配置不符合插件字段要求")
	}
	encrypted, e := auth.EncryptSecret(m.settingsKey+":plugin-settings:"+id, string(raw))
	if e != nil {
		return e
	}
	version, e = st.SavePluginSettings(ctx, id, encrypted, expected)
	if e != nil {
		return e
	}
	item, e := m.store.GetPlugin(id)
	if e == nil && item.Enabled {
		m.registry.setSettings(item.Name, id, data, version)
	}
	return e
}
func (m *Manager) activateSettings(ctx context.Context, id string) error {
	manifest, data, version, e := m.settingsFor(ctx, id)
	if e != nil {
		return e
	}
	raw, _ := json.Marshal(data)
	if len(manifest.SettingsSchema) > 0 && schema.ValidateInstance(settingsSchema(manifest), raw) != nil {
		return fmt.Errorf("插件设置尚未填写完整")
	}
	m.registry.setSettings(manifest.Name, id, data, version)
	return nil
}

func redactSettingValue(value any, field map[string]any) any {
	if field["writeOnly"] == true {
		return nil
	}
	switch v := value.(type) {
	case map[string]any:
		return redactSettings(v, field)
	case []any:
		items, _ := field["items"].(map[string]any)
		result := []any{}
		for _, item := range v {
			result = append(result, redactSettingValue(item, items))
		}
		return result
	default:
		return value
	}
}
func validSettingsSchema(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		if v["writeOnly"] == true {
			if _, ok := v["default"]; ok {
				return false
			}
		}
		for k, x := range v {
			if k == "$ref" || k == "$dynamicRef" || !validSettingsSchema(x) {
				return false
			}
		}
	case []any:
		for _, x := range v {
			if !validSettingsSchema(x) {
				return false
			}
		}
	}
	return true
}
