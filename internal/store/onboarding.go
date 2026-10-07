package store

import (
	"api-manager/internal/model"
	"context"
)

func (p *Postgres) PeekVerification(ctx context.Context, id, purpose, hash, binding string) (model.Verification, error) {
	var v model.Verification
	e := p.pool.QueryRow(ctx, `SELECT id,purpose,subject,user_id,payload,binding,expires_at FROM account_verifications WHERE id=$1 AND purpose=$2 AND code_hash=$3 AND binding=$4 AND expires_at>NOW() AND attempts<5`, id, purpose, hash, binding).Scan(&v.ID, &v.Purpose, &v.Subject, &v.UserID, &v.Payload, &v.Binding, &v.ExpiresAt)
	return v, e
}
