package store

import (
	"api-manager/internal/model"
	"crypto/subtle"
)

func (m *Memory) ConfigureBootstrap(hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.users) > 0 {
		m.bootstrapUsed = true
	}
	if !m.bootstrapUsed {
		m.bootstrapHash = hash
	}
	return nil
}
func (m *Memory) BootstrapAvailable(hash string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return hash != "" && !m.bootstrapUsed && len(m.users) == 0 && m.bootstrapHash == hash, nil
}
func (m *Memory) RegisterInitialAdmin(hash string, u model.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.bootstrapUsed || len(m.users) != 0 || hash == "" || subtle.ConstantTimeCompare([]byte(hash), []byte(m.bootstrapHash)) != 1 {
		return ErrConflict
	}
	u.Role = "super_admin"
	u.Roles = []string{"super_admin"}
	m.users[u.ID] = u
	m.userRoles[u.ID] = []string{"super_admin"}
	m.bootstrapUsed = true
	m.bootstrapHash = ""
	return nil
}
func (p *Postgres) ConfigureBootstrap(hash string) error {
	ctx, cancel := dbContext()
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(71950321)`); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_bootstrap(id,key_hash,consumed) VALUES(1,$1,EXISTS(SELECT 1 FROM users)) ON CONFLICT(id) DO UPDATE SET consumed=admin_bootstrap.consumed OR EXISTS(SELECT 1 FROM users),key_hash=CASE WHEN admin_bootstrap.consumed OR EXISTS(SELECT 1 FROM users) THEN '' ELSE EXCLUDED.key_hash END`, hash)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) BootstrapAvailable(hash string) (bool, error) {
	ctx, cancel := dbContext()
	defer cancel()
	var available bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_bootstrap WHERE id=1 AND NOT consumed AND key_hash=$1 AND NOT EXISTS(SELECT 1 FROM users))`, hash).Scan(&available)
	return hash != "" && available, err
}
func (p *Postgres) RegisterInitialAdmin(hash string, u model.User) error {
	ctx, cancel := dbContext()
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(71950321)`); err != nil {
		return err
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_bootstrap WHERE id=1 AND NOT consumed AND key_hash=$1 AND NOT EXISTS(SELECT 1 FROM users))`, hash).Scan(&valid); err != nil {
		return err
	}
	if !valid || hash == "" {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,username,email,password_hash,role,status,created_at,updated_at) VALUES($1,$2,NULLIF($3,''),$4,'super_admin','active',$5,$5)`, u.ID, u.Username, u.Email, u.PasswordHash, u.CreatedAt); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE name='super_admin' AND tenant_id IS NULL`, u.ID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `UPDATE admin_bootstrap SET consumed=true,key_hash='' WHERE id=1`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
