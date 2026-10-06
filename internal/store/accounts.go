package store

import (
	"api-manager/internal/model"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
)

type AccountStore interface {
	Account(context.Context, string) (model.AccountRecord, error)
	UpdateBasics(context.Context, string, string) error
	VerifyEmail(context.Context, string, string) error
	BeginTOTP(context.Context, string, string) error
	EnableTOTP(context.Context, string, string, int64, []string) error
	DisableTOTP(context.Context, string, string) error
	ResetUserID(context.Context, string) (string, error)
	AcceptTOTP(context.Context, string, string, int64, string) error
	PutVerification(context.Context, model.Verification) error
	ConsumeVerification(context.Context, string, string, string, string) (model.Verification, error)
	SecuritySettings(context.Context) (model.SecuritySettings, string, error)
	SaveSecuritySettings(context.Context, model.SecuritySettings, string) error
	FindIdentity(context.Context, string, string) (string, error)
	LinkIdentity(context.Context, model.Identity) error
	RenameRole(context.Context, string, string, string) error
}

func (p *Postgres) Account(ctx context.Context, id string) (model.AccountRecord, error) {
	var a model.AccountRecord
	a.UserID = id
	err := p.pool.QueryRow(ctx, `SELECT nickname,email_verified FROM users WHERE id=$1`, id).Scan(&a.Nickname, &a.EmailVerified)
	if err != nil {
		return a, err
	}
	var recovery []byte
	err = p.pool.QueryRow(ctx, `SELECT totp_secret,totp_pending,totp_last_step,recovery_hashes,pending_expires_at FROM account_security WHERE user_id=$1`, id).Scan(&a.TOTPSecret, &a.TOTPPending, &a.TOTPLastStep, &recovery, &a.TOTPPendingExpires)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	if len(recovery) > 0 {
		_ = json.Unmarshal(recovery, &a.RecoveryHashes)
	}
	return a, err
}
func (p *Postgres) UpdateBasics(ctx context.Context, id, nickname string) error {
	_, err := p.pool.Exec(ctx, `UPDATE users SET nickname=$2,updated_at=NOW() WHERE id=$1`, id, nickname)
	return err
}
func (p *Postgres) VerifyEmail(ctx context.Context, id, email string) error {
	result, err := p.pool.Exec(ctx, `UPDATE users SET email_verified=TRUE WHERE id=$1 AND email=$2`, id, email)
	if err == nil && result.RowsAffected() == 0 {
		return ErrConflict
	}
	return err
}
func (p *Postgres) BeginTOTP(ctx context.Context, id, secret string) error {
	result, err := p.pool.Exec(ctx, `INSERT INTO account_security(user_id,totp_pending,pending_expires_at) VALUES($1,$2,NOW()+INTERVAL '10 minutes') ON CONFLICT(user_id) DO UPDATE SET totp_pending=EXCLUDED.totp_pending,pending_expires_at=EXCLUDED.pending_expires_at WHERE account_security.totp_secret=''`, id, secret)
	if err == nil && result.RowsAffected() == 0 {
		return ErrConflict
	}
	return err
}
func (p *Postgres) EnableTOTP(ctx context.Context, id, pending string, step int64, hashes []string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, id); err != nil {
		return err
	}
	data, err := json.Marshal(hashes)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE account_security SET totp_secret=totp_pending,totp_pending='',pending_expires_at='1970-01-01',totp_last_step=$3,recovery_hashes=$4 WHERE user_id=$1 AND totp_secret='' AND totp_pending=$2 AND totp_pending<>'' AND pending_expires_at>NOW()`, id, pending, step, data)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET auth_revision=auth_revision+1 WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM user_sessions WHERE user_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) DisableTOTP(ctx context.Context, id, secret string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, id); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE account_security SET totp_secret='',totp_pending='',pending_expires_at='1970-01-01',totp_last_step=-1,recovery_hashes='[]' WHERE user_id=$1 AND totp_secret=$2 AND totp_secret<>''`, id, secret)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET auth_revision=auth_revision+1 WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM user_sessions WHERE user_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) ResetUserID(ctx context.Context, hash string) (string, error) {
	var id string
	err := p.pool.QueryRow(ctx, `SELECT r.user_id FROM password_resets r JOIN users u ON u.id=r.user_id WHERE r.key_hash=$1 AND r.expires_at>NOW() AND r.password_hash_snapshot=u.password_hash AND r.email_snapshot=u.email AND r.username_snapshot=u.username AND u.status='active'`, hash).Scan(&id)
	return id, err
}
func (p *Postgres) AcceptTOTP(ctx context.Context, id, secret string, step int64, recovery string) error {
	if recovery != "" {
		result, err := p.pool.Exec(ctx, `UPDATE account_security SET recovery_hashes=recovery_hashes-$2 WHERE user_id=$1 AND totp_secret=$3 AND totp_secret<>'' AND recovery_hashes ? $2`, id, recovery, secret)
		if err == nil && result.RowsAffected() == 0 {
			return ErrConflict
		}
		return err
	}
	result, err := p.pool.Exec(ctx, `UPDATE account_security SET totp_last_step=$2 WHERE user_id=$1 AND totp_secret=$3 AND totp_secret<>'' AND totp_last_step<$2`, id, step, secret)
	if err == nil && result.RowsAffected() == 0 {
		return ErrConflict
	}
	return err
}
func (p *Postgres) PutVerification(ctx context.Context, v model.Verification) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `DELETE FROM account_verifications WHERE expires_at<=NOW() OR (purpose=$1 AND subject=$2 AND binding=$3)`, v.Purpose, v.Subject, v.Binding); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO account_verifications(id,purpose,subject,user_id,code_hash,payload,binding,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, v.ID, v.Purpose, v.Subject, v.UserID, v.CodeHash, v.Payload, v.Binding, v.ExpiresAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) ConsumeVerification(ctx context.Context, id, purpose, hash, binding string) (model.Verification, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return model.Verification{}, err
	}
	defer tx.Rollback(ctx)
	var v model.Verification
	err = tx.QueryRow(ctx, `SELECT id,purpose,subject,user_id,code_hash,payload,binding,expires_at,attempts FROM account_verifications WHERE id=$1 FOR UPDATE`, id).Scan(&v.ID, &v.Purpose, &v.Subject, &v.UserID, &v.CodeHash, &v.Payload, &v.Binding, &v.ExpiresAt, &v.Attempts)
	if err != nil {
		return v, err
	}
	var expired bool
	if err = tx.QueryRow(ctx, `SELECT expires_at<=NOW() FROM account_verifications WHERE id=$1`, id).Scan(&expired); err != nil {
		return v, err
	}
	if expired || v.Attempts >= 5 || v.Purpose != purpose || v.CodeHash != hash || v.Binding != binding {
		_, _ = tx.Exec(ctx, `UPDATE account_verifications SET attempts=attempts+1 WHERE id=$1`, id)
		_ = tx.Commit(ctx)
		return v, ErrConflict
	}
	if _, err = tx.Exec(ctx, `DELETE FROM account_verifications WHERE id=$1`, id); err != nil {
		return v, err
	}
	return v, tx.Commit(ctx)
}
func (p *Postgres) SecuritySettings(ctx context.Context) (model.SecuritySettings, string, error) {
	var cfg model.SecuritySettings
	var data []byte
	var secrets string
	err := p.pool.QueryRow(ctx, `SELECT settings,encrypted_secrets,version FROM authentication_settings WHERE id=1`).Scan(&data, &secrets, &cfg.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return cfg, "", nil
	}
	if err != nil {
		return cfg, "", err
	}
	version := cfg.Version
	err = json.Unmarshal(data, &cfg)
	cfg.Version = version
	return cfg, secrets, err
}
func (p *Postgres) SaveSecuritySettings(ctx context.Context, cfg model.SecuritySettings, secrets string) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	result, err := p.pool.Exec(ctx, `INSERT INTO authentication_settings(id,settings,encrypted_secrets,version) VALUES(1,$1,$2,1) ON CONFLICT(id) DO UPDATE SET settings=EXCLUDED.settings,encrypted_secrets=EXCLUDED.encrypted_secrets,version=authentication_settings.version+1 WHERE authentication_settings.version=$3`, data, secrets, cfg.Version)
	if err == nil && result.RowsAffected() != 1 {
		return ErrConflict
	}
	return err
}

