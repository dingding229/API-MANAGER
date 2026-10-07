package account

import (
	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type cardStore interface {
	SaveCardBatch(context.Context, model.CardBatch) (model.CardBatch, error)
	CardBatch(context.Context, string) (model.CardBatch, error)
	CardBatches(context.Context) ([]model.CardBatch, error)
	RevokeCardBatch(context.Context, string) error
	RedeemCard(context.Context, string, string) (string, error)
}

func (s *Service) cards(w http.ResponseWriter, r *http.Request, u model.User) {
	st, ok := s.store.(cardStore)
	if !ok {
		write(w, 503, map[string]string{"error": "卡密服务不可用"})
		return
	}
	if r.URL.Path == "/account/v1/redeem" {
		if r.Method != "POST" {
			write(w, 405, nil)
			return
		}
		var req payload
		if !read(w, r, &req) {
			return
		}
		if !req.Confirm || len(req.Code) > 128 {
			write(w, 400, map[string]string{"error": "请填写并确认卡密"})
			return
		}
		if s.limiter != nil && !s.limiter.Allow("card-user:"+u.ID, 10, time.Minute, time.Now()) {
			write(w, 429, map[string]string{"error": "兑换过于频繁，请稍后再试"})
			return
		}
		hash := auth.HashAPIKey(strings.TrimSpace(req.Code))
		if e := audit.New(s.store, nil).RecordChecked(r.Context(), auditActor(u), r, "card.redeem.requested", "user", u.ID, 202, nil); e != nil {
			write(w, 503, nil)
			return
		}
		kind, e := st.RedeemCard(r.Context(), u.ID, hash)
		if e != nil {
			write(w, 409, map[string]string{"error": "卡密无效、已使用、已过期，或已有待生效续订"})
			return
		}
		write(w, 200, map[string]string{"kind": kind, "message": "兑换成功"})
		return
	}
	if r.URL.Path == "/account/v1/admin/cards" && r.Method == "GET" {
		list, e := st.CardBatches(r.Context())
		if e != nil {
			write(w, 503, nil)
			return
		}
		write(w, 200, list)
		return
	}
	if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/account/v1/admin/cards/") && strings.HasSuffix(r.URL.Path, "/items") {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/account/v1/admin/cards/"), "/items")
		if !assignmentUUID.MatchString(id) {
			write(w, 404, nil)
			return
		}
		b, e := st.CardBatch(r.Context(), id)
		if e != nil {
			write(w, 404, nil)
			return
		}
		write(w, 200, b)
		return
	}
	var req struct {
		Purpose         string    `json:"purpose"`
		Kind            string    `json:"kind"`
		AmountMicros    int64     `json:"amount_micros"`
		PlanID          string    `json:"plan_id"`
		Count           int       `json:"count"`
		ExpiresAt       time.Time `json:"expires_at"`
		OperationID     string    `json:"operation_id"`
		CurrentPassword string    `json:"current_password"`
		TurnstileToken  string    `json:"turnstile_token"`
		Confirm         bool      `json:"confirm"`
	}
	if !read(w, r, &req) {
		return
	}
	if !req.Confirm {
		write(w, 400, map[string]string{"error": "请确认卡密操作"})
		return
	}
	if e := s.reauthenticate(r, u, payload{CurrentPassword: req.CurrentPassword, TurnstileToken: req.TurnstileToken}); e != nil {
		write(w, 403, map[string]string{"error": "请重新验证管理员密码"})
		return
	}
	if e := audit.New(s.store, nil).RecordChecked(r.Context(), auditActor(u), r, "card.manage.requested", "card", "", 202, nil); e != nil {
		write(w, 503, nil)
		return
	}
	if r.URL.Path == "/account/v1/admin/cards" && r.Method == "POST" {
		if !assignmentUUID.MatchString(req.OperationID) || req.Count < 1 || req.Count > 100 || !req.ExpiresAt.After(time.Now()) || req.ExpiresAt.After(time.Now().AddDate(5, 0, 0)) || (req.Kind != "balance" && req.Kind != "plan") || (req.Kind == "balance" && (req.AmountMicros <= 0 || req.AmountMicros > 1000000000000000)) {
			write(w, 400, map[string]string{"error": "请核对类型、数量、面额与有效期"})
			return
		}
		core := struct {
			Kind, PlanID string
			Amount       int64
			Count        int
			Expiry       time.Time
		}{req.Kind, req.PlanID, req.AmountMicros, req.Count, req.ExpiresAt}
		raw, _ := json.Marshal(core)
		b := model.CardBatch{ID: ids.NewUUID(), CreatorID: u.ID, OperationID: req.OperationID, RequestHash: auth.HashAPIKey(string(raw)), Kind: req.Kind, AmountMicros: req.AmountMicros, Count: req.Count, ExpiresAt: req.ExpiresAt}
		if req.Kind == "plan" {
			bs, ok := s.store.(interface {
				Plans(context.Context, bool) ([]model.Plan, error)
			})
			if !ok {
				write(w, 503, nil)
				return
			}
			plans, e := bs.Plans(r.Context(), true)
			if e != nil {
				write(w, 503, nil)
				return
			}
			for _, p := range plans {
				if p.ID == req.PlanID {
					b.Plan, _ = json.Marshal(p)
				}
			}
			if len(b.Plan) == 0 {
				write(w, 400, map[string]string{"error": "套餐不存在"})
				return
			}
			b.AmountMicros = 0
		}
		for i := 0; i < req.Count; i++ {
			code := "cd_" + randomHex(24)
			if len(code) != 51 {
				write(w, 503, nil)
				return
			}
			encrypted, e := auth.EncryptSecret(s.key+":cards", code)
			if e != nil {
				write(w, 503, nil)
				return
			}
			b.Cards = append(b.Cards, model.RedeemCard{ID: ids.NewUUID(), CodeHash: auth.HashAPIKey(code), Prefix: code[:11], EncryptedCode: encrypted})
		}
		result, e := st.SaveCardBatch(r.Context(), b)
		if e != nil {
			write(w, 409, map[string]string{"error": "创建失败，请使用原操作编号重试"})
			return
		}
		s.outputCards(w, result, "unused")
		return
	}
	tail := strings.TrimPrefix(r.URL.Path, "/account/v1/admin/cards/")
	parts := strings.Split(tail, "/")
	if len(parts) != 2 || !assignmentUUID.MatchString(parts[0]) || r.Method != "POST" {
		write(w, 404, nil)
		return
	}
	if parts[1] == "reveal" {
		b, e := st.CardBatch(r.Context(), parts[0])
		if e != nil {
			write(w, 404, nil)
			return
		}
		if req.Purpose == "" {
			req.Purpose = "unused"
		}
		if req.Purpose != "used" && req.Purpose != "unused" && req.Purpose != "all" {
			write(w, 400, nil)
			return
		}
		s.outputCards(w, b, req.Purpose)
		return
	}
	if parts[1] == "revoke" && req.Confirm {
		if e := st.RevokeCardBatch(r.Context(), parts[0]); e != nil {
			write(w, 409, nil)
			return
		}
		write(w, 200, map[string]bool{"revoked": true})
		return
	}
	write(w, 400, nil)
}
func (s *Service) outputCards(w http.ResponseWriter, b model.CardBatch, purpose string) {
	codes := []string{}
	for _, c := range b.Cards {
		if purpose == "all" || (purpose == "used" && c.Status == "used") || (purpose == "unused" && c.Status == "unused") {
			v, e := auth.DecryptSecret(s.key+":cards", c.EncryptedCode)
			if e != nil {
				write(w, 503, map[string]string{"error": "卡密不可读取"})
				return
			}
			codes = append(codes, v)
		}
	}
	write(w, 200, map[string]any{"batch": b, "codes": codes})
}
