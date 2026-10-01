package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"api-manager/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.up.sql
var migrationFiles embed.FS

type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}
	config.MaxConns = 10
	config.MinConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	p := &Postgres{pool: pool}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := p.Ping(pingCtx); err != nil {
		p.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if err := p.Migrate(ctx); err != nil {
		p.Close()
		return nil, err
	}
	if err := p.EnsureRBAC(); err != nil {
		p.Close()
		return nil, fmt.Errorf("seed RBAC: %w", err)
	}
	return p, nil
}

func (p *Postgres) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }
func (p *Postgres) Close()                         { p.pool.Close() }

func (p *Postgres) Migrate(ctx context.Context) error {
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration lock connection: %w", err)
	}
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock(8142075)"); err != nil {
		conn.Release()
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(unlockCtx, "SELECT pg_advisory_unlock(8142075)"); err != nil {
			_ = conn.Hijack().Close(unlockCtx) // Never return a session holding the lock to the pool.
		} else {
			conn.Release()
		}
	}()
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	entries, err := fs.Glob(migrationFiles, "migrations/*.up.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(entries)
	for _, entry := range entries {
		version := strings.TrimSuffix(strings.TrimPrefix(entry, "migrations/"), ".up.sql")
		var applied bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		if applied {
			continue
		}
		contents, err := migrationFiles.ReadFile(entry)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", version, err)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", version, err)
		}
		if _, err = tx.Exec(ctx, string(contents)); err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", version, err)
		}
	}
	return nil
}

