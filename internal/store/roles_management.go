package store

import (
	"api-manager/internal/model"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"strings"
)

func RolePermissionsAllowed(name string, codes []string) bool {
	if name == "super_admin" {
		return len(codes) == 1 && codes[0] == "*"
	}
	if !SupportedRole(name) {
		return false
	}
	for _, code := range codes {
		if name == "member" {
			if code != "api.test" {
				return false
			}
			continue
		}
		if !strings.HasPrefix(code, "api.") && !strings.HasPrefix(code, "plugin.") && code != "observability.read" {
			return false
		}
	}
	return true
}
func (p *Postgres) SaveRoleDetails(ctx context.Context, role model.Role) error {
	if !RolePermissionsAllowed(role.Name, role.Permissions) {
		return ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id string
	if err = tx.QueryRow(ctx, `SELECT id FROM roles WHERE name=$1 AND tenant_id IS NULL AND deleted_at IS NULL FOR UPDATE`, role.Name).Scan(&id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE roles SET display_name=$2,description=$3 WHERE id=$1`, id, role.DisplayName, role.Description); err != nil {
		return err
	}
	if role.Name != "super_admin" {
		if err = setRolePermissionsTx(ctx, tx, id, role.Permissions); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (p *Postgres) DeleteRole(ctx context.Context, name string) error {
	if name == "super_admin" || !SupportedRole(name) {
		return ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(71950322)`); err != nil {
		return err
	}
	var id string
	if err = tx.QueryRow(ctx, `SELECT id FROM roles WHERE name=$1 AND tenant_id IS NULL AND deleted_at IS NULL FOR UPDATE`, name).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	var used bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_roles WHERE role_id=$1) OR EXISTS(SELECT 1 FROM users WHERE role=$2)`, id, name).Scan(&used); err != nil {
		return err
	}
	if used {
		return ErrConflict
	}
	var registration bool
	var defaultRole string
	err = tx.QueryRow(ctx, `SELECT COALESCE((settings->>'registration_enabled')::boolean,FALSE),COALESCE(settings->>'default_role','member') FROM authentication_settings WHERE id=1 FOR SHARE`).Scan(&registration, &defaultRole)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if registration && defaultRole == name {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE roles SET deleted_at=NOW() WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (m *Memory) DeleteRole(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == "super_admin" || !SupportedRole(name) {
		return ErrConflict
	}
	if _, ok := m.roles[name]; !ok {
		return ErrNotFound
	}
	for _, roles := range m.userRoles {
		for _, role := range roles {
			if role == name {
				return ErrConflict
			}
		}
	}
	for _, u := range m.users {
		if u.Role == name {
			return ErrConflict
		}
	}
	delete(m.roles, name)
	m.deletedRoles[name] = true
	return nil
}
func (m *Memory) SaveRoleDetails(ctx context.Context, role model.Role) error {
	if !RolePermissionsAllowed(role.Name, role.Permissions) {
		return ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.roles[role.Name]
	if !ok {
		return ErrNotFound
	}
	old.DisplayName = role.DisplayName
	old.Description = role.Description
	if role.Name != "super_admin" {
		old.Permissions = dedupe(role.Permissions)
	}
	m.roles[role.Name] = old
	return nil
}
