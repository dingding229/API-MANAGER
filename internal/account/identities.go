package account

import (
	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/user"
	"context"
	"net/http"
	"strings"
)

type identityStore interface {
	Identities(context.Context, string) ([]string, error)
	UnlinkIdentity(context.Context, string, string) error
	ActiveSession(context.Context, string, string) (bool, error)
}

func (s *Service) identities(w http.ResponseWriter, r *http.Request, u model.User) {
	st, ok := s.store.(identityStore)
	if !ok {
		write(w, 503, map[string]string{"error": "授权账号服务不可用"})
		return
	}
	if r.Method == "GET" {
		list, e := st.Identities(r.Context(), u.ID)
		if e != nil {
			write(w, 503, map[string]string{"error": "授权账号读取失败"})
			return
		}
		write(w, 200, list)
		return
	}
	if r.Method != "DELETE" {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var req payload
	if !read(w, r, &req) {
		return
	}
	if e := s.reauthenticateAction(r, u, req, "oauth"); e != nil {
		write(w, 403, map[string]string{"error": "请重新验证账号"})
		return
	}
	provider := strings.TrimPrefix(r.URL.Path, "/account/v1/identities/")
	if provider != "github" && provider != "google" {
		write(w, 400, map[string]string{"error": "授权服务无效"})
		return
	}
	if e := st.UnlinkIdentity(r.Context(), u.ID, provider); e != nil {
		write(w, 409, map[string]string{"error": "无法解绑授权账号"})
		return
	}
	s.users.RecordAudit(auditActor(u), r, "account.identity.unlink", "identity", provider, 200, nil)
	write(w, 200, map[string]bool{"unlinked": true})
}

func (s *Service) sessions(w http.ResponseWriter, r *http.Request, u model.User) {
	token, _ := auth.SessionToken(r)
	if r.Method == "GET" && r.URL.Path == "/account/v1/sessions" {
		list, err := s.users.Sessions(u.ID)
		if err != nil {
			write(w, 503, map[string]string{"error": "会话读取失败"})
			return
		}
		current, e := s.users.CurrentSession(token)
		if e != nil {
			write(w, 503, map[string]string{"error": "当前会话读取失败"})
			return
		}
		out := []map[string]any{}
		for _, v := range list {
			out = append(out, map[string]any{"id": v.ID, "device": v.Device, "login_ip": v.LoginIP, "last_seen_at": v.LastSeenAt, "created_at": v.CreatedAt, "current": v.ID == current.ID})
		}
		write(w, 200, out)
		return
	}
	if r.Method == "DELETE" {
		var req payload
		if !read(w, r, &req) {
			return
		}
		if !req.Confirm {
			write(w, 400, map[string]string{"error": "请确认退出会话"})
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/account/v1/sessions/")
		list, err := s.users.Sessions(u.ID)
		if err != nil {
			write(w, 503, map[string]string{"error": "会话服务不可用"})
			return
		}
		owned := false
		for _, v := range list {
			if v.ID == id {
				owned = true
			}
		}
		if !owned {
			write(w, 404, map[string]string{"error": "会话不存在"})
			return
		}
		if err = audit.New(s.store, nil).RecordChecked(r.Context(), auditActor(u), r, "user.session.revoke.requested", "user_session", id, 202, nil); err != nil {
			write(w, 503, map[string]string{"error": "审计不可用，会话未退出"})
			return
		}
		current, _ := s.users.CurrentSession(token)
		if _, err = s.users.RevokeSession(u.ID, id); err != nil {
			write(w, 503, map[string]string{"error": "会话退出失败"})
			return
		}
		if current.ID == id {
			user.ClearSessionCookie(w, r, s.production)
		}
		write(w, 200, map[string]bool{"revoked": true})
		return
	}
	write(w, 405, map[string]string{"error": "method not allowed"})
}
