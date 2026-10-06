package store

import (
	"api-manager/internal/model"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

func (m *Memory) IssuePasswordReset(v model.PasswordReset) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[v.UserID]
	if !ok || u.Status != "active" || u.Email != v.Email || u.Username != v.Username || u.PasswordHash != v.PasswordHash {
		return false, ErrConflict
	}
	for key, r := range m.resets {
		if !time.Now().Before(r.ExpiresAt) {
			delete(m.resets, key)
		} else if r.UserID == v.UserID {
			if time.Since(r.CreatedAt) < time.Minute {
				return false, nil
			}
			delete(m.resets, key)
		}
	}
	m.resets[v.Hash] = v
	return true, nil
}
func (m *Memory) DeletePasswordReset(hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.resets, hash)
	return nil
}
func (m *Memory) CompletePasswordReset(hash, newHash string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.resets[hash]
	if !ok || !time.Now().Before(r.ExpiresAt) {
		return "", ErrNotFound
	}
	u, ok := m.users[r.UserID]
	if !ok || u.Status != "active" || u.Email != r.Email || u.Username != r.Username || u.PasswordHash != r.PasswordHash {
		return "", ErrNotFound
	}
	u.PasswordHash = newHash
	u.UpdatedAt = time.Now().UTC()
	m.users[u.ID] = u
	for key, v := range m.resets {
		if v.UserID == u.ID {
			delete(m.resets, key)
		}
	}
	for key, v := range m.sessions {
		if v.UserID == u.ID {
			delete(m.sessions, key)
		}
	}
	return u.ID, nil
}
func (p *Postgres) IssuePasswordReset(v model.PasswordReset) (bool, error) {
	ctx, cancel := dbContext()
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var name, email, hash, status string
	err = tx.QueryRow(ctx, `SELECT username,COALESCE(email,''),password_hash,status FROM users WHERE id=$1 FOR UPDATE`, v.UserID).Scan(&name, &email, &hash, &status)
	if err != nil {
		return false, err
	}
	if status != "active" || name != v.Username || email != v.Email || hash != v.PasswordHash {
		return false, ErrConflict
	}
	var created time.Time
	err = tx.QueryRow(ctx, `SELECT created_at FROM password_resets WHERE user_id=$1`, v.UserID).Scan(&created)
	if err == nil && time.Since(created) < time.Minute {
		return false, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO password_resets(key_hash,user_id,username_snapshot,email_snapshot,password_hash_snapshot,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(user_id) DO UPDATE SET key_hash=EXCLUDED.key_hash,username_snapshot=EXCLUDED.username_snapshot,email_snapshot=EXCLUDED.email_snapshot,password_hash_snapshot=EXCLUDED.password_hash_snapshot,created_at=EXCLUDED.created_at,expires_at=EXCLUDED.expires_at`, v.Hash, v.UserID, v.Username, v.Email, v.PasswordHash, v.CreatedAt, v.ExpiresAt)
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
func (p *Postgres) DeletePasswordReset(hash string) error {
	ctx, cancel := dbContext()
	defer cancel()
	_, err := p.pool.Exec(ctx, `DELETE FROM password_resets WHERE key_hash=$1`, hash)
	return err
}
func (p *Postgres) CompletePasswordReset(hash, newHash string) (string, error) {
	return p.CompletePasswordResetVerified(hash, newHash, -1)
}
func (p *Postgres) CompletePasswordResetVerified(hash, newHash string, expectedRevision int64) (string, error) {
	ctx, cancel := dbContext()
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	// Lookup first, then lock user before reset rows: same lock order as profile edits/logins.
	var id string
	err = tx.QueryRow(ctx, `SELECT user_id FROM password_resets WHERE key_hash=$1`, hash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	var name, email, password, status string
	var revision int64
	err = tx.QueryRow(ctx, `SELECT username,COALESCE(email,''),password_hash,status,auth_revision FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&name, &email, &password, &status, &revision)
	if err != nil {
		return "", err
	}
	if expectedRevision >= 0 && expectedRevision != revision {
		return "", ErrConflict
	}
	if expectedRevision < 0 {
		var has bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM account_security WHERE user_id=$1 AND totp_secret<>'')`, id).Scan(&has); e != nil || has {
			return "", ErrConflict
		}
	}
	var r model.PasswordReset
	err = tx.QueryRow(ctx, `SELECT username_snapshot,email_snapshot,password_hash_snapshot,expires_at FROM password_resets WHERE key_hash=$1 FOR UPDATE`, hash).Scan(&r.Username, &r.Email, &r.PasswordHash, &r.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if status != "active" || name != r.Username || email != r.Email || password != r.PasswordHash || !time.Now().Before(r.ExpiresAt) {
		return "", ErrNotFound
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET password_hash=$2,auth_revision=auth_revision+1,updated_at=NOW() WHERE id=$1`, id, newHash); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM password_resets WHERE user_id=$1`, id); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM user_sessions WHERE user_id=$1`, id); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}