func (p *Postgres) CreateAPI(api model.API) error {
	ctx, cancel := dbContext()
	defer cancel()
	authConfig, err := json.Marshal(api.AuthConfig)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO apis
		(id,name,description,method,path,auth_mode,auth_config,rate_limit_per_minute,daily_quota,monthly_quota,response_status,response_body,request_schema,response_schema,parameters_schema,plugin_name,upstream_url,upstream_path,strip_path,upstream_timeout_ms,upstream_retries,circuit_breaker_threshold,circuit_breaker_reset_seconds,enabled,published_at,created_at,updated_at,upstream_auth_ref,public_visible,public_title,public_summary,public_category)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32)`,
		api.ID, api.Name, api.Description, api.Method, api.Path, api.AuthMode, authConfig,
		api.RateLimitPerMinute, api.DailyQuota, api.MonthlyQuota, api.ResponseStatus, api.ResponseBody, schemaDocument(api.RequestSchema), schemaDocument(api.ResponseSchema), schemaDocument(api.ParametersSchema),
		api.Plugin, api.UpstreamURL, api.UpstreamPath, api.StripPath, api.UpstreamTimeoutMS, api.UpstreamRetries, api.CircuitThreshold, api.CircuitResetSecs, api.Enabled, api.PublishedAt, api.CreatedAt, api.UpdatedAt, api.UpstreamAuthRef, api.PublicVisible, api.PublicTitle, api.PublicSummary, api.PublicCategory)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return ErrConflict
		}
		return fmt.Errorf("create api: %w", err)
	}
	return nil
}

func (p *Postgres) UpdateAPI(api model.API) error {
	ctx, cancel := dbContext()
	defer cancel()
	authConfig, err := json.Marshal(api.AuthConfig)
	if err != nil {
		return err
	}
	result, err := p.pool.Exec(ctx, `UPDATE apis SET
		name=$2,description=$3,method=$4,path=$5,auth_mode=$6,auth_config=$7,rate_limit_per_minute=$8,daily_quota=$9,monthly_quota=$10,response_status=$11,response_body=$12,request_schema=$13,response_schema=$14,parameters_schema=$15,plugin_name=$16,upstream_url=$17,upstream_path=$18,strip_path=$19,upstream_timeout_ms=$20,upstream_retries=$21,circuit_breaker_threshold=$22,circuit_breaker_reset_seconds=$23,enabled=$24,published_at=$25,updated_at=$26,upstream_auth_ref=$27,public_visible=$28,public_title=$29,public_summary=$30,public_category=$31
		WHERE id=$1`,
		api.ID, api.Name, api.Description, api.Method, api.Path, api.AuthMode, authConfig,
		api.RateLimitPerMinute, api.DailyQuota, api.MonthlyQuota, api.ResponseStatus, api.ResponseBody, schemaDocument(api.RequestSchema), schemaDocument(api.ResponseSchema), schemaDocument(api.ParametersSchema),
		api.Plugin, api.UpstreamURL, api.UpstreamPath, api.StripPath, api.UpstreamTimeoutMS, api.UpstreamRetries, api.CircuitThreshold, api.CircuitResetSecs, api.Enabled, api.PublishedAt, api.UpdatedAt, api.UpstreamAuthRef, api.PublicVisible, api.PublicTitle, api.PublicSummary, api.PublicCategory)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return ErrConflict
		}
		return fmt.Errorf("update api: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const apiSelect = `SELECT id,name,description,method,path,auth_mode,auth_config,rate_limit_per_minute,daily_quota,monthly_quota,response_status,response_body,request_schema,response_schema,parameters_schema,plugin_name,upstream_url,upstream_path,strip_path,upstream_timeout_ms,upstream_retries,circuit_breaker_threshold,circuit_breaker_reset_seconds,enabled,published_at,created_at,updated_at,upstream_auth_ref,public_visible,public_title,public_summary,public_category FROM apis`

func scanAPI(row pgx.Row) (model.API, error) {
	var api model.API
	var authConfig, requestSchema, responseSchema, parametersSchema []byte
	var pluginName string
	if err := row.Scan(&api.ID, &api.Name, &api.Description, &api.Method, &api.Path, &api.AuthMode, &authConfig,
		&api.RateLimitPerMinute, &api.DailyQuota, &api.MonthlyQuota, &api.ResponseStatus, &api.ResponseBody, &requestSchema, &responseSchema, &parametersSchema, &pluginName,
		&api.UpstreamURL, &api.UpstreamPath, &api.StripPath, &api.UpstreamTimeoutMS, &api.UpstreamRetries, &api.CircuitThreshold, &api.CircuitResetSecs, &api.Enabled, &api.PublishedAt, &api.CreatedAt, &api.UpdatedAt, &api.UpstreamAuthRef, &api.PublicVisible, &api.PublicTitle, &api.PublicSummary, &api.PublicCategory); err != nil {
		return model.API{}, err
	}
	api.Plugin = pluginName
	api.RequestSchema = schemaRawMessage(requestSchema)
	api.ResponseSchema = schemaRawMessage(responseSchema)
	api.ParametersSchema = schemaRawMessage(parametersSchema)
	if len(authConfig) > 0 {
		if err := json.Unmarshal(authConfig, &api.AuthConfig); err != nil {
			return model.API{}, fmt.Errorf("decode api auth config: %w", err)
		}
	}
	return api, nil
}

func schemaDocument(raw json.RawMessage) []byte {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return []byte("{}")
	}
	return raw
}

func schemaRawMessage(raw []byte) json.RawMessage {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("{}")) {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func (p *Postgres) GetAPI(id string) (model.API, error) {
	ctx, cancel := dbContext()
	defer cancel()
	api, err := scanAPI(p.pool.QueryRow(ctx, apiSelect+` WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.API{}, ErrNotFound
	}
	return api, err
}

func (p *Postgres) ListAPIs() []model.API {
	result, _ := p.ListAPIsChecked()
	return result
}

