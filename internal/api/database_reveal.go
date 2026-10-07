package api

import (
	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type databaseRecordStore interface {
	DatabaseRecord(context.Context, string, map[string]json.RawMessage) (map[string]any, error)
}

func databaseRecoverableFields(table string, row map[string]any, master string) map[string]string {
	out := map[string]string{}
	switch table {
	case "api_credentials":
		out["encrypted_key"] = master
	case "site_settings":
		out["encrypted_smtp_password"] = master
	case "authentication_settings":
		out["encrypted_secrets"] = master + ":authentication"
	case "plugin_settings":
		out["encrypted_settings"] = master + ":plugin-settings:" + fmt.Sprint(row["plugin_id"])
	case "redeem_cards":
		out["encrypted_code"] = master + ":cards"
	case "version_check_settings":
		out["encrypted_token"] = master + ":version"
	case "plugin_response_cache":
		out["encrypted_response"] = master + ":plugin-cache:" + fmt.Sprint(row["cache_key"])
	}
	return out
}
func (a *Admin) databaseReveal(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, 405, nil)
		return
	}
	var q struct {
		CurrentPassword string                     `json:"current_password"`
		Table           string                     `json:"table"`
		Primary         map[string]json.RawMessage `json:"primary"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	if !a.globalAPIScope(r) || !a.hasPermission(r, "database.manage") || !a.databasePassword(r, q.CurrentPassword) {
		writeJSON(w, 403, map[string]string{"error": "请验证当前超级管理员密码"})
		return
	}
	db, ok := a.store.(databaseRecordStore)
	if !ok {
		writeJSON(w, 503, nil)
		return
	}
	row, e := db.DatabaseRecord(r.Context(), q.Table, q.Primary)
	if e != nil {
		writeJSON(w, 404, map[string]string{"error": "记录不存在或主键无效"})
		return
	}
	keys := databaseRecoverableFields(q.Table, row, a.credentialEncryptionKey)
	result := map[string]any{}
	actor, _ := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	if e = a.auditor.RecordChecked(r.Context(), actor, r, "database.secret.reveal.requested", "database", q.Table, 202, nil); e != nil {
		writeJSON(w, 503, nil)
		return
	}
	for column, key := range keys {
		raw, _ := row[column].(string)
		if raw == "" {
			result[column] = "未设置"
			continue
		}
		plain, e := auth.DecryptSecret(key, raw)
		if e != nil {
			writeJSON(w, 409, map[string]string{"error": "加密字段无法读取，请核对部署密钥"})
			return
		}
		if column == "encrypted_secrets" || column == "encrypted_settings" || column == "encrypted_response" {
			var structured any
			decoder := json.NewDecoder(strings.NewReader(plain))
			decoder.UseNumber()
			if decoder.Decode(&structured) == nil {
				result[column] = structured
			} else {
				result[column] = plain
			}
		} else {
			result[column] = plain
		}

	}
	if len(result) == 0 {
		writeJSON(w, 400, map[string]string{"error": "此记录没有可还原的加密字段。登录密码、会话、验证器绑定和摘要不能在此查看。"})
		return
	}
	a.recordAudit(r, "database.secret.reveal", "database", q.Table, 200, map[string]any{"fields": len(result), "primary": q.Primary})
	writeJSON(w, 200, map[string]any{"fields": result, "message": "内容仅临时显示，请勿分享"})
}