func (p *Postgres) FindIdentity(ctx context.Context, provider, subject string) (string, error) {
	var id string
	err := p.pool.QueryRow(ctx, `SELECT user_id FROM external_identities WHERE provider=$1 AND subject=$2`, provider, subject).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return id, err
}
func (p *Postgres) LinkIdentity(ctx context.Context, v model.Identity) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO external_identities(provider,subject,user_id) VALUES($1,$2,$3)`, v.Provider, v.Subject, v.UserID)
	return err
}
func (p *Postgres) RenameRole(ctx context.Context, name, display, description string) error {
	result, err := p.pool.Exec(ctx, `UPDATE roles SET display_name=$2,description=$3 WHERE name=$1 AND tenant_id IS NULL`, name, display, description)
	if err == nil && result.RowsAffected() != 1 {
		return ErrNotFound
	}
	return err
}

func (p *Postgres) Identities(ctx context.Context, id string) ([]string, error) {
	rows, err := p.pool.Query(ctx, `SELECT provider FROM external_identities WHERE user_id=$1 ORDER BY provider`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []string{}
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	return list, rows.Err()
}
func (p *Postgres) UnlinkIdentity(ctx context.Context, id, provider string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM external_identities WHERE user_id=$1 AND provider=$2`, id, provider)
	return err
}
func (p *Postgres) ActiveSession(ctx context.Context, id, hash string) (bool, error) {
	var ok bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_sessions WHERE user_id=$1 AND key_hash=$2 AND expires_at>NOW())`, id, hash).Scan(&ok)
	return ok, err
}
func (p *Postgres) OwnCredentials(ctx context.Context, id string) ([]model.Credential, error) {
	rows, err := p.pool.Query(ctx, `SELECT id,name,prefix,revoked,expires_at,created_at FROM api_credentials WHERE owner_user_id=$1 ORDER BY created_at DESC LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []model.Credential{}
	for rows.Next() {
		var v model.Credential
		if err = rows.Scan(&v.ID, &v.Name, &v.Prefix, &v.Revoked, &v.ExpiresAt, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.OwnerUserID = id
		list = append(list, v)
	}
	return list, rows.Err()
}

func (p *Postgres) RenamePermission(ctx context.Context, code, label string) error {
	result, err := p.pool.Exec(ctx, `UPDATE permissions SET description=$2 WHERE code=$1`, code, label)
	if err == nil && result.RowsAffected() != 1 {
		return ErrNotFound
	}
	return err
}

func (p *Postgres) CreateOwnedCredential(ctx context.Context, c model.Credential) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, c.OwnerUserID).Scan(&status); err != nil {
		return err
	}
	if status != "active" {
		return ErrNotFound
	}
	var active int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM api_credentials WHERE owner_user_id=$1 AND revoked=FALSE AND (expires_at IS NULL OR expires_at>NOW())`, c.OwnerUserID).Scan(&active); err != nil {
		return err
	}
	if active >= 20 {
		return ErrConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO api_credentials(id,name,prefix,key_hash,encrypted_key,created_at,owner_user_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, c.ID, c.Name, c.Prefix, c.Hash, c.EncryptedKey, c.CreatedAt, c.OwnerUserID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
