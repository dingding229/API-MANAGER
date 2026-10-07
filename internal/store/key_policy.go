package store

import (
	"api-manager/internal/model"
	"context"
)

func (p *Postgres) SetCredentialIPRanges(ctx context.Context, id, owner string, ranges []string) (model.Credential, error) {
	result, e := p.pool.Exec(ctx, `UPDATE api_credentials SET allowed_ip_ranges=$3 WHERE id=$1 AND COALESCE(owner_user_id::text,'')=$2 AND revoked=FALSE AND (owner_user_id IS NULL OR EXISTS(SELECT 1 FROM users WHERE users.id=owner_user_id AND status='active'))`, id, owner, ipRangesJSON(ranges))
	if e != nil {
		return model.Credential{}, e
	}
	if result.RowsAffected() != 1 {
		return model.Credential{}, ErrConflict
	}
	return p.GetCredential(id)
}
func (m *Memory) SetCredentialIPRanges(_ context.Context, id, owner string, ranges []string) (model.Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.credentials[id]
	if !ok {
		return c, ErrNotFound
	}
	if c.Revoked || c.OwnerUserID != owner {
		return c, ErrConflict
	}
	c.AllowedIPRanges = append([]string{}, ranges...)
	m.credentials[id] = c
	return c, nil
}
