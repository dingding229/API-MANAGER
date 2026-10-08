package store

import (
	"context"
	"time"
)

type DeviceBindingStore interface {
	BindSessionDevice(context.Context, string, string, string, string) error
}

func (p *Postgres) BindSessionDevice(ctx context.Context, hash, userID, agent, binding string) error {
	result, e := p.pool.Exec(ctx, `UPDATE user_sessions SET device_binding_hash=$4 WHERE key_hash=$1 AND user_id=$2 AND user_agent=$3 AND device_binding_hash='' AND expires_at>NOW()`, hash, userID, agent, binding)
	if e == nil && result.RowsAffected() != 1 {
		return ErrConflict
	}
	return e
}
func (m *Memory) BindSessionDevice(ctx context.Context, hash, userID, agent, binding string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[hash]
	if !ok || s.UserID != userID || s.UserAgent != agent || s.DeviceBindingHash != "" || !time.Now().Before(s.ExpiresAt) {
		return ErrConflict
	}
	s.DeviceBindingHash = binding
	m.sessions[hash] = s
	return nil
}