// ListAPIsChecked prevents database errors from being misreported as missing routes.
func (p *Postgres) ListAPIsChecked() ([]model.API, error) {
	ctx, cancel := dbContext()
	defer cancel()
	rows, err := p.pool.Query(ctx, apiSelect+` ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.API, 0)
	for rows.Next() {
		api, err := scanAPI(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, api)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (p *Postgres) DeleteAPI(id string) error {
	ctx, cancel := dbContext()
	defer cancel()
	result, err := p.pool.Exec(ctx, `DELETE FROM apis WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete api: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) CreateCredential(credential model.Credential) error {
	ctx, cancel := dbContext()
	defer cancel()
	_, err := p.pool.Exec(ctx, `INSERT INTO api_credentials(id,name,prefix,key_hash,encrypted_key,revoked,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		credential.ID, credential.Name, credential.Prefix, credential.Hash, credential.EncryptedKey, credential.Revoked, credential.ExpiresAt, credential.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return ErrConflict
		}
		return fmt.Errorf("create credential: %w", err)
	}
	return nil
}

func (p *Postgres) UpdateCredential(credential model.Credential) error {
	ctx, cancel := dbContext()
	defer cancel()
	result, err := p.pool.Exec(ctx, `UPDATE api_credentials SET name=$2,revoked=$3,expires_at=$4 WHERE id=$1`, credential.ID, credential.Name, credential.Revoked, credential.ExpiresAt)
	if err != nil {
		return fmt.Errorf("update credential: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RotateCredential replaces the hash atomically so no old/new key overlap exists.
func (p *Postgres) RotateCredential(id, prefix, hash, encryptedKey string) (model.Credential, error) {
	ctx, cancel := dbContext()
	defer cancel()
	var credential model.Credential
	err := p.pool.QueryRow(ctx, `UPDATE api_credentials SET prefix=$2,key_hash=$3,encrypted_key=$4 WHERE id=$1 AND revoked=FALSE
		RETURNING id,name,prefix,key_hash,encrypted_key,revoked,created_at,expires_at`, id, prefix, hash, encryptedKey).
		Scan(&credential.ID, &credential.Name, &credential.Prefix, &credential.Hash, &credential.EncryptedKey, &credential.Revoked, &credential.CreatedAt, &credential.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		var revoked bool
		if lookupErr := p.pool.QueryRow(ctx, `SELECT revoked FROM api_credentials WHERE id=$1`, id).Scan(&revoked); errors.Is(lookupErr, pgx.ErrNoRows) {
			return model.Credential{}, ErrNotFound
		} else if lookupErr != nil {
			return model.Credential{}, lookupErr
		}
		return model.Credential{}, ErrConflict
	}
	if err != nil {
		return model.Credential{}, fmt.Errorf("rotate credential: %w", err)
	}
	return credential, nil
}

func (p *Postgres) GetCredential(id string) (model.Credential, error) {
	ctx, cancel := dbContext()
	defer cancel()
	var credential model.Credential
	err := p.pool.QueryRow(ctx, `SELECT id,name,prefix,key_hash,encrypted_key,revoked,created_at,expires_at FROM api_credentials WHERE id=$1`, id).
		Scan(&credential.ID, &credential.Name, &credential.Prefix, &credential.Hash, &credential.EncryptedKey, &credential.Revoked, &credential.CreatedAt, &credential.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Credential{}, ErrNotFound
	}
	credential.KeyAvailable = credential.EncryptedKey != ""
	return credential, err
}

func (p *Postgres) ListCredentials() []model.Credential {
	ctx, cancel := dbContext()
	defer cancel()
	rows, err := p.pool.Query(ctx, `SELECT id,name,prefix,key_hash,encrypted_key,revoked,created_at,expires_at FROM api_credentials ORDER BY created_at ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]model.Credential, 0)
	for rows.Next() {
		var credential model.Credential
		if err := rows.Scan(&credential.ID, &credential.Name, &credential.Prefix, &credential.Hash, &credential.EncryptedKey, &credential.Revoked, &credential.CreatedAt, &credential.ExpiresAt); err == nil {
			credential.KeyAvailable = credential.EncryptedKey != ""
			result = append(result, credential)
		}
	}
	return result
}

func (p *Postgres) FindCredentialByHash(hash string) (model.Credential, bool) {
	ctx, cancel := dbContext()
	defer cancel()
	var credential model.Credential
	err := p.pool.QueryRow(ctx, `SELECT id,name,prefix,key_hash,encrypted_key,revoked,created_at,expires_at FROM api_credentials WHERE key_hash=$1`, hash).
		Scan(&credential.ID, &credential.Name, &credential.Prefix, &credential.Hash, &credential.EncryptedKey, &credential.Revoked, &credential.CreatedAt, &credential.ExpiresAt)
	return credential, err == nil
}

func dbContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func (p *Postgres) CreateUser(user model.User) error {
	ctx, cancel := dbContext()
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO users(id,username,email,password_hash,role,status,created_at,updated_at) VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8)`, user.ID, user.Username, user.Email, user.PasswordHash, user.Role, user.Status, user.CreatedAt, user.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return ErrConflict
		}
		return fmt.Errorf("create user: %w", err)
	}
	roles := user.Roles
	if len(roles) == 0 && user.Role != "" {
		roles = []string{user.Role}
	}
	if len(roles) == 0 {
		roles = []string{"viewer"}
	}
	for _, name := range dedupeCodes(roles) {
		var roleID string
		if err := tx.QueryRow(ctx, `SELECT id FROM roles WHERE tenant_id IS NULL AND name=$1`, name).Scan(&roleID); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2)`, user.ID, roleID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) GetUserByUsername(username string) (model.User, error) {
	ctx, cancel := dbContext()
	defer cancel()
	var user model.User
	err := p.pool.QueryRow(ctx, `SELECT id,username,COALESCE(email,''),password_hash,role,status,created_at,updated_at FROM users WHERE username=$1`, username).
		Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.Role, &user.Status, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	if err == nil {
		user.Roles = p.userRoles(ctx, user.ID)
		if len(user.Roles) > 0 {
			user.Role = user.Roles[0]
		}
	}
	return user, err
}

func (p *Postgres) GetUserByEmail(email string) (model.User, error) {
	ctx, cancel := dbContext()
	defer cancel()
	var user model.User
	err := p.pool.QueryRow(ctx, `SELECT id,username,COALESCE(email,''),password_hash,role,status,created_at,updated_at FROM users WHERE email=$1`, email).
		Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.Role, &user.Status, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	if err == nil {
		user.Roles = p.userRoles(ctx, user.ID)
		if len(user.Roles) > 0 {
			user.Role = user.Roles[0]
		}
	}
	return user, err
}

func (p *Postgres) ListUsers() []model.User {
	ctx, cancel := dbContext()
	defer cancel()
	rows, err := p.pool.Query(ctx, `SELECT id,username,COALESCE(email,''),password_hash,role,status,created_at,updated_at FROM users ORDER BY created_at ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	users := make([]model.User, 0)
	for rows.Next() {
		var user model.User
		if err := rows.Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.Role, &user.Status, &user.CreatedAt, &user.UpdatedAt); err == nil {
			user.Roles = p.userRoles(ctx, user.ID)
			if len(user.Roles) > 0 {
				user.Role = user.Roles[0]
			}
			users = append(users, user)
		}
	}
	return users
}

func (p *Postgres) CountUsers() int {
	count, _ := p.CountUsersChecked()
	return count
}

func (p *Postgres) CountUsersChecked() (int, error) {
	ctx, cancel := dbContext()
	defer cancel()
	var count int
	if err := p.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return count, nil
}

func (p *Postgres) CreateRelease(api model.API) (model.Release, error) {
	ctx, cancel := dbContext()
	defer cancel()
	snapshot, err := json.Marshal(api)
	if err != nil {
		return model.Release{}, fmt.Errorf("marshal release snapshot: %w", err)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return model.Release{}, fmt.Errorf("begin release: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, api.ID); err != nil {
		return model.Release{}, fmt.Errorf("lock release: %w", err)
	}
	var version int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM api_releases WHERE api_id=$1`, api.ID).Scan(&version); err != nil {
		return model.Release{}, fmt.Errorf("next release version: %w", err)
	}
	var result model.Release
	result.APIID, result.Version, result.Snapshot = api.ID, version, api
	err = tx.QueryRow(ctx, `INSERT INTO api_releases(api_id,version,snapshot) VALUES($1,$2,$3) RETURNING id,published_at`, api.ID, version, snapshot).Scan(&result.ID, &result.PublishedAt)
	if err != nil {
		return model.Release{}, fmt.Errorf("insert release: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Release{}, fmt.Errorf("commit release: %w", err)
	}
	return result, nil
}

func (p *Postgres) ListReleases(apiID string) []model.Release {
	ctx, cancel := dbContext()
	defer cancel()
	rows, err := p.pool.Query(ctx, `SELECT id,api_id,version,snapshot,published_at FROM api_releases WHERE api_id=$1 ORDER BY version DESC`, apiID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]model.Release, 0)
	for rows.Next() {
		var release model.Release
		var snapshot []byte
		if err := rows.Scan(&release.ID, &release.APIID, &release.Version, &snapshot, &release.PublishedAt); err != nil {
			continue
		}
		if json.Unmarshal(snapshot, &release.Snapshot) != nil {
			continue
		}
		result = append(result, release)
	}
	return result
}

