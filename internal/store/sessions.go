package store

import (
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"errors"
	"github.com/jackc/pgx/v5"
	"sort"
	"time"
)

func (m *Memory) CreateSession(session model.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if session.ID == "" {
		session.ID = ids.NewUUID()
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	if session.LastSeenAt.IsZero() {
		session.LastSeenAt = session.CreatedAt
	}
	for hash, s := range m.sessions {
		if !time.Now().Before(s.ExpiresAt) {
			delete(m.sessions, hash)
		}
	}
	user, ok := m.users[session.UserID]
	if !ok || user.Status != "active" {
		return ErrNotFound
	}
	if session.AuthenticatedPasswordHash != "" && (user.Username != session.AuthenticatedUsername || user.PasswordHash != session.AuthenticatedPasswordHash || user.Email != session.AuthenticatedEmail || user.AuthRevision != session.AuthRevision) {
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
	session.AuthenticatedUsername, session.AuthenticatedPasswordHash, session.AuthenticatedEmail = "", "", ""
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
	if session.ID == "" {
		session.ID = ids.NewUUID()
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	if session.LastSeenAt.IsZero() {
		session.LastSeenAt = session.CreatedAt
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// The same account row lock is used by profile changes and session creation.
	// Always lock the account before deleting sessions to avoid lock-order inversions.
	var status, username, passwordHash, email string
	var revision int64
	if err = tx.QueryRow(ctx, `SELECT status,username,password_hash,COALESCE(email,''),auth_revision FROM users WHERE id=$1 FOR UPDATE`, session.UserID).Scan(&status, &username, &passwordHash, &email, &revision); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if status != "active" {
		return ErrNotFound
	}
	if session.AuthenticatedPasswordHash != "" && (username != session.AuthenticatedUsername || passwordHash != session.AuthenticatedPasswordHash || email != session.AuthenticatedEmail || revision != session.AuthRevision) {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `DELETE FROM user_sessions WHERE user_id=$1 AND expires_at <= NOW()`, session.UserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM user_sessions WHERE user_id=$1 AND key_hash IN (SELECT key_hash FROM user_sessions WHERE user_id=$1 ORDER BY expires_at DESC OFFSET 19)`, session.UserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_sessions(key_hash,user_id,expires_at,session_id,created_at,last_seen_at,login_ip,last_ip,peer_ip,ip_source,user_agent,device) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, session.Hash, session.UserID, session.ExpiresAt, session.ID, session.CreatedAt, session.LastSeenAt, session.LoginIP, session.LastIP, session.PeerIP, session.IPSource, session.UserAgent, session.Device); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) GetSession(hash string) (model.Session, error) {
	ctx, cancel := dbContext()
	defer cancel()
	var s model.Session
	err := p.pool.QueryRow(ctx, `SELECT key_hash,user_id,expires_at,session_id,created_at,last_seen_at,login_ip,last_ip,peer_ip,ip_source,user_agent,device FROM user_sessions WHERE key_hash=$1`, hash).Scan(&s.Hash, &s.UserID, &s.ExpiresAt, &s.ID, &s.CreatedAt, &s.LastSeenAt, &s.LoginIP, &s.LastIP, &s.PeerIP, &s.IPSource, &s.UserAgent, &s.Device)
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

func (m *Memory) ListUserSessions(id string, now time.Time) ([]model.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := []model.Session{}
	for _, s := range m.sessions {
		if s.UserID == id && now.Before(s.ExpiresAt) {
			list = append(list, s)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	return list, nil
}
func (m *Memory) DeleteUserSession(userID, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for hash, s := range m.sessions {
		if s.UserID == userID && s.ID == id {
			delete(m.sessions, hash)
			return true, nil
		}
	}
	return false, nil
}
func (m *Memory) TouchSession(hash string, now time.Time, ip string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[hash]
	if !ok {
		return ErrNotFound
	}
	if now.Sub(s.LastSeenAt) > time.Minute || (ip != "" && s.LastIP != ip) {
		s.LastSeenAt = now
		if ip != "" {
			s.LastIP = ip
		}
		m.sessions[hash] = s
	}
	return nil
}
func (p *Postgres) ListUserSessions(id string, now time.Time) ([]model.Session, error) {
	ctx, cancel := dbContext()
	defer cancel()
	rows, err := p.pool.Query(ctx, `SELECT session_id,user_id,created_at,last_seen_at,expires_at,login_ip,last_ip,peer_ip,ip_source,user_agent,device FROM user_sessions WHERE user_id=$1 AND expires_at>$2 ORDER BY created_at DESC LIMIT 20`, id, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []model.Session{}
	for rows.Next() {
		var s model.Session
		if err := rows.Scan(&s.ID, &s.UserID, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt, &s.LoginIP, &s.LastIP, &s.PeerIP, &s.IPSource, &s.UserAgent, &s.Device); err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, rows.Err()
}
func (p *Postgres) DeleteUserSession(userID, id string) (bool, error) {
	ctx, cancel := dbContext()
	defer cancel()
	result, err := p.pool.Exec(ctx, `DELETE FROM user_sessions WHERE user_id=$1 AND session_id=$2`, userID, id)
	if err != nil {
		return false, err
	}
	return result.RowsAffected() > 0, nil
}
func (p *Postgres) TouchSession(hash string, now time.Time, ip string) error {
	ctx, cancel := dbContext()
	defer cancel()
	_, err := p.pool.Exec(ctx, `UPDATE user_sessions SET last_seen_at=$2::timestamptz,last_ip=CASE WHEN $3='' THEN last_ip ELSE $3 END WHERE key_hash=$1 AND (last_seen_at<$2::timestamptz-INTERVAL '1 minute' OR ($3<>'' AND last_ip<>$3))`, hash, now, ip)
	return err
}
