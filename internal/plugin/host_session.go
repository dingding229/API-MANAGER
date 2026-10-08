package plugin

import (
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"context"
	"encoding/json"
	"errors"
	"time"
)

type cachedPluginSession struct {
	Record        model.PluginSession
	LocalExpiry   time.Time
	PolicyVersion int64
	Persistent    bool
}
type hostSessionRequest struct {
	Key        string          `json:"key"`
	Value      json.RawMessage `json:"value"`
	Version    int64           `json:"version"`
	TTLSeconds *int            `json:"ttl_seconds"`
	Persist    *bool           `json:"persist"`
	Fresh      bool            `json:"fresh"`
}

func (s *pluginRuntimeServices) session(ctx context.Context, name, op string, q hostSessionRequest) (model.PluginSessionValue, error) {
	snapshot := s.snapshot(name)
	if identity, ok := ctx.Value(hostIdentityKey{}).(string); ok && identity != snapshot.ID {
		return model.PluginSessionValue{}, errors.New("plugin runtime identity changed")
	}
	p := snapshot.Policy
	id := snapshot.ID
	if id == "" || s.key == "" || (!p.SessionCache && !p.SessionPersistence) {
		return model.PluginSessionValue{}, errors.New("session storage disabled")
	}
	if !databaseKey.MatchString(q.Key) {
		return model.PluginSessionValue{}, errors.New("invalid session key")
	}
	persist := p.SessionPersistence
	if q.Persist != nil {
		persist = *q.Persist
	}
	if persist && !p.SessionPersistence || !persist && !p.SessionCache {
		return model.PluginSessionValue{}, errors.New("requested session storage mode disabled")
	}
	key := auth.HashAPIKey(q.Key)
	cacheKey := key + ":cache"
	if persist {
		cacheKey = key + ":persistent"
	}
	result := model.PluginSessionValue{}
	s.mu.Lock()
	cached := s.sessions[id][cacheKey]
	if !cached.LocalExpiry.IsZero() && (!time.Now().Before(cached.LocalExpiry) || cached.PolicyVersion != p.Version || (cached.Persistent && !p.SessionPersistence)) {
		delete(s.sessions[id], cacheKey)
		cached = cachedPluginSession{}
	}
	s.mu.Unlock()
	if op == "session_get" {
		record := cached.Record
		if q.Fresh && persist {
			record = model.PluginSession{}
		}
		if record.Version == 0 {
			if !persist || s.store == nil {
				return result, nil
			}
			var e error
			record, e = s.store.PluginSession(ctx, id, key)
			if errors.Is(e, store.ErrNotFound) {
				return result, nil
			}
			if e != nil {
				return result, e
			}
		}
		raw, e := auth.DecryptSecret(s.key+":plugin-session:"+id+":"+key, record.EncryptedValue)
		if e != nil {
			return result, errors.New("session data unavailable")
		}
		if p.SessionCache {
			s.cacheSession(name, record, p, persist)
		}
		return model.PluginSessionValue{Value: json.RawMessage(raw), Found: true, Version: record.Version, ExpiresAt: record.ExpiresAt}, nil
	}
	if op == "session_delete" {
		if q.Version < 1 {
			return result, errors.New("session version required")
		}
		if persist && s.store != nil {
			if e := s.store.DeletePluginSession(ctx, id, key, q.Version); e != nil && !errors.Is(e, store.ErrNotFound) {
				return result, e
			}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if existing := s.sessions[id][cacheKey]; !persist && existing.Record.Version != 0 && existing.Record.Version != q.Version {
			return result, store.ErrConflict
		}
		delete(s.sessions[id], cacheKey)
		return result, nil
	}
	if op != "session_put" {
		return result, errors.New("invalid session operation")
	}
	if len(q.Value) == 0 || len(q.Value) > p.MaxValueBytes || !json.Valid(q.Value) {
		return result, errors.New("invalid or oversized session value")
	}
	var value any
	if json.Unmarshal(q.Value, &value) != nil || !safeSettings(value, 0) {
		return result, errors.New("invalid session structure")
	}

	ttl := p.SessionTTLSeconds
	if q.TTLSeconds != nil {
		ttl = *q.TTLSeconds
	}
	if ttl < 0 || ttl > p.SessionTTLSeconds || ttl == 0 && (!persist || !p.AllowNonexpiring) {
		return result, errors.New("invalid session expiry")
	}
	record := model.PluginSession{PluginID: id, KeyHash: key}
	if ttl > 0 {
		expiry := time.Now().Add(time.Duration(ttl) * time.Second)
		record.ExpiresAt = &expiry
	}
	encrypted, e := auth.EncryptSecret(s.key+":plugin-session:"+id+":"+key, string(q.Value))
	if e != nil {
		return result, e
	}
	record.EncryptedValue = encrypted
	if persist {
		if s.store == nil {
			return result, errors.New("persistent session storage unavailable")
		}
		record.Version, e = s.store.SavePluginSession(ctx, record, q.Version, p.MaxEntries, p.Version)
		if e != nil {
			return result, e
		}
	} else {
		s.mu.Lock()
		defer s.mu.Unlock()
		current := s.policies[name]
		if current.ID != id || current.Policy.Version != p.Version || !current.Policy.SessionCache {
			return result, errors.New("session permissions changed")
		}
		if s.sessions[id] == nil {
			s.sessions[id] = map[string]cachedPluginSession{}
		}
		for k, v := range s.sessions[id] {
			if !time.Now().Before(v.LocalExpiry) {
				delete(s.sessions[id], k)
			}
		}
		if !s.cacheFitsLocked(id, cacheKey, record.EncryptedValue) {
			return result, errors.New("global session cache limit reached")
		}
		old := s.sessions[id][cacheKey]
		if old.Record.Version != q.Version {
			return result, store.ErrConflict
		}
		if old.Record.Version == 0 && len(s.sessions[id]) >= p.MaxEntries {
			return result, errors.New("session entry limit reached")
		}
		if s.cacheVersion == 0 {
			s.cacheVersion = time.Now().UnixMilli() * 1024
		}
		s.cacheVersion++
		record.Version = s.cacheVersion
		s.sessions[id][cacheKey] = cachedPluginSession{Record: record, LocalExpiry: *record.ExpiresAt, PolicyVersion: p.Version}
		return model.PluginSessionValue{Found: true, Version: record.Version, ExpiresAt: record.ExpiresAt}, nil
	}
	if p.SessionCache {
		s.cacheSession(name, record, p, persist)
	}
	return model.PluginSessionValue{Found: true, Version: record.Version, ExpiresAt: record.ExpiresAt}, nil
}
func (s *pluginRuntimeServices) cacheSession(name string, record model.PluginSession, p model.PluginRuntimePolicy, persistent bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.policies[name]
	if current.ID != record.PluginID || current.Policy.Version != p.Version || !current.Policy.SessionCache {
		return
	}
	if s.sessions[record.PluginID] == nil {
		s.sessions[record.PluginID] = map[string]cachedPluginSession{}
	}
	values := s.sessions[record.PluginID]
	cacheKey := record.KeyHash + ":cache"
	if persistent {
		cacheKey = record.KeyHash + ":persistent"
	}
	for key, v := range values {
		if !time.Now().Before(v.LocalExpiry) {
			delete(values, key)
		}
	}
	if _, ok := values[cacheKey]; !ok && len(values) >= p.MaxEntries {
		return
	}
	if !s.cacheFitsLocked(record.PluginID, cacheKey, record.EncryptedValue) {
		return
	}
	expiry := time.Now().Add(time.Duration(p.SessionTTLSeconds) * time.Second)
	if record.ExpiresAt != nil && record.ExpiresAt.Before(expiry) {
		expiry = *record.ExpiresAt
	}
	values[cacheKey] = cachedPluginSession{Record: record, LocalExpiry: expiry, PolicyVersion: p.Version, Persistent: persistent}
}

// Bound process-wide cache pressure as well as each plugin's configured quota.
func (s *pluginRuntimeServices) cacheFitsLocked(id, key, value string) bool {
	bytes, count := len(value), 1
	for plugin, entries := range s.sessions {
		for k, v := range entries {
			if !time.Now().Before(v.LocalExpiry) {
				delete(entries, k)
				continue
			}
			if plugin == id && k == key {
				continue
			}
			bytes += len(v.Record.EncryptedValue) + len(k) + 128
			count++
		}
	}
	return bytes <= 64<<20 && count <= 8192
}
