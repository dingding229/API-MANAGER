package store

import (
	"api-manager/internal/model"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
)

type PluginRuntimeStore interface {
	PluginRuntimePolicy(context.Context, string) (model.PluginRuntimePolicy, error)
	SavePluginRuntimePolicy(context.Context, string, model.PluginRuntimePolicy, int64) (int64, error)
	PluginSession(context.Context, string, string) (model.PluginSession, error)
	SavePluginSession(context.Context, model.PluginSession, int64, int, int64) (int64, error)
	DeletePluginSession(context.Context, string, string, int64) error
}

func (p *Postgres) PluginRuntimePolicy(ctx context.Context, id string) (policy model.PluginRuntimePolicy, e error) {
	var raw []byte
	e = p.pool.QueryRow(ctx, `SELECT policy,version FROM plugin_runtime_policies WHERE plugin_id=$1`, id).Scan(&raw, &policy.Version)
	if errors.Is(e, pgx.ErrNoRows) {
		return model.PluginRuntimePolicy{}, nil
	}
	if e != nil {
		return policy, e
	}
	version := policy.Version
	e = json.Unmarshal(raw, &policy)
	policy.Version = version
	return
}
func (p *Postgres) SavePluginRuntimePolicy(ctx context.Context, id string, policy model.PluginRuntimePolicy, expected int64) (int64, error) {
	raw, e := json.Marshal(policy)
	if e != nil {
		return 0, e
	}
	var version int64
	if expected == 0 {
		e = p.pool.QueryRow(ctx, `INSERT INTO plugin_runtime_policies(plugin_id,policy) VALUES($1,$2) ON CONFLICT DO NOTHING RETURNING version`, id, raw).Scan(&version)
	} else {
		e = p.pool.QueryRow(ctx, `UPDATE plugin_runtime_policies SET policy=$2,version=version+1,updated_at=NOW() WHERE plugin_id=$1 AND version=$3 RETURNING version`, id, raw, expected).Scan(&version)
	}
	if errors.Is(e, pgx.ErrNoRows) {
		e = ErrConflict
	}
	return version, e
}
func (p *Postgres) PluginSession(ctx context.Context, id, key string) (v model.PluginSession, e error) {
	v.PluginID = id
	v.KeyHash = key
	e = p.pool.QueryRow(ctx, `SELECT encrypted_value,version,expires_at FROM plugin_sessions WHERE plugin_id=$1 AND key_hash=$2 AND (expires_at IS NULL OR expires_at>NOW())`, id, key).Scan(&v.EncryptedValue, &v.Version, &v.ExpiresAt)
	if errors.Is(e, pgx.ErrNoRows) {
		e = ErrNotFound
	}
	return
}
func (p *Postgres) SavePluginSession(ctx context.Context, v model.PluginSession, expected int64, limit int, policyVersion int64) (int64, error) {
	tx, e := p.pool.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	var current int64
	var enabled bool
	e = tx.QueryRow(ctx, `SELECT version,COALESCE((policy->>'session_persistence')::boolean,false) FROM plugin_runtime_policies WHERE plugin_id=$1 FOR UPDATE`, v.PluginID).Scan(&current, &enabled)
	if e != nil || current != policyVersion || !enabled {
		return 0, ErrConflict
	}
	if _, e = tx.Exec(ctx, `DELETE FROM plugin_sessions WHERE plugin_id=$1 AND expires_at<=NOW()`, v.PluginID); e != nil {
		return 0, e
	}
	var existing int64
	e = tx.QueryRow(ctx, `SELECT version FROM plugin_sessions WHERE plugin_id=$1 AND key_hash=$2 FOR UPDATE`, v.PluginID, v.KeyHash).Scan(&existing)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return 0, e
	}
	if existing != expected {
		return 0, ErrConflict
	}
	if existing == 0 {
		var count int
		if e = tx.QueryRow(ctx, `SELECT COUNT(*) FROM plugin_sessions WHERE plugin_id=$1`, v.PluginID).Scan(&count); e != nil {
			return 0, e
		}
		if count >= limit {
			return 0, ErrConflict
		}
	}
	var version int64
	e = tx.QueryRow(ctx, `INSERT INTO plugin_sessions(plugin_id,key_hash,encrypted_value,version,expires_at) VALUES($1,$2,$3,nextval('plugin_session_version_seq'),$4) ON CONFLICT(plugin_id,key_hash) DO UPDATE SET encrypted_value=EXCLUDED.encrypted_value,version=EXCLUDED.version,expires_at=EXCLUDED.expires_at,updated_at=NOW() RETURNING version`, v.PluginID, v.KeyHash, v.EncryptedValue, v.ExpiresAt).Scan(&version)
	if e != nil {
		return 0, e
	}
	return version, tx.Commit(ctx)
}
func (p *Postgres) DeletePluginSession(ctx context.Context, id, key string, expected int64) error {
	result, e := p.pool.Exec(ctx, `DELETE FROM plugin_sessions WHERE plugin_id=$1 AND key_hash=$2 AND version=$3`, id, key, expected)
	if e == nil && result.RowsAffected() != 1 {
		return ErrConflict
	}
	return e
}
