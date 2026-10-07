package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

func (p *Postgres) PluginSettings(ctx context.Context, id string) (string, int64, error) {
	var encrypted string
	var version int64
	e := p.pool.QueryRow(ctx, `SELECT encrypted_settings,version FROM plugin_settings WHERE plugin_id=$1`, id).Scan(&encrypted, &version)
	if errors.Is(e, pgx.ErrNoRows) {
		return "", 0, nil
	}
	return encrypted, version, e
}
func (p *Postgres) SavePluginSettings(ctx context.Context, id, encrypted string, expected int64) (int64, error) {
	var version int64
	var e error
	if expected == 0 {
		e = p.pool.QueryRow(ctx, `INSERT INTO plugin_settings(plugin_id,encrypted_settings) VALUES($1,$2) ON CONFLICT DO NOTHING RETURNING version`, id, encrypted).Scan(&version)
	} else {
		e = p.pool.QueryRow(ctx, `UPDATE plugin_settings SET encrypted_settings=$2,version=version+1,updated_at=NOW() WHERE plugin_id=$1 AND version=$3 RETURNING version`, id, encrypted, expected).Scan(&version)
	}
	if errors.Is(e, pgx.ErrNoRows) {
		return 0, ErrConflict
	}
	return version, e
}
