package api

import (
	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/user"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type sessionAdministration interface {
	Sessions(string) ([]model.Session, error)
	RevokeSession(string, string) (bool, error)
	CurrentSession(string) (model.Session, error)
}

func (a *Admin) manageSessions(w http.ResponseWriter, r *http.Request) {
	st, ok := a.userManager.(sessionAdministration)
	if !ok {
		writeJSON(w, 503, map[string]string{"error": "会话服务不可用"})
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/v1/users/"), "/")
	if len(parts) < 2 || parts[1] != "sessions" {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	id := parts[0]
	actor, _ := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	if id!=actor.ID && !a.userManager.Can(actor.ID,"user.sessions.manage"){writeJSON(w,403,map[string]string{"error":"无权管理该用户的会话"});return}
 target, err := a.store.GetUserByID(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "用户不存在"})
		return
	}
	roles := a.store.ListUserRoles(id)
	super := false
	for _, role := range roles {
		if role == "super_admin" {
			super = true
		}
	}
	if id != actor.ID && (!a.userManager.Can(actor.ID, "user.sessions.manage") || (super && !a.userManager.Can(actor.ID, "*"))) {
		writeJSON(w, 403, map[string]string{"error": "无权管理该用户的会话"})
		return
	}
	if r.Method == http.MethodGet && len(parts) == 2 {
		sessions, err := st.Sessions(id)
		if err != nil {
			writeJSON(w, 503, map[string]string{"error": "无法读取登录会话"})
			return
		}
		token, _ := auth.SessionToken(r)
		current, _ := st.CurrentSession(token)
		list := []map[string]any{}
		for _, s := range sessions {
			list = append(list, map[string]any{"id": s.ID, "user_id": s.UserID, "username": target.Username, "created_at": func() any {
				if s.LoginIP == "" && s.UserAgent == "" {
					return nil
				}
				return s.CreatedAt
			}(), "last_seen_at": s.LastSeenAt, "expires_at": s.ExpiresAt, "login_ip": s.LoginIP, "last_ip": s.LastIP, "peer_ip": s.PeerIP, "ip_source": s.IPSource, "device": s.Device, "user_agent": s.UserAgent, "current": s.ID == current.ID})
		}
		writeJSON(w, 200, list)
		return
	}
	if r.Method != http.MethodDelete || len(parts) != 3 {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	var body struct {
		Confirm bool `json:"confirm"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || !body.Confirm || decoder.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "请确认退出此会话"})
		return
	}
	if err := a.auditor.RecordChecked(r.Context(), actor, r, "user.session.revoke.requested", "user_session", parts[2], 202, map[string]any{"user_id": id}); err != nil {
		writeJSON(w, 503, map[string]string{"error": "审计不可用，会话未退出"})
		return
	}
	token, _ := auth.SessionToken(r)
	current, _ := st.CurrentSession(token)
	deleted, err := st.RevokeSession(id, parts[2])
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "会话退出失败"})
		return
	}
	if !deleted {
		writeJSON(w, 404, map[string]string{"error": "会话已退出或不存在"})
		return
	}
	a.recordAudit(r, "user.session.revoke", "user_session", parts[2], 200, map[string]any{"user_id": id})
	writeJSON(w, 200, map[string]bool{"revoked": true, "current": current.ID == parts[2]})
}

var _ sessionAdministration = (*user.Service)(nil)


## 插件设置接口

插件版本可在 `manifest.yaml` 声明设置字段。例如：

```yaml
settings_schema:
  type: object
  additionalProperties: false
  properties:
    region:
      type: string
      title: 地区
      default: CN
      enum: [CN, US, JP]
    timeout:
      type: integer
      title: 超时秒数
      minimum: 1
      maximum: 30
      default: 5
    access_token:
      type: string
      title: 访问令牌
      writeOnly: true
  required: [region, timeout]
```

Schema 最大 32 KiB，设置 JSON 最大 32 KiB、深度最大 16，必须声明 `type: object` 和 `additionalProperties: false`。不支持 `$ref`、外部资源引用、原型字段。字段标题与说明是纯文本，不支持 HTML。密码或令牌须使用字符串类型并标记 `writeOnly: true`，不得把实际秘密放入清单默认值。

每次 `handle` 请求 JSON 新增可选 `settings` 对象，只包含当前插件版本的配置。未声明设置的旧插件无需变更；请忽略未知请求字段。插件只能从此对象读取设置，不获得数据库连接、任意 SQL、环境变量或文件系统权限。配置仅注入匹配插件，不注入公开 API 文档。

后台配置接口（使用管理会话与 `plugin.manage` 权限）：

- `GET /admin/v1/plugins/{plugin_id}/settings` 返回 `schema`、`values`、`version` 和 `secret_fields_set`，不返回 `writeOnly` 的秘密值。
- `PUT /admin/v1/plugins/{plugin_id}/settings` 提交 `{ "version": 版本, "changes": { "字段": 值 }, "remove_fields": [] }`。省略字段保留旧值；嵌套对象按字段合并，数组中的对象按索引合并以保留未回显的秘密字段。调整包含秘密的数组顺序时，应明确重新填写对应秘密。填写 `remove_fields` 显式删除字段，最终对象仍须通过 Schema 校验。
- 设置按插件版本 ID 隔离、加密持久化。后台保存后生效，无需重新上传 WASM；缓存标识包含配置版本，因此旧缓存不会作为新配置结果返回。
- 必填设置没有值或默认值时，插件不能启用。先保存合法设置再启用。新版本应包含迁移兼容的字段或让管理员在启用前补齐设置。

客户端禁止把配置或配置中的秘密复制到响应、调用日志、错误消息或公开说明中。主程序限制插件权限，但无法替插件作者判断哪些业务输出应保密。
