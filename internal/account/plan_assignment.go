package account

import (
	"api-manager/internal/audit"
	"api-manager/internal/model"
	"context"
	"net/http"
	"regexp"
	"strings"
)

var assignmentUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (s *Service) assignPlan(w http.ResponseWriter, r *http.Request, u model.User) {
	var req struct {
		UserID          string `json:"user_id"`
		PlanID          string `json:"plan_id"`
		OperationID     string `json:"operation_id"`
		Note            string `json:"note"`
		Confirm         bool   `json:"confirm"`
		CurrentPassword string `json:"current_password"`
		TurnstileToken  string `json:"turnstile_token"`
	}
	if !read(w, r, &req) {
		return
	}
	if !req.Confirm || !assignmentUUID.MatchString(req.OperationID) || !assignmentUUID.MatchString(req.UserID) || !assignmentUUID.MatchString(req.PlanID) || strings.TrimSpace(req.Note) == "" || len(req.Note) > 512 {
		write(w, 400, map[string]string{"error": "用户、套餐、原因与替换确认必填"})
		return
	}
	if e := s.reauthenticate(r, u, payload{CurrentPassword: req.CurrentPassword, TurnstileToken: req.TurnstileToken}); e != nil {
		write(w, 403, map[string]string{"error": "请重新验证管理员密码"})
		return
	}
	st, ok := s.store.(interface {
		AssignPlan(context.Context, string, string, string, string) (model.Subscription, error)
	})
	if !ok {
		write(w, 503, map[string]string{"error": "套餐绑定服务不可用"})
		return
	}
	if e := audit.New(s.store, nil).RecordChecked(r.Context(), auditActor(u), r, "billing.plan.assign.requested", "user", req.UserID, 202, map[string]any{"plan_id": req.PlanID, "note": req.Note, "operation_id": req.OperationID}); e != nil {
		write(w, 503, map[string]string{"error": "审计不可用，未绑定套餐"})
		return
	}
	result, e := st.AssignPlan(r.Context(), req.UserID, req.PlanID, "grant:"+req.UserID+":"+req.OperationID, req.Note)
	if e != nil {
		write(w, 409, map[string]string{"error": "套餐绑定失败，请核对用户及操作编号"})
		return
	}
	s.users.RecordAudit(auditActor(u), r, "billing.plan.assign", "user", req.UserID, 200, map[string]any{"subscription_id": result.ID, "plan_id": req.PlanID, "operation_id": req.OperationID})
	write(w, 200, result)
}
