package plugin

import (
	"api-manager/internal/model"
	"api-manager/internal/store"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
)

type runtimeSnapshot struct {
	ID     string
	Policy model.PluginRuntimePolicy
}
type pluginRuntimeServices struct {
	store        store.PluginRuntimeStore
	key          string
	mu           sync.RWMutex
	policies     map[string]runtimeSnapshot
	sessions     map[string]map[string]cachedPluginSession
	cacheVersion int64
}

func defaultRuntimePolicy(p model.PluginRuntimePolicy) model.PluginRuntimePolicy {
	if p.TimeoutMS == 0 {
		p.TimeoutMS = 3000
	}
	if p.ResponseBytes == 0 {
		p.ResponseBytes = 256 << 10
	}
	if p.SessionTTLSeconds == 0 {
		p.SessionTTLSeconds = 3600
	}
	if p.MaxEntries == 0 {
		p.MaxEntries = 256
	}
	if p.MaxValueBytes == 0 {
		p.MaxValueBytes = 64 << 10
	}
	if p.AllowedOrigins == nil {
		p.AllowedOrigins = []string{}
	}
	return p
}
func validateRuntimePolicy(p *model.PluginRuntimePolicy) error {
	*p = defaultRuntimePolicy(*p)
	if p.TimeoutMS < 100 || p.TimeoutMS > 30000 || p.ResponseBytes < 1024 || p.ResponseBytes > 1<<20 || p.SessionTTLSeconds < 1 || p.SessionTTLSeconds > 2592000 || p.MaxEntries < 1 || p.MaxEntries > 4096 || p.MaxValueBytes < 1024 || p.MaxValueBytes > 256<<10 || int64(p.MaxEntries)*int64(p.MaxValueBytes) > 64<<20 || len(p.AllowedOrigins) > 32 {
		return errors.New("插件运行限制超出允许范围")
	}
	origins := []string{}
	seen := map[string]bool{}
	for _, raw := range p.AllowedOrigins {
		u, e := url.Parse(raw)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || len(raw) > 512 || strings.Contains(u.Hostname(), "*") {
			return errors.New("外部地址须为完整 HTTPS 来源地址，不含路径、通配符或账号信息")
		}
		origin := strings.ToLower((&url.URL{Scheme: u.Scheme, Host: u.Host}).String())
		if !seen[origin] {
			origins = append(origins, origin)
			seen[origin] = true
		}
	}
	if p.AllowNonexpiring && !p.SessionPersistence {
		return errors.New("不自动过期的会话必须同时开启数据库保存")
	}
	p.AllowedOrigins = origins
	if p.NetworkEnabled && len(origins) == 0 {
		return errors.New("开启外部网络请求时须填写允许访问的 HTTPS 来源地址")
	}
	return nil
}
func (m *Manager) configureRuntime() {
	if m.registry == nil {
		return
	}
	m.registry.mu.Lock()
	defer m.registry.mu.Unlock()
	if m.settingsKey == "" {
		return
	}
	st, _ := m.store.(store.PluginRuntimeStore)
	if m.registry.services == nil {
		m.registry.services = &pluginRuntimeServices{store: st, key: m.settingsKey, policies: map[string]runtimeSnapshot{}, sessions: map[string]map[string]cachedPluginSession{}}
	}
}
func (m *Manager) RuntimePolicy(ctx context.Context, id string) (model.PluginRuntimePolicy, error) {
	if _, e := m.store.GetPlugin(id); e != nil {
		return model.PluginRuntimePolicy{}, e
	}
	st, ok := m.store.(store.PluginRuntimeStore)
	if !ok {
		return defaultRuntimePolicy(model.PluginRuntimePolicy{}), nil
	}
	p, e := st.PluginRuntimePolicy(ctx, id)
	return defaultRuntimePolicy(p), e
}
func (m *Manager) SaveRuntimePolicy(ctx context.Context, id string, p model.PluginRuntimePolicy, expected int64) error {
	item, e := m.store.GetPlugin(id)
	if e != nil {
		return e
	}
	var manifest Manifest
	raw, _ := json.Marshal(item.Manifest)
	if json.Unmarshal(raw, &manifest) != nil {
		return errors.New("插件清单无效")
	}
	if e = validateRuntimePolicy(&p); e != nil {
		return e
	}
	if p.NetworkEnabled && !hasCapability(manifest, "network") {
		return errors.New("插件未声明 network 能力")
	}
	if (p.SessionCache || p.SessionPersistence) && !hasCapability(manifest, "session_storage") {
		return errors.New("插件未声明 session_storage 能力")
	}
	st, ok := m.store.(store.PluginRuntimeStore)
	if !ok || m.settingsKey == "" {
		return errors.New("插件运行设置不可用")
	}
	version, e := st.SavePluginRuntimePolicy(ctx, id, p, expected)
	if e != nil {
		return e
	}
	p.Version = version
	if item.Enabled {
		m.configureRuntime()
		if m.registry.services != nil {
			m.registry.services.setPolicy(item.Name, id, p)
		}
	}
	return nil
}
func (m *Manager) activateRuntime(ctx context.Context, id string) error {
	item, e := m.store.GetPlugin(id)
	if e != nil {
		return e
	}
	p, e := m.RuntimePolicy(ctx, id)
	if e != nil {
		return e
	}
	if e = validateRuntimePolicy(&p); e != nil {
		return e
	}
	m.configureRuntime()
	if m.registry.services != nil {
		m.registry.services.setPolicy(item.Name, id, p)
	}
	return nil
}
func (s *pluginRuntimeServices) setPolicy(name, id string, p model.PluginRuntimePolicy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.policies[name]
	s.policies[name] = runtimeSnapshot{ID: id, Policy: p}
	if old.ID != id || old.Policy.Version != p.Version || !p.SessionCache {
		delete(s.sessions, old.ID)
	}
}
func (s *pluginRuntimeServices) snapshot(name string) runtimeSnapshot {
	if s == nil {
		return runtimeSnapshot{Policy: defaultRuntimePolicy(model.PluginRuntimePolicy{})}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.policies[name]
}
