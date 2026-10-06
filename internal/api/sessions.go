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
	if id != actor.ID && !a.userManager.Can(actor.ID, "user.sessions.manage") {
		writeJSON(w, 403, map[string]string{"error": "无权管理该用户的会话"})
		return
	}
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
