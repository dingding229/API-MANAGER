package store

import (
	"api-manager/internal/model"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"sort"
	"time"
)

func (m *Memory) GetPluginCache(ctx context.Context, id, key string, now time.Time) (model.PluginCacheEntry, error) {
	if err := ctx.Err(); err != nil {
		return model.PluginCacheEntry{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.pluginCache[id+":"+key]
	if !ok || !now.Before(e.ExpiresAt) {
		return model.PluginCacheEntry{}, ErrNotFound
	}
	return e, nil
}
func (m *Memory) PutPluginCache(ctx context.Context, e model.PluginCacheEntry, limit int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if limit < 1 || limit > 10000 {
		return errors.New("invalid cache limit")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apis[e.APIID]
	if !ok || !a.Enabled || a.PublishedAt == nil || !a.PluginCache.Enabled || !a.UpdatedAt.Equal(e.APIUpdatedAt) {
		return ErrConflict
	}
	if m.pluginCache == nil {
		m.pluginCache = make(map[string]model.PluginCacheEntry)
	}
	for key, item := range m.pluginCache {
		if item.APIID == e.APIID && !time.Now().Before(item.ExpiresAt) {
			delete(m.pluginCache, key)
		}
	}
	m.pluginCache[e.APIID+":"+e.Key] = e
	type pair struct {
		key     string
		created time.Time
	}
	var list []pair
	for key, item := range m.pluginCache {
		if item.APIID == e.APIID {
			list = append(list, pair{key, item.CreatedAt})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].created.After(list[j].created) })
	for _, item := range list[min(limit, len(list)):] {
		delete(m.pluginCache, item.key)
	}
	return nil
}
func (m *Memory) ClearPluginCache(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apis[id]
	if !ok {
		return ErrNotFound
	}
	next := time.Now().UTC()
	if !next.After(a.UpdatedAt) {
		next = a.UpdatedAt.Add(time.Nanosecond)
	}
	a.UpdatedAt = next
	m.apis[id] = a
	for key, e := range m.pluginCache {
		if e.APIID == id {
			delete(m.pluginCache, key)
		}
	}
	return nil
}
func (m *Memory) PluginCacheStats(ctx context.Context, id string, now time.Time) (model.PluginCacheStats, error) {
	if err := ctx.Err(); err != nil {
		return model.PluginCacheStats{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var s model.PluginCacheStats
	for _, e := range m.pluginCache {
		if e.APIID == id {
			if now.Before(e.ExpiresAt) {
				s.Entries++
			} else {
				s.Expired++
			}
		}
	}
	return s, nil
}
func (m *Memory) PrunePluginCache(ctx context.Context, now time.Time, limit int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, e := range m.pluginCache {
		if limit <= 0 {
			break
		}
		if _, ok := m.apis[e.APIID]; !ok || !now.Before(e.ExpiresAt) {
			delete(m.pluginCache, key)
			limit--
		}
	}
	return nil
}
func (p *Postgres) GetPluginCache(ctx context.Context, id, key string, now time.Time) (model.PluginCacheEntry, error) {
	var e model.PluginCacheEntry
	err := p.pool.QueryRow(ctx, `SELECT api_id,cache_key,encrypted_response,api_updated_at,created_at,expires_at FROM plugin_response_cache WHERE api_id=$1 AND cache_key=$2 AND expires_at>$3`, id, key, now).Scan(&e.APIID, &e.Key, &e.Ciphertext, &e.APIUpdatedAt, &e.CreatedAt, &e.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return e, err
}
func (p *Postgres) PutPluginCache(ctx context.Context, e model.PluginCacheEntry, limit int) error {
	if limit < 1 || limit > 10000 {
		return errors.New("invalid cache limit")
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var updated time.Time
	var enabled, published, cacheEnabled bool
	err = tx.QueryRow(ctx, `SELECT updated_at,enabled,published_at IS NOT NULL,COALESCE((plugin_cache->>'enabled')::boolean,false) FROM apis WHERE id=$1 FOR UPDATE`, e.APIID).Scan(&updated, &enabled, &published, &cacheEnabled)
	if err != nil {
		return err
	}
	if !enabled || !published || !cacheEnabled || !updated.Equal(e.APIUpdatedAt) {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `DELETE FROM plugin_response_cache WHERE api_id=$1 AND expires_at<=NOW()`, e.APIID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO plugin_response_cache(api_id,cache_key,encrypted_response,api_updated_at,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(api_id,cache_key) DO UPDATE SET encrypted_response=EXCLUDED.encrypted_response,api_updated_at=EXCLUDED.api_updated_at,created_at=EXCLUDED.created_at,expires_at=EXCLUDED.expires_at`, e.APIID, e.Key, e.Ciphertext, e.APIUpdatedAt, e.CreatedAt, e.ExpiresAt); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM plugin_response_cache WHERE api_id=$1 AND cache_key IN (SELECT cache_key FROM plugin_response_cache WHERE api_id=$1 ORDER BY created_at DESC,cache_key OFFSET $2)`, e.APIID, limit); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) ClearPluginCache(ctx context.Context, id string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE apis SET updated_at=GREATEST(NOW(),updated_at+INTERVAL '1 microsecond') WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `DELETE FROM plugin_response_cache WHERE api_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) PluginCacheStats(ctx context.Context, id string, now time.Time) (model.PluginCacheStats, error) {
	var s model.PluginCacheStats
	err := p.pool.QueryRow(ctx, `SELECT COUNT(*) FILTER(WHERE expires_at>$2),COUNT(*) FILTER(WHERE expires_at<=$2) FROM plugin_response_cache WHERE api_id=$1`, id, now).Scan(&s.Entries, &s.Expired)
	return s, err
}
func (p *Postgres) PrunePluginCache(ctx context.Context, now time.Time, limit int) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM plugin_response_cache WHERE (api_id,cache_key) IN (SELECT api_id,cache_key FROM plugin_response_cache WHERE expires_at<=$1 ORDER BY expires_at LIMIT $2)`, now, min(max(limit, 0), 1000))
	return err
}
