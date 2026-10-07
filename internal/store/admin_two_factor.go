package store

import (
	"context"
)

// Administrative recovery clears active/pending secrets and recovery codes atomically.
func (p *Postgres) ResetUserTwoFactor(ctx context.Context, id string, revision int64) error {
	tx, e := p.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var actual int64
	if e = tx.QueryRow(ctx, `SELECT auth_revision FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&actual); e != nil {
		return ErrNotFound
	}
	if actual != revision {
		return ErrConflict
	}
	if _, e = tx.Exec(ctx, `UPDATE account_security SET totp_secret='',totp_pending='',pending_expires_at='1970-01-01',totp_last_step=-1,recovery_hashes='[]' WHERE user_id=$1`, id); e != nil {
		return e
	}
	for _, q := range []string{`UPDATE users SET auth_revision=auth_revision+1,updated_at=NOW() WHERE id=$1`, `DELETE FROM user_sessions WHERE user_id=$1`, `DELETE FROM password_resets WHERE user_id=$1`, `DELETE FROM account_verifications WHERE user_id=$1 OR binding=$1`} {
		if _, e = tx.Exec(ctx, q, id); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
