package store

import (
	"api-manager/internal/model"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

// A ticket is short-lived, session-scoped and consumed atomically before a test.
type TestTicketStore interface {
	PutTestTicket(context.Context, model.APITestTicket) error
	ConsumeTestTicket(context.Context, string, string, string, string, string, time.Time) (model.APITestTicket, error)
}

func (m *Memory) PutTestTicket(ctx context.Context, t model.APITestTicket) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[t.SessionHash]
	if !ok || !time.Now().Before(s.ExpiresAt) {
		return ErrNotFound
	}
	if m.testTickets == nil {
		m.testTickets = map[string]model.APITestTicket{}
	}
	count := 0
	for key, v := range m.testTickets {
		if !time.Now().Before(v.ExpiresAt) {
			delete(m.testTickets, key)
		} else if v.SessionHash == t.SessionHash {
			count++
		}
	}
	if count >= 3 {
		return ErrConflict
	}
	m.testTickets[t.Hash] = t
	return nil
}
func (m *Memory) ConsumeTestTicket(ctx context.Context, hash, session, digest, ip, agent string, now time.Time) (model.APITestTicket, error) {
	if err := ctx.Err(); err != nil {
		return model.APITestTicket{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.testTickets[hash]
	s, active := m.sessions[session]
	if !ok || !active || !now.Before(s.ExpiresAt) || !now.Before(t.ExpiresAt) || t.SessionHash != session || t.Digest != digest || t.ClientIP != ip || t.UserAgent != agent {
		return model.APITestTicket{}, ErrNotFound
	}
	delete(m.testTickets, hash)
	return t, nil
}
func (p *Postgres) PutTestTicket(ctx context.Context, t model.APITestTicket) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var hash string
	if err = tx.QueryRow(ctx, `SELECT key_hash FROM user_sessions WHERE key_hash=$1 AND expires_at>NOW() FOR UPDATE`, t.SessionHash).Scan(&hash); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM api_test_tickets WHERE expires_at<=NOW()`); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM api_test_tickets WHERE session_hash=$1`, t.SessionHash).Scan(&count); err != nil {
		return err
	}
	if count >= 3 {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO api_test_tickets(ticket_hash,session_hash,api_id,request_digest,client_ip,user_agent,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, t.Hash, t.SessionHash, t.APIID, t.Digest, t.ClientIP, t.UserAgent, t.ExpiresAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) ConsumeTestTicket(ctx context.Context, hash, session, digest, ip, agent string, now time.Time) (model.APITestTicket, error) {
	var t model.APITestTicket
	err := p.pool.QueryRow(ctx, `DELETE FROM api_test_tickets WHERE ticket_hash=$1 AND session_hash=$2 AND request_digest=$3 AND client_ip=$4 AND user_agent=$5 AND expires_at>$6 AND EXISTS(SELECT 1 FROM user_sessions WHERE key_hash=$2 AND expires_at>$6) RETURNING ticket_hash,session_hash,api_id,request_digest,client_ip,user_agent,expires_at`, hash, session, digest, ip, agent, now).Scan(&t.Hash, &t.SessionHash, &t.APIID, &t.Digest, &t.ClientIP, &t.UserAgent, &t.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return t, err
}
