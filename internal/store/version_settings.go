package store

import (
	"api-manager/internal/model"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

func (p *Postgres) VersionCheckSettings(ctx context.Context) (model.VersionCheckSettings, error) {
	var v model.VersionCheckSettings
	e := p.pool.QueryRow(ctx, `SELECT version,encrypted_token FROM version_check_settings WHERE singleton=TRUE`).Scan(&v.Version, &v.EncryptedToken)
	if errors.Is(e, pgx.ErrNoRows) {
		e = ErrNotFound
	}
	return v, e
}
func (p *Postgres) SaveVersionCheckSettings(ctx context.Context, expected int64, encrypted string) (model.VersionCheckSettings, error) {
	var v model.VersionCheckSettings
	var e error
	if expected == 0 {
		e = p.pool.QueryRow(ctx, `INSERT INTO version_check_settings(singleton,version,encrypted_token) VALUES(TRUE,1,$1) ON CONFLICT(singleton) DO NOTHING RETURNING version,encrypted_token`, encrypted).Scan(&v.Version, &v.EncryptedToken)
	} else {
		e = p.pool.QueryRow(ctx, `UPDATE version_check_settings SET version=version+1,encrypted_token=$2,updated_at=NOW() WHERE singleton=TRUE AND version=$1 RETURNING version,encrypted_token`, expected, encrypted).Scan(&v.Version, &v.EncryptedToken)
	}

	if errors.Is(e, pgx.ErrNoRows) {
		e = ErrConflict
	}
	return v, e
}
func (m *Memory) VersionCheckSettings(context.Context) (model.VersionCheckSettings, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.versionSettings.Version == 0 {
		return model.VersionCheckSettings{}, ErrNotFound
	}
	return m.versionSettings, nil
}
func (m *Memory) SaveVersionCheckSettings(_ context.Context, expected int64, encrypted string) (model.VersionCheckSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.versionSettings.Version != expected {
		return model.VersionCheckSettings{}, ErrConflict
	}
	m.versionSettings = model.VersionCheckSettings{Version: expected + 1, EncryptedToken: encrypted}
	return m.versionSettings, nil
}
