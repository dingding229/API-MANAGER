package store

import (
	"api-manager/internal/model"
	"context"
)

type PasskeyStore interface {
	Passkeys(context.Context, string, string) ([]model.Passkey, error)
	Passkey(context.Context, string, string) (model.Passkey, error)
	AddPasskey(context.Context, model.Passkey, int64) error
	UsePasskey(context.Context, model.Passkey, int64) error
	DeletePasskey(context.Context, string, string) error
}

func (p *Postgres) Passkeys(ctx context.Context, userID, rpid string) ([]model.Passkey, error) {
	rows, e := p.pool.Query(ctx, `SELECT credential_id,user_id,rp_id,name,credential,revision,created_at,last_used_at FROM user_passkeys WHERE user_id=$1 AND rp_id=$2 ORDER BY created_at`, userID, rpid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Passkey{}
	for rows.Next() {
		var v model.Passkey
		if e = rows.Scan(&v.ID, &v.UserID, &v.RPID, &v.Name, &v.Credential, &v.Revision, &v.CreatedAt, &v.LastUsedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (p *Postgres) Passkey(ctx context.Context, id, rpid string) (v model.Passkey, e error) {
	e = p.pool.QueryRow(ctx, `SELECT credential_id,user_id,rp_id,name,credential,revision,created_at,last_used_at FROM user_passkeys WHERE credential_id=$1 AND rp_id=$2`, id, rpid).Scan(&v.ID, &v.UserID, &v.RPID, &v.Name, &v.Credential, &v.Revision, &v.CreatedAt, &v.LastUsedAt)
	return
}
func (p *Postgres) AddPasskey(ctx context.Context, v model.Passkey, authRevision int64) error {
	tx, e := p.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var current int64
	e = tx.QueryRow(ctx, `SELECT auth_revision FROM users WHERE id=$1 AND status='active' AND deleted_at IS NULL FOR UPDATE`, v.UserID).Scan(&current)
	if e != nil || current != authRevision {
		return ErrConflict
	}
	var count int
	e = tx.QueryRow(ctx, `SELECT count(*) FROM user_passkeys WHERE user_id=$1`, v.UserID).Scan(&count)
	if e != nil {
		return e
	}
	if count >= 20 {
		return ErrConflict
	}
	_, e = tx.Exec(ctx, `INSERT INTO user_passkeys(credential_id,user_id,rp_id,name,credential) VALUES($1,$2,$3,$4,$5)`, v.ID, v.UserID, v.RPID, v.Name, []byte(v.Credential))
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (p *Postgres) UsePasskey(ctx context.Context, v model.Passkey, authRevision int64) error {
	result, e := p.pool.Exec(ctx, `UPDATE user_passkeys p SET credential=$3,revision=p.revision+1,last_used_at=NOW() FROM users u WHERE p.credential_id=$1 AND p.user_id=$2 AND p.rp_id=$4 AND p.revision=$5 AND u.id=p.user_id AND u.auth_revision=$6 AND u.status='active' AND u.deleted_at IS NULL`, v.ID, v.UserID, []byte(v.Credential), v.RPID, v.Revision, authRevision)
	if e == nil && result.RowsAffected() != 1 {
		return ErrConflict
	}
	return e
}
func (p *Postgres) DeletePasskey(ctx context.Context, userID, id string) error {
	tx, e := p.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	// Serialize deletion with successful assertions; invalidate sessions and any
	// outstanding confirmation tied to a removed authenticator.
	var locked string
	if e = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND status='active' AND deleted_at IS NULL FOR UPDATE`, userID).Scan(&locked); e != nil {
		return e
	}
	result, e := tx.Exec(ctx, `DELETE FROM user_passkeys WHERE user_id=$1 AND credential_id=$2`, userID, id)
	if e != nil {
		return e
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	if _, e = tx.Exec(ctx, `UPDATE users SET auth_revision=auth_revision+1,updated_at=NOW() WHERE id=$1`, userID); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM user_sessions WHERE user_id=$1`, userID); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM account_verifications WHERE user_id=$1 AND purpose LIKE 'passkey-%'`, userID); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

var _ PasskeyStore = (*Postgres)(nil)
