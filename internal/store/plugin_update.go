package store

import (
	"api-manager/internal/model"
	"context"
)

// Metadata and already-bound interface contracts switch in one transaction.
func (p *Postgres) UpdateManagedPlugin(ctx context.Context, item model.Plugin, expected string, apis []model.API) error {
	tx, e := p.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(71950320)`); e != nil {
		return e
	}
	var old string
	if e = tx.QueryRow(ctx, `SELECT checksum FROM plugins WHERE id=$1 FOR UPDATE`, item.ID).Scan(&old); e != nil {
		return e
	}
	if old != expected {
		return ErrConflict
	}
	result, e := tx.Exec(ctx, `UPDATE plugins SET version=$2,manifest=$3,checksum=$4,storage_path=$5,updated_at=$6 WHERE id=$1`, item.ID, item.Version, []byte(item.Manifest), item.Checksum, item.StoragePath, item.UpdatedAt)
	if e != nil || result.RowsAffected() != 1 {
		return ErrConflict
	}
	for _, a := range apis {
		if len(a.RequestSchema) == 0 {
			a.RequestSchema = []byte(`{}`)
		}
		if len(a.ResponseSchema) == 0 {
			a.ResponseSchema = []byte(`{}`)
		}
		if len(a.ParametersSchema) == 0 {
			a.ParametersSchema = []byte(`{}`)
		}
		result, e = tx.Exec(ctx, `UPDATE apis SET description=$2,parameters_schema=$3,request_schema=$4,response_schema=$5,updated_at=$6 WHERE id=$1 AND plugin_name=$7`, a.ID, a.Description, []byte(a.ParametersSchema), []byte(a.RequestSchema), []byte(a.ResponseSchema), item.UpdatedAt, item.Name)
		if e != nil {
			return e
		}
		if result.RowsAffected() != 1 {
			return ErrConflict
		}
	}
	return tx.Commit(ctx)
}
func (m *Memory) UpdateManagedPlugin(ctx context.Context, item model.Plugin, expected string, apis []model.API) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.plugins[item.ID]
	if !ok {
		return ErrNotFound
	}
	if old.Checksum != expected {
		return ErrConflict
	}
	for _, v := range m.plugins {
		if v.ID != item.ID && v.Name == item.Name && v.Version == item.Version {
			return ErrConflict
		}
	}
	for _, a := range apis {
		v, ok := m.apis[a.ID]
		if !ok || v.Plugin != item.Name {
			return ErrConflict
		}
	}
	m.plugins[item.ID] = clonePlugin(item)
	for _, a := range apis {
		m.apis[a.ID] = a
	}
	return nil
}