func (p *Postgres) GetRelease(apiID string, version int) (model.Release, error) {
	ctx, cancel := dbContext()
	defer cancel()
	var release model.Release
	var snapshot []byte
	err := p.pool.QueryRow(ctx, `SELECT id,api_id,version,snapshot,published_at FROM api_releases WHERE api_id=$1 AND version=$2`, apiID, version).
		Scan(&release.ID, &release.APIID, &release.Version, &snapshot, &release.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Release{}, ErrNotFound
	}
	if err != nil {
		return model.Release{}, err
	}
	if err := json.Unmarshal(snapshot, &release.Snapshot); err != nil {
		return model.Release{}, fmt.Errorf("decode release snapshot: %w", err)
	}
	return release, nil
}

func (p *Postgres) EnsureRBAC() error {
	ctx, cancel := dbContext()
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin RBAC seed: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, permission := range DefaultPermissions() {
		if _, err := tx.Exec(ctx, `INSERT INTO permissions(id,code,description) VALUES($1,$2,$3) ON CONFLICT(code) DO UPDATE SET description=EXCLUDED.description`, newUUID(), permission.Code, permission.Description); err != nil {
			return fmt.Errorf("seed permission %s: %w", permission.Code, err)
		}
	}
	for _, role := range DefaultRoles() {
		if err := ensureRoleTx(ctx, tx, role); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) CreateRole(role model.Role) error {
	ctx, cancel := dbContext()
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var count int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM roles WHERE tenant_id IS NULL AND name=$1`, role.Name).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrConflict
	}
	if err := ensureRoleTx(ctx, tx, role); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func ensureRoleTx(ctx context.Context, tx pgx.Tx, role model.Role) error {
	var roleID string
	err := tx.QueryRow(ctx, `SELECT id FROM roles WHERE tenant_id IS NULL AND name=$1`, role.Name).Scan(&roleID)
	if errors.Is(err, pgx.ErrNoRows) {
		roleID = role.ID
		if roleID == "" {
			roleID = newUUID()
		}
		if _, err := tx.Exec(ctx, `INSERT INTO roles(id,name,description) VALUES($1,$2,$3)`, roleID, role.Name, role.Description); err != nil {
			return fmt.Errorf("create role %s: %w", role.Name, err)
		}
	} else if err != nil {
		return err
	} else if _, err := tx.Exec(ctx, `UPDATE roles SET description=$2 WHERE id=$1`, roleID, role.Description); err != nil {
		return err
	} else {
		// Existing roles may have been customized by administrators. Seeding must
		// never overwrite their permissions on every application restart.
		return nil
	}
	return setRolePermissionsTx(ctx, tx, roleID, role.Permissions)
}

func setRolePermissionsTx(ctx context.Context, tx pgx.Tx, roleID string, codes []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id=$1`, roleID); err != nil {
		return err
	}
	for _, code := range dedupeCodes(codes) {
		if code == "*" {
			continue
		}
		var permissionID string
		if err := tx.QueryRow(ctx, `SELECT id FROM permissions WHERE code=$1`, code).Scan(&permissionID); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO role_permissions(role_id,permission_id) VALUES($1,$2)`, roleID, permissionID); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) UpdateRolePermissions(name string, codes []string) error {
	ctx, cancel := dbContext()
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var roleID string
	if err := tx.QueryRow(ctx, `SELECT id FROM roles WHERE tenant_id IS NULL AND name=$1`, name).Scan(&roleID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if err := setRolePermissionsTx(ctx, tx, roleID, codes); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ListPermissions() []model.Permission {
	ctx, cancel := dbContext()
	defer cancel()
	rows, err := p.pool.Query(ctx, `SELECT id,code,description FROM permissions ORDER BY code`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]model.Permission, 0)
	for rows.Next() {
		var item model.Permission
		if rows.Scan(&item.ID, &item.Code, &item.Description) == nil {
			result = append(result, item)
		}
	}
	return result
}

func (p *Postgres) ListRoles() []model.Role {
	ctx, cancel := dbContext()
	defer cancel()
	rows, err := p.pool.Query(ctx, `SELECT id,name,description FROM roles WHERE tenant_id IS NULL ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	roles := make([]model.Role, 0)
	for rows.Next() {
		var role model.Role
		if err := rows.Scan(&role.ID, &role.Name, &role.Description); err != nil {
			continue
		}
		role.Permissions = p.rolePermissions(ctx, role.ID)
		roles = append(roles, role)
	}
	return roles
}

func (p *Postgres) GetRoleByName(name string) (model.Role, error) {
	ctx, cancel := dbContext()
	defer cancel()
	var role model.Role
	if err := p.pool.QueryRow(ctx, `SELECT id,name,description FROM roles WHERE tenant_id IS NULL AND name=$1`, name).Scan(&role.ID, &role.Name, &role.Description); errors.Is(err, pgx.ErrNoRows) {
		return model.Role{}, ErrNotFound
	} else if err != nil {
		return model.Role{}, err
	}
	role.Permissions = p.rolePermissions(ctx, role.ID)
	return role, nil
}

func (p *Postgres) rolePermissions(ctx context.Context, roleID string) []string {
	rows, err := p.pool.Query(ctx, `SELECT p.code FROM permissions p JOIN role_permissions rp ON rp.permission_id=p.id WHERE rp.role_id=$1 ORDER BY p.code`, roleID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var code string
		if rows.Scan(&code) == nil {
			result = append(result, code)
		}
	}
	if len(result) == 0 {
		var name string
		if p.pool.QueryRow(ctx, `SELECT name FROM roles WHERE id=$1`, roleID).Scan(&name) == nil && name == "super_admin" {
			return []string{"*"}
		}
	}
	return result
}

func (p *Postgres) AssignUserRoles(userID string, roles []string) error {
	ctx, cancel := dbContext()
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, userID); err != nil {
		return err
	}
	roleNames := dedupeCodes(roles)
	for _, name := range roleNames {
		var roleID string
		if err := tx.QueryRow(ctx, `SELECT id FROM roles WHERE tenant_id IS NULL AND name=$1`, name).Scan(&roleID); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2)`, userID, roleID); err != nil {
			return err
		}
	}
	if len(roleNames) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE users SET role=$2,updated_at=NOW() WHERE id=$1`, userID, roleNames[0]); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ListUserRoles(userID string) []string {
	ctx, cancel := dbContext()
	defer cancel()
	return p.userRoles(ctx, userID)
}

