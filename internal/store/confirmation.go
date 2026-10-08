package store

import (
	"context"
	"errors"
)

type ConfirmationStore interface {
	ConfirmationMethod(context.Context, string) (string, error)
	SetConfirmationMethod(context.Context, string, string, int64, string) error
}

func (p *Postgres) ConfirmationMethod(ctx context.Context, id string) (method string, e error) {
	e = p.pool.QueryRow(ctx, `SELECT confirmation_method FROM users WHERE id=$1 AND status='active' AND deleted_at IS NULL`, id).Scan(&method)
	return
}
func (p *Postgres) SetConfirmationMethod(ctx context.Context, id, method string, revision int64, rpid string) error {
	if method != "password" && method != "passkey" {
		return errors.New("invalid confirmation preference")
	}
	result, e := p.pool.Exec(ctx, `UPDATE users SET confirmation_method=$2,updated_at=NOW() WHERE id=$1 AND auth_revision=$3 AND status='active' AND deleted_at IS NULL AND ($2='password' OR EXISTS(SELECT 1 FROM user_passkeys WHERE user_id=$1 AND rp_id=$4))`, id, method, revision, rpid)
	if e == nil && result.RowsAffected() != 1 {
		return ErrConflict
	}
	return e
}
