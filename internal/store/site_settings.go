package store

import (
	"api-manager/internal/model"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
)

func (m *Memory) GetSiteSettings() (model.SiteSettingsRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.siteSettings.Version == 0 {
		return model.SiteSettingsRecord{}, ErrNotFound
	}
	return m.siteSettings, nil
}
func (m *Memory) SaveSiteSettings(record model.SiteSettingsRecord, expected int64) (model.SiteSettingsRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if expected != m.siteSettings.Version {
		return model.SiteSettingsRecord{}, ErrConflict
	}
	record.Version = expected + 1
	m.siteSettings = record
	return record, nil
}
func (p *Postgres) GetSiteSettings() (model.SiteSettingsRecord, error) {
	ctx, cancel := dbContext()
	defer cancel()
	var r model.SiteSettingsRecord
	var data []byte
	err := p.pool.QueryRow(ctx, `SELECT version,settings,encrypted_smtp_password,updated_at FROM site_settings WHERE id=1`).Scan(&r.Version, &data, &r.EncryptedSMTPPassword, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(data, &r.Settings); err != nil {
		return model.SiteSettingsRecord{}, err
	}
	return r, nil
}
func (p *Postgres) SaveSiteSettings(r model.SiteSettingsRecord, expected int64) (model.SiteSettingsRecord, error) {
	ctx, cancel := dbContext()
	defer cancel()
	data, err := json.Marshal(r.Settings)
	if err != nil {
		return r, err
	}
	var version int64
	if expected == 0 {
		err = p.pool.QueryRow(ctx, `INSERT INTO site_settings(id,version,settings,encrypted_smtp_password,updated_at) VALUES(1,1,$1,$2,$3) ON CONFLICT(id) DO NOTHING RETURNING version`, data, r.EncryptedSMTPPassword, r.UpdatedAt).Scan(&version)
	} else {
		err = p.pool.QueryRow(ctx, `UPDATE site_settings SET version=version+1,settings=$2,encrypted_smtp_password=$3,updated_at=$4 WHERE id=1 AND version=$1 RETURNING version`, expected, data, r.EncryptedSMTPPassword, r.UpdatedAt).Scan(&version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return model.SiteSettingsRecord{}, ErrConflict
	}
	if err != nil {
		return model.SiteSettingsRecord{}, err
	}
	r.Version = version
	return r, nil
}