func (p *Postgres) userRoles(ctx context.Context, userID string) []string {
	rows, err := p.pool.Query(ctx, `SELECT r.name FROM roles r JOIN user_roles ur ON ur.role_id=r.id WHERE ur.user_id=$1 ORDER BY r.name`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			result = append(result, name)
		}
	}
	return result
}

func (p *Postgres) GetUserPermissions(userID string) []string {
	ctx, cancel := dbContext()
	defer cancel()
	roles := p.userRoles(ctx, userID)
	for _, role := range roles {
		if role == "super_admin" {
			return []string{"*"}
		}
	}
	rows, err := p.pool.Query(ctx, `SELECT DISTINCT p.code FROM permissions p JOIN role_permissions rp ON rp.permission_id=p.id JOIN user_roles ur ON ur.role_id=rp.role_id WHERE ur.user_id=$1 ORDER BY p.code`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var code string
		if rows.Scan(&code) == nil {
			result = append(result, code)
		}
	}
	return result
}

func (p *Postgres) GetUserByID(id string) (model.User, error) {
	ctx, cancel := dbContext()
	defer cancel()
	var user model.User
	err := p.pool.QueryRow(ctx, `SELECT id,username,COALESCE(email,''),password_hash,role,status,created_at,updated_at FROM users WHERE id=$1`, id).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.Role, &user.Status, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	if err != nil {
		return model.User{}, err
	}
	user.Roles = p.userRoles(ctx, user.ID)
	if len(user.Roles) > 0 {
		user.Role = user.Roles[0]
	}
	return user, nil
}

