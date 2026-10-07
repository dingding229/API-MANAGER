package store

import (
	"api-manager/internal/model"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"reflect"
	"strings"
	"time"
)

const MaxDatabaseSnapshotBytes = 16 << 20

// Dependency order is fixed. Request/file identifiers never become arbitrary SQL.
var databaseTables = []string{"tenants", "users", "roles", "permissions", "user_roles", "role_permissions", "plugins", "apis", "api_credentials", "api_releases", "api_routes", "audit_logs", "plugin_data", "admin_bootstrap", "site_settings", "plans", "wallets", "wallet_ledger", "subscriptions", "usage_windows", "api_charges", "user_call_logs", "account_security", "external_identities", "authentication_settings", "retired_role_settings", "card_batches", "redeem_cards", "plugin_settings", "version_check_settings", "user_sessions", "password_resets", "account_verifications", "api_test_tickets", "plugin_response_cache"}
var transientDatabaseTables = map[string]bool{"user_sessions": true, "password_resets": true, "account_verifications": true, "api_test_tickets": true, "plugin_response_cache": true}

// #nosec G101 -- Static Chinese labels for table names, not credentials or secret values.
var databaseDescriptions = map[string]string{"users": "账号资料", "roles": "角色", "permissions": "权限目录", "user_roles": "用户角色", "role_permissions": "角色权限", "apis": "接口配置", "api_credentials": "调用凭证", "api_routes": "发布路由", "api_releases": "接口发布记录", "plugins": "插件", "plugin_data": "插件数据", "audit_logs": "审计日志", "user_call_logs": "调用日志", "wallets": "余额账户", "wallet_ledger": "余额流水", "subscriptions": "用户套餐", "plans": "套餐", "usage_windows": "套餐用量", "api_charges": "调用结算", "card_batches": "卡密批次", "redeem_cards": "卡密", "site_settings": "网站设置", "authentication_settings": "注册与登录设置", "account_security": "账号安全", "external_identities": "授权账号", "user_sessions": "登录会话", "plugin_response_cache": "插件缓存", "plugin_settings": "插件设置", "version_check_settings": "更新设置", "tenants": "租户预留", "admin_bootstrap": "首次注册状态", "password_resets": "密码找回请求", "account_verifications": "验证请求", "api_test_tickets": "在线测试授权", "retired_role_settings": "历史角色设置"}

