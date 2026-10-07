package store

import (
	"context"
	"time"
)

// Logical deletion preserves mandatory financial/audit relationships and never reopens setup.
func (p *Postgres) DeleteUser(ctx context.Context, id string) error {
	tx, e := p.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = protectLastAdmin(ctx, tx, id, true); e != nil {
		return e
	}
	// Settlement must complete before account removal.
	var held int64
	if e = tx.QueryRow(ctx, `SELECT COALESCE((SELECT held_micros FROM wallets WHERE user_id=$1),0)`, id).Scan(&held); e != nil {
		return e
	}
	if held != 0 {
		return ErrConflict
	}
	result, e := tx.Exec(ctx, `UPDATE users SET status='deleted',deleted_at=NOW(),updated_at=NOW(),auth_revision=auth_revision+1 WHERE id=$1 AND deleted_at IS NULL`, id)
	if e != nil {
		return e
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	for _, q := range []string{`DELETE FROM user_sessions WHERE user_id=$1`, `DELETE FROM password_resets WHERE user_id=$1`, `DELETE FROM account_verifications WHERE user_id=$1 OR binding=$1`, `DELETE FROM external_identities WHERE user_id=$1`, `UPDATE api_credentials SET revoked=TRUE WHERE owner_user_id=$1`} {
		if _, e = tx.Exec(ctx, q, id); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func (m *Memory) DeleteUser(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok || u.Status == "deleted" {
		return ErrNotFound
	}
	if m.lastActiveAdmin(id) {
		return ErrConflict
	}
	u.Status = "deleted"
	u.AuthRevision++
	u.UpdatedAt = time.Now().UTC()
	m.users[id] = u
	for hash, s := range m.sessions {
		if s.UserID == id {
			delete(m.sessions, hash)
		}
	}
	for hash, s := range m.resets {
		if s.UserID == id {
			delete(m.resets, hash)
		}
	}
	for key, c := range m.credentials {
		if c.OwnerUserID == id {
			c.Revoked = true
			m.credentials[key] = c
		}
	}
	return nil
}