func (p *Postgres) UpdateUserStatus(id, status string) error {
	ctx, cancel := dbContext()
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE users SET status=$2,updated_at=NOW() WHERE id=$1`, id, status)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	if status == "disabled" {
		if _, err := tx.Exec(ctx, `DELETE FROM password_resets WHERE user_id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM user_sessions WHERE user_id=$1`, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func dedupeCodes(values []string) []string {
	set := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !set[value] {
			set[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func newUUID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}

func (p *Postgres) CreateAuditLog(log model.AuditLog) error {
	ctx, cancel := dbContext()
	defer cancel()
	details, err := json.Marshal(log.Details)
	if err != nil {
		return fmt.Errorf("marshal audit details: %w", err)
	}
	var actorID any
	if log.ActorID != "" {
		actorID = log.ActorID
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO audit_logs
		(actor_id,actor_type,actor_email,action,resource_type,resource_id,request_id,method,path,remote_addr,user_agent,status_code,details,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		actorID, log.ActorType, log.ActorEmail, log.Action, log.ResourceType, nullableText(log.ResourceID), nullableText(log.RequestID),
		nullableText(log.Method), nullableText(log.Path), nullableText(log.RemoteAddr), nullableText(log.UserAgent), nullableInt(log.StatusCode), details, log.CreatedAt)
	if err != nil {
		return fmt.Errorf("create audit log: %w", err)
	}
	return nil
}

func (p *Postgres) ListAuditLogs(query model.AuditLogQuery) (model.AuditLogPage, error) {
	ctx, cancel := dbContext()
	defer cancel()
	query = normalizeAuditQuery(query)
	where, args := auditWhere(query)
	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs a `+where, args...).Scan(&total); err != nil {
		return model.AuditLogPage{}, fmt.Errorf("count audit logs: %w", err)
	}
	args = append(args, query.PageSize, (query.Page-1)*query.PageSize)
	rows, err := p.pool.Query(ctx, `SELECT a.id,COALESCE(a.actor_id::text,''),a.actor_type,COALESCE(a.actor_email,u.username,''),a.action,a.resource_type,
		COALESCE(a.resource_id,''),COALESCE(a.request_id,''),COALESCE(a.method,''),COALESCE(a.path,''),COALESCE(a.remote_addr,''),
		COALESCE(a.user_agent,''),COALESCE(a.status_code,0),a.details,a.created_at
		FROM audit_logs a LEFT JOIN users u ON u.id=a.actor_id `+where+` ORDER BY a.created_at DESC,a.id DESC LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return model.AuditLogPage{}, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()
	items := make([]model.AuditLog, 0)
	for rows.Next() {
		var item model.AuditLog
		var details []byte
		if err := rows.Scan(&item.ID, &item.ActorID, &item.ActorType, &item.ActorEmail, &item.Action, &item.ResourceType, &item.ResourceID, &item.RequestID, &item.Method, &item.Path, &item.RemoteAddr, &item.UserAgent, &item.StatusCode, &details, &item.CreatedAt); err != nil {
			return model.AuditLogPage{}, fmt.Errorf("scan audit log: %w", err)
		}
		item.Details = map[string]any{}
		if len(details) > 0 && json.Unmarshal(details, &item.Details) != nil {
			item.Details = map[string]any{"_decode_error": "invalid details"}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return model.AuditLogPage{}, fmt.Errorf("iterate audit logs: %w", err)
	}
	return model.AuditLogPage{Items: items, Page: query.Page, PageSize: query.PageSize, Total: total}, nil
}

func auditWhere(query model.AuditLogQuery) (string, []any) {
	clauses := make([]string, 0, 6)
	args := make([]any, 0, 6)
	add := func(column string, value any) {
		args = append(args, value)
		clauses = append(clauses, column+"=$"+fmt.Sprint(len(args)))
	}
	if query.Action != "" {
		add("a.action", query.Action)
	}
	if query.ResourceType != "" {
		add("a.resource_type", query.ResourceType)
	}
	if query.ActorID != "" {
		add("a.actor_id", query.ActorID)
	}
	if query.RequestID != "" {
		add("a.request_id", query.RequestID)
	}
	if query.From != nil {
		args = append(args, *query.From)
		clauses = append(clauses, "a.created_at >= $"+fmt.Sprint(len(args)))
	}
	if query.To != nil {
		args = append(args, *query.To)
		clauses = append(clauses, "a.created_at <= $"+fmt.Sprint(len(args)))
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func (p *Postgres) PutPluginData(item model.PluginData) error {
	ctx, cancel := dbContext()
	defer cancel()
	_, err := p.pool.Exec(ctx, `INSERT INTO plugin_data(plugin_name,namespace,data_key,data_value,updated_at) VALUES($1,$2,$3,$4,NOW()) ON CONFLICT(plugin_name,namespace,data_key) DO UPDATE SET data_value=EXCLUDED.data_value,updated_at=NOW()`, item.PluginName, item.Namespace, item.Key, schemaDocument(item.Value))
	return err
}
func (p *Postgres) GetPluginData(pluginName, namespace, key string) (model.PluginData, error) {
	ctx, cancel := dbContext()
	defer cancel()
	var item model.PluginData
	err := p.pool.QueryRow(ctx, `SELECT plugin_name,namespace,data_key,data_value,updated_at FROM plugin_data WHERE plugin_name=$1 AND namespace=$2 AND data_key=$3`, pluginName, namespace, key).Scan(&item.PluginName, &item.Namespace, &item.Key, &item.Value, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.PluginData{}, ErrNotFound
	}
	return item, err
}

func (p *Postgres) CreatePlugin(item model.Plugin) error {
	ctx, cancel := dbContext()
	defer cancel()
	manifest := item.Manifest
	if len(manifest) == 0 {
		manifest = []byte(`{}`)
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO plugins(id,name,version,runtime,manifest,checksum,storage_path,enabled,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, item.ID, item.Name, item.Version, item.Runtime, manifest, item.Checksum, item.StoragePath, item.Enabled, item.CreatedAt, item.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return ErrConflict
		}
		return fmt.Errorf("create plugin: %w", err)
	}
	return nil
}

func (p *Postgres) GetPlugin(id string) (model.Plugin, error) {
	ctx, cancel := dbContext()
	defer cancel()
	var item model.Plugin
	var manifest []byte
	err := p.pool.QueryRow(ctx, `SELECT id,name,version,runtime,manifest,checksum,storage_path,enabled,created_at,updated_at FROM plugins WHERE id=$1`, id).
		Scan(&item.ID, &item.Name, &item.Version, &item.Runtime, &manifest, &item.Checksum, &item.StoragePath, &item.Enabled, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Plugin{}, ErrNotFound
	}
	item.Manifest = append([]byte(nil), manifest...)
	return item, err
}

func (p *Postgres) ListPlugins() []model.Plugin {
	ctx, cancel := dbContext()
	defer cancel()
	rows, err := p.pool.Query(ctx, `SELECT id,name,version,runtime,manifest,checksum,storage_path,enabled,created_at,updated_at FROM plugins ORDER BY name ASC,created_at DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	items := make([]model.Plugin, 0)
	for rows.Next() {
		var item model.Plugin
		var manifest []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.Version, &item.Runtime, &manifest, &item.Checksum, &item.StoragePath, &item.Enabled, &item.CreatedAt, &item.UpdatedAt); err == nil {
			item.Manifest = append([]byte(nil), manifest...)
			items = append(items, item)
		}
	}
	return items
}