func allowedDatabaseTable(name string) bool {
	for _, v := range databaseTables {
		if v == name {
			return true
		}
	}
	return false
}
func databaseIdentifier(name string) string { return pgx.Identifier{"public", name}.Sanitize() }
func (p *Postgres) DatabaseTables(ctx context.Context) ([]model.DatabaseTable, error) {
	rows, e := p.pool.Query(ctx, `SELECT c.relname,GREATEST(c.reltuples,0)::bigint,pg_total_relation_size(c.oid),pg_indexes_size(c.oid),
 ARRAY(SELECT a.attname FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum),
 ARRAY(SELECT a.atttypid::regtype::text FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum),
 ARRAY(SELECT a.attname FROM pg_index i CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY k(num,pos) JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum=k.num WHERE i.indrelid=c.oid AND i.indisprimary ORDER BY k.pos)
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind='r' AND c.relname=ANY($1::text[]) ORDER BY array_position($1::text[],c.relname::text)`, databaseTables)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := make([]model.DatabaseTable, 0, len(databaseTables))
	for rows.Next() {
		var info model.DatabaseTable
		if e = rows.Scan(&info.Name, &info.ApproxRows, &info.Bytes, &info.IndexBytes, &info.Columns, &info.Types, &info.Primary); e != nil {
			return nil, e
		}
		info.Description = databaseDescriptions[info.Name]
		out = append(out, info)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if len(out) != len(databaseTables) {
		return nil, errors.New("database schema incomplete")
	}
	return out, nil
}

func sensitiveDatabaseField(k string) bool {
	k = strings.ToLower(strings.ReplaceAll(k, "-", "_"))
	for _, v := range []string{"password", "secret", "encrypted", "token", "authorization", "cookie", "recovery", "binding"} {
		if strings.Contains(k, v) {
			return true
		}
	}
	return strings.HasSuffix(k, "_hash") || k == "hash" || k == "key_hash" || k == "code_hash" || k == "key" || k == "api_key" || k == "payload" || k == "request_digest"
}
func redactDatabaseValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if sensitiveDatabaseField(k) {
				x[k] = "[已隐藏]"
			} else {
				x[k] = redactDatabaseValue(v)
			}
		}
		return x
	case []any:
		for i, v := range x {
			x[i] = redactDatabaseValue(v)
		}
		return x
	default:
		return v
	}
}
func (p *Postgres) DatabaseRows(ctx context.Context, table string, page, size int) (model.DatabaseRows, error) {
	result := model.DatabaseRows{Table: table, Page: page, PageSize: size, Items: []map[string]any{}}
	if !allowedDatabaseTable(table) || page < 1 || size < 1 || size > 50 || (page-1)*size > 10000 {
		return result, ErrNotFound
	}
	tables, e := p.DatabaseTables(ctx)
	if e != nil {
		return result, e
	}
	var info model.DatabaseTable
	for _, v := range tables {
		if v.Name == table {
			info = v
		}
	}
	result.Columns = info.Columns
	order := []string{}
	for _, key := range info.Primary {
		order = append(order, pgx.Identifier{key}.Sanitize())
	}
	if len(order) == 0 {
		order = []string{"ctid"}
	}
	// #nosec G202 -- table is a fixed allowlist; ordering identifiers come from PostgreSQL schema metadata and are quoted with pgx.Identifier. Limits are parameters.
	rows, e := p.pool.Query(ctx, `SELECT row_to_json(t) FROM (SELECT * FROM `+databaseIdentifier(table)+` ORDER BY `+strings.Join(order, ",")+` LIMIT $1 OFFSET $2) t`, size+1, (page-1)*size)
	if e != nil {
		return result, e
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		if e = rows.Scan(&data); e != nil {
			return result, e
		}
		if len(result.Items) == size {
			result.HasMore = true
			continue
		}
		if len(data) > 1<<20 {
			result.Items = append(result.Items, map[string]any{"说明": "此条记录较大，请使用数据库备份查看"})
			continue
		}
		var v map[string]any
		if e = json.Unmarshal(data, &v); e != nil {
			return result, e
		}
		redactDatabaseValue(v)
		result.Items = append(result.Items, v)
	}
	return result, rows.Err()
}
func (p *Postgres) databaseSchema(ctx context.Context) (string, []model.DatabaseTable, error) {
	tables, e := p.DatabaseTables(ctx)
	if e != nil {
		return "", nil, e
	}
	rows, e := p.pool.Query(ctx, `SELECT table_name,column_name,udt_name,is_nullable,COALESCE(column_default,'') FROM information_schema.columns WHERE table_schema='public' ORDER BY table_name,ordinal_position`)
	if e != nil {
		return "", nil, e
	}
	defer rows.Close()
	sum := sha256.New()
	for rows.Next() {
		var t, c, u, n, d string
		if e = rows.Scan(&t, &c, &u, &n, &d); e != nil {
			return "", nil, e
		}
		if allowedDatabaseTable(t) {
			fmt.Fprintf(sum, "%s\x00%s\x00%s\x00%s\x00%s\n", t, c, u, n, d)
		}
	}
	return hex.EncodeToString(sum.Sum(nil)), tables, rows.Err()
}
func (p *Postgres) DatabaseSnapshot(ctx context.Context) (model.DatabaseSnapshot, error) {
	schema, tables, e := p.databaseSchema(ctx)
	if e != nil {
		return model.DatabaseSnapshot{}, e
	}
	tx, e := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return model.DatabaseSnapshot{}, e
	}
	defer tx.Rollback(ctx)
	snapshot := model.DatabaseSnapshot{Format: 2, Schema: schema, CreatedAt: time.Now().UTC()}
	total := 0
	for _, info := range tables {
		block := model.DatabaseSnapshotTable{Name: info.Name, Columns: info.Columns, Rows: []json.RawMessage{}, Nulls: [][]string{}}
		if !transientDatabaseTables[info.Name] {
			nulls := []string{}
			for _, col := range info.Columns {
				nulls = append(nulls, "CASE WHEN t."+pgx.Identifier{col}.Sanitize()+" IS NULL THEN '"+strings.ReplaceAll(col, "'", "''")+"' END")
			}
			// #nosec G202 -- identifier comes from the fixed databaseTables allowlist and is quoted, never from SQL supplied by an upload.
			rows, e := tx.Query(ctx, `SELECT row_to_json(t),array_remove(ARRAY[`+strings.Join(nulls, ",")+`]::text[],NULL) FROM `+databaseIdentifier(info.Name)+` t`)
			if e != nil {
				return snapshot, e
			}
			for rows.Next() {
				var raw []byte
				var sqlNulls []string
				if e = rows.Scan(&raw, &sqlNulls); e != nil {
					rows.Close()
					return snapshot, e
				}
				total += len(raw)
				if total > MaxDatabaseSnapshotBytes {
					rows.Close()
					return snapshot, errors.New("数据库较大，请使用服务器备份脚本")
				}
				block.Rows = append(block.Rows, append(json.RawMessage(nil), raw...))
				block.Nulls = append(block.Nulls, sqlNulls)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return snapshot, e
			}
		}
		snapshot.Tables = append(snapshot.Tables, block)
	}
	return snapshot, tx.Commit(ctx)
}
func (p *Postgres) ValidateDatabaseSnapshot(ctx context.Context, snapshot model.DatabaseSnapshot) error {
	schema, tables, e := p.databaseSchema(ctx)
	if e != nil {
		return e
	}
	if snapshot.Format != 2 || snapshot.Schema != schema || len(snapshot.Tables) != len(tables) {
		return ErrConflict
	}
	total := 0
	for i, table := range snapshot.Tables {
		if table.Name != tables[i].Name || !reflect.DeepEqual(table.Columns, tables[i].Columns) {
			return ErrConflict
		}
		if transientDatabaseTables[table.Name] && len(table.Rows) > 0 {
			return ErrConflict
		}
		if len(table.Nulls) != len(table.Rows) {
			return ErrConflict
		}
		for index, raw := range table.Rows {
			total += len(raw)
			if total > MaxDatabaseSnapshotBytes {
				return ErrConflict
			}
			var fields map[string]json.RawMessage
			if e = json.Unmarshal(raw, &fields); e != nil || len(fields) != len(table.Columns) {
				return ErrConflict
			}
			for _, col := range table.Nulls[index] {
				if value, ok := fields[col]; !ok || string(value) != "null" {
					return ErrConflict
				}
			}
			for _, col := range table.Columns {
				if _, ok := fields[col]; !ok {
					return ErrConflict
				}
			}
		}
	}
	return nil
}
func (p *Postgres) RestoreDatabaseSnapshot(ctx context.Context, snapshot model.DatabaseSnapshot) error {
	if e := p.ValidateDatabaseSnapshot(ctx, snapshot); e != nil {
		return e
	}
	_, metadata, e := p.databaseSchema(ctx)
	if e != nil {
		return e
	}
	tx, e := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, `SET LOCAL lock_timeout='5s'; SET LOCAL statement_timeout='60s'`); e != nil {
		return e
	}
	names := []string{}
	for _, table := range databaseTables {
		names = append(names, databaseIdentifier(table))
	}
	// #nosec G202 -- the complete table list is fixed, quoted, and excludes schema_migrations; no CASCADE or user SQL is accepted.
	if _, e = tx.Exec(ctx, `TRUNCATE `+strings.Join(names, ",")); e != nil {
		return e
	}

	for tableIndex, table := range snapshot.Tables {
		cols := []string{}
		for _, v := range table.Columns {
			cols = append(cols, pgx.Identifier{v}.Sanitize())
		}
		for offset := 0; offset < len(table.Rows); offset += 100 {
			end := offset + 100
			if end > len(table.Rows) {
				end = len(table.Rows)
			}
			batch := []map[string]any{}
			for i := offset; i < end; i++ {
				batch = append(batch, map[string]any{"values": table.Rows[i], "nulls": table.Nulls[i]})
			}
			data, e := json.Marshal(batch)
			if e != nil {
				return e
			}
			// #nosec G202 -- table/columns were compared to the fixed live schema before the transaction. Uploaded values are a JSON parameter and never executable SQL.
			values := []string{}
			for i, col := range table.Columns {
				quoted := pgx.Identifier{col}.Sanitize()
				kind := metadata[tableIndex].Types[i]
				if kind == "json" || kind == "jsonb" {
					values = append(values, "CASE WHEN (entry->'nulls')::jsonb ? '"+strings.ReplaceAll(col, "'", "''")+"' THEN NULL ELSE COALESCE(r."+quoted+",'null'::"+kind+") END")
				} else {
					values = append(values, "r."+quoted)
				}
			}
			if _, e = tx.Exec(ctx, `INSERT INTO `+databaseIdentifier(table.Name)+` (`+strings.Join(cols, ",")+`) SELECT `+strings.Join(values, ",")+` FROM json_array_elements($1::json) entry CROSS JOIN LATERAL json_populate_record(NULL::`+databaseIdentifier(table.Name)+`,entry->'values') r`, string(data)); e != nil {
				return e
			}
		}
	}
	var admins int
	if e = tx.QueryRow(ctx, `SELECT COUNT(DISTINCT u.id) FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.status='active' AND u.deleted_at IS NULL AND u.password_hash IS NOT NULL AND u.password_hash<>'' AND r.name='super_admin' AND r.deleted_at IS NULL`).Scan(&admins); e != nil || admins < 1 {
		return ErrConflict
	}
	if _, e = tx.Exec(ctx, `UPDATE users SET auth_revision=auth_revision+1; UPDATE admin_bootstrap SET consumed=TRUE; UPDATE account_security SET totp_pending='',pending_expires_at='1970-01-01',recovery_hashes='[]'`); e != nil {
		return e
	}
	// Reset the serial sequence before writing a restore receipt, including a new installation.
	if _, e = tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence('public.audit_logs','id'),GREATEST(COALESCE((SELECT MAX(id) FROM audit_logs),0),COALESCE(pg_sequence_last_value(pg_get_serial_sequence('public.audit_logs','id')::regclass),0),1),TRUE)`); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,action,resource_type,details,status_code) VALUES('system','database.restore.commit','database',jsonb_build_object('snapshot_created_at',$1::text),200)`, snapshot.CreatedAt.Format(time.RFC3339)); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// DatabaseRecord only addresses an existing row through the table's complete primary key.
