package store

import (
	"api-manager/internal/model"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

func (m *Memory) CreateSession(session model.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for hash, s := range m.sessions {
		if !time.Now().Before(s.ExpiresAt) {
			delete(m.sessions, hash)
		}
	}
	user, ok := m.users[session.UserID]
	if !ok || user.Status != "active" {
		return ErrNotFound
	}
	if session.AuthenticatedPasswordHash != "" && (user.Username != session.AuthenticatedUsername || user.PasswordHash != session.AuthenticatedPasswordHash) {
		return ErrConflict
	}
	// Limit active sessions for a user without retaining plaintext tokens.
	count := 0
	oldestHash := ""
	var oldest time.Time
	for hash, s := range m.sessions {
		if s.UserID != session.UserID || hash == session.Hash {
			continue
		}
		count++
		if oldestHash == "" || s.ExpiresAt.Before(oldest) {
			oldestHash, oldest = hash, s.ExpiresAt
		}
	}
	if count >= 20 {
		delete(m.sessions, oldestHash)
	}
	session.AuthenticatedUsername, session.AuthenticatedPasswordHash = "", ""
	m.sessions[session.Hash] = session
	return nil
}
func (m *Memory) GetSession(hash string) (model.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[hash]
	if !ok {
		return model.Session{}, ErrNotFound
	}
	return s, nil
}
func (m *Memory) DeleteSession(hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, hash)
	return nil
}
func (p *Postgres) CreateSession(session model.Session) error {
	ctx, cancel := dbContext()
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// The same account row lock is used by profile changes and session creation.
	// Always lock the account before deleting sessions to avoid lock-order inversions.
	var status, username, passwordHash string
	if err = tx.QueryRow(ctx, `SELECT status,username,password_hash FROM users WHERE id=$1 FOR UPDATE`, session.UserID).Scan(&status, &username, &passwordHash); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if status != "active" {
		return ErrNotFound
	}
	if session.AuthenticatedPasswordHash != "" && (username != session.AuthenticatedUsername || passwordHash != session.AuthenticatedPasswordHash) {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `DELETE FROM user_sessions WHERE user_id=$1 AND expires_at <= NOW()`, session.UserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM user_sessions WHERE user_id=$1 AND key_hash IN (SELECT key_hash FROM user_sessions WHERE user_id=$1 ORDER BY expires_at DESC OFFSET 19)`, session.UserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_sessions(key_hash,user_id,expires_at) VALUES($1,$2,$3)`, session.Hash, session.UserID, session.ExpiresAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) GetSession(hash string) (model.Session, error) {
	ctx, cancel := dbContext()
	defer cancel()
	var s model.Session
	err := p.pool.QueryRow(ctx, `SELECT key_hash,user_id,expires_at FROM user_sessions WHERE key_hash=$1`, hash).Scan(&s.Hash, &s.UserID, &s.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Session{}, ErrNotFound
	}
	return s, err
}
func (p *Postgres) DeleteSession(hash string) error {
	ctx, cancel := dbContext()
	defer cancel()
	_, err := p.pool.Exec(ctx, `DELETE FROM user_sessions WHERE key_hash=$1`, hash)
	return err
}