func (p *Postgres) SetPluginEnabled(id string, enabled bool) error {
	ctx, cancel := dbContext()
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var name string
	if err := tx.QueryRow(ctx, `SELECT name FROM plugins WHERE id=$1`, id).Scan(&name); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if enabled {
		if _, err := tx.Exec(ctx, `UPDATE plugins SET enabled=FALSE,updated_at=NOW() WHERE name=$1 AND id<>$2`, name, id); err != nil {
			return err
		}
	}
	result, err := tx.Exec(ctx, `UPDATE plugins SET enabled=$2,updated_at=NOW() WHERE id=$1`, id, enabled)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

func (p *Postgres) DeletePlugin(id string) error {
	ctx, cancel := dbContext()
	defer cancel()
	result, err := p.pool.Exec(ctx, `DELETE FROM plugins WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete plugin: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPublicAPIsChecked reads only documentation data, never credentials or upstreams.
func (p *Postgres) ListPublicAPIsChecked() ([]model.API, error) {
	ctx, cancel := dbContext()
	defer cancel()
	rows, err := p.pool.Query(ctx, `SELECT method,path,auth_mode,request_schema,parameters_schema,public_visible,public_title,public_summary,public_category,enabled,published_at FROM apis WHERE public_visible=true AND enabled=true AND published_at IS NOT NULL ORDER BY public_category,public_title LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.API, 0)
	for rows.Next() {
		var a model.API
		var request, parameters []byte
		if err := rows.Scan(&a.Method, &a.Path, &a.AuthMode, &request, &parameters, &a.PublicVisible, &a.PublicTitle, &a.PublicSummary, &a.PublicCategory, &a.Enabled, &a.PublishedAt); err != nil {
			return nil, err
		}
		a.RequestSchema = schemaRawMessage(request)
		a.ParametersSchema = schemaRawMessage(parameters)
		result = append(result, a)
	}
	return result, rows.Err()
}