func (p *Postgres) DatabaseRecord(ctx context.Context, table string, primary map[string]json.RawMessage) (map[string]any, error) {
	if !allowedDatabaseTable(table) {
		return nil, ErrNotFound
	}
	tables, e := p.DatabaseTables(ctx)
	if e != nil {
		return nil, e
	}
	var info model.DatabaseTable
	for _, v := range tables {
		if v.Name == table {
			info = v
		}
	}
	if len(info.Primary) == 0 || len(primary) != len(info.Primary) {
		return nil, ErrNotFound
	}
	where := []string{}
	for _, col := range info.Primary {
		v, ok := primary[col]
		if !ok || len(v) > 4096 {
			return nil, ErrNotFound
		}
		where = append(where, "t."+pgx.Identifier{col}.Sanitize()+" IS NOT DISTINCT FROM k."+pgx.Identifier{col}.Sanitize())
	}
	key, e := json.Marshal(primary)
	if e != nil {
		return nil, e
	}
	var raw []byte
	// #nosec G202 -- table is a fixed allowlist, primary identifiers come from trusted schema metadata, and all supplied values are a JSON parameter.
	e = p.pool.QueryRow(ctx, `SELECT row_to_json(t) FROM `+databaseIdentifier(table)+` t CROSS JOIN json_populate_record(NULL::`+databaseIdentifier(table)+`,$1::json) k WHERE `+strings.Join(where, " AND ")+` LIMIT 1`, string(key)).Scan(&raw)
	if e != nil {
		return nil, e
	}
	if len(raw) > 1<<20 {
		return nil, ErrConflict
	}
	var row map[string]any
	e = json.Unmarshal(raw, &row)
	return row, e
}
