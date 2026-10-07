package account

import (
	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"context"
	"net/http"
	"strings"
)

func (s *Service) keyIPPolicy(w http.ResponseWriter, r *http.Request, u model.User, admin bool) {
	prefix := "/account/v1/keys/"
	if admin {
		prefix = "/account/v1/admin/keys/"
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/ip-policy")
	key, e := s.store.GetCredential(id)
	if e != nil || (!admin && key.OwnerUserID != u.ID) {
		write(w, 404, nil)
		return
	}
	var req payload
	if !read(w, r, &req) {
		return
	}
	if !req.Confirm || key.Revoked {
		write(w, 400, nil)
		return
	}
	if e = s.reauthenticate(r, u, req); e != nil {
		write(w, 403, map[string]string{"error": "请重新验证账号"})
		return
	}
	ranges, e := auth.NormalizeIPRanges(req.AllowedIPRanges)
	if e != nil {
		write(w, 400, map[string]string{"error": e.Error()})
		return
	}
	if e = audit.New(s.store, nil).RecordChecked(r.Context(), auditActor(u), r, "credential.ip_policy.update.requested", "credential", id, 202, map[string]any{"range_count": len(ranges)}); e != nil {
		write(w, 503, nil)
		return
	}
	st, ok := s.store.(interface {
		SetCredentialIPRanges(context.Context, string, string, []string) (model.Credential, error)
	})
	if !ok {
		write(w, 503, nil)
		return
	}
	key, e = st.SetCredentialIPRanges(r.Context(), key.ID, key.OwnerUserID, ranges)
	if e != nil {
		write(w, 409, map[string]string{"error": "凭据已变化或已吊销，请刷新后重试"})
		return
	}
	write(w, 200, key)
}
