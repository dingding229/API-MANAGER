package api

import (
	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
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
		Locator         string                     `json:"locator"`
		Primary         map[string]json.RawMessage `json:"primary"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	if !a.globalAPIScope(r) || !a.hasPermission(r, "database.manage") || !a.databasePassword(r, q.CurrentPassword) {
		writeJSON(w, 403, map[string]string{"error": "请使用密码或通行密钥验证当前超级管理员身份"})
		return
	}
	db, ok := a.store.(databaseRecordStore)
	if !ok {
		writeJSON(w, 503, nil)
		return
	}
	if q.Locator != "" {
		primary, e := a.databaseLocatorPrimary(r, q.Table, q.Locator)
		if e != nil {
			writeJSON(w, 403, map[string]string{"error": "记录查看授权已失效，请刷新后重试"})
			return
		}
		q.Primary = primary
	}
	row, e := db.DatabaseRecord(r.Context(), q.Table, q.Primary)
	if e != nil {
		writeJSON(w, 404, map[string]string{"error": "记录不存在或主键无效"})
		return
	}
	keys := databaseRecoverableFields(q.Table, row, a.credentialEncryptionKey)
	result := store.DatabaseHiddenValues(row)
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
	a.recordAudit(r, "database.secret.reveal", "database", q.Table, 200, map[string]any{"fields": len(result)})
	writeJSON(w, 200, map[string]any{"fields": result, "message": "内容仅临时显示。密码摘要不是原密码；没有解密入口的字段展示数据库保存值。请勿分享"})
}

type databaseLocator struct {
	Table, SessionHash string
	Primary            map[string]json.RawMessage
	Expires            time.Time
}

func (a *Admin) attachDatabaseLocators(r *http.Request, data *model.DatabaseRows) error {
	token, _ := auth.SessionToken(r)
	for i, primary := range data.Primary {
		if len(primary) == 0 || i >= len(data.Items) {
			continue
		}
		raw, e := json.Marshal(databaseLocator{Table: data.Table, SessionHash: auth.HashAPIKey(token), Primary: primary, Expires: time.Now().Add(10 * time.Minute)})
		if e != nil {
			return e
		}
		locator, e := auth.EncryptSecret(a.credentialEncryptionKey+":database-locator", string(raw))
		if e != nil {
			return e
		}
		data.Items[i]["_record_locator"] = locator
	}
	return nil
}
func (a *Admin) databaseLocatorPrimary(r *http.Request, table, locator string) (map[string]json.RawMessage, error) {
	if len(locator) > 8192 {
		return nil, errors.New("invalid record locator")
	}
	raw, e := auth.DecryptSecret(a.credentialEncryptionKey+":database-locator", locator)
	if e != nil {
		return nil, e
	}
	var v databaseLocator
	if json.Unmarshal([]byte(raw), &v) != nil {
		return nil, errors.New("invalid record locator")
	}
	token, _ := auth.SessionToken(r)
	if v.Table != table || v.SessionHash != auth.HashAPIKey(token) || v.Expires.Before(time.Now()) {
		return nil, errors.New("expired record locator")
	}
	return v.Primary, nil
}
