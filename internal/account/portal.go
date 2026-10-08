package account

import (
	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *Service) portal(w http.ResponseWriter, r *http.Request, u model.User) {
	billing, ok := s.store.(store.BillingStore)
	if !ok {
		write(w, 503, map[string]string{"error": "账户结算服务不可用"})
		return
	}
	path := r.URL.Path
	if path == "/account/v1/wallet" && r.Method == "GET" {
		wallet, e := billing.Wallet(r.Context(), u.ID)
		if e != nil {
			write(w, 503, map[string]string{"error": "余额不可读取"})
			return
		}
		subscription, e := billing.Subscription(r.Context(), u.ID)
		if e != nil {
			write(w, 503, map[string]string{"error": "套餐不可读取"})
			return
		}
		usage := map[string]int64{}
		if st, ok := s.store.(interface {
			Usage(context.Context, string, time.Time) (map[string]int64, error)
		}); ok {
			usage, e = st.Usage(r.Context(), u.ID, time.Now())
			if e != nil {
				write(w, 503, map[string]string{"error": "额度不可读取"})
				return
			}
		}
		write(w, 200, map[string]any{"wallet": wallet, "subscription": subscription, "money_scale": model.MoneyScale, "usage": usage})
		return
	}
	if path == "/account/v1/wallet/ledger" && r.Method == "GET" {
		v, e := billing.Ledger(r.Context(), u.ID)
		if e != nil {
			write(w, 503, map[string]string{"error": "流水不可读取"})
			return
		}
		write(w, 200, v)
		return
	}
	if path == "/account/v1/plans" && r.Method == "GET" {
		v, e := billing.Plans(r.Context(), false)
		if e != nil {
			write(w, 503, map[string]string{"error": "套餐不可读取"})
			return
		}
		write(w, 200, v)
		return
	}
	if path == "/account/v1/subscription" && r.Method == "POST" {
		var req payload
		if !read(w, r, &req) {
			return
		}
		if !req.Confirm || len(req.OperationID) != 36 || req.PlanID == "" {
			write(w, 400, map[string]string{"error": "请确认套餐购买并使用唯一操作编号"})
			return
		}
		if e := audit.New(s.store, nil).RecordChecked(r.Context(), auditActor(u), r, "account.plan.purchase.requested", "plan", req.PlanID, 202, map[string]any{"operation_id": req.OperationID}); e != nil {
			write(w, 503, map[string]string{"error": "审计不可用，未购买"})
			return
		}
		v, e := billing.PurchasePlan(r.Context(), u.ID, req.PlanID, "purchase:"+u.ID+":"+req.OperationID)
		if e != nil {
			write(w, 400, map[string]string{"error": billingError(e)})
			return
		}
		s.users.RecordAudit(auditActor(u), r, "account.plan.purchase", "subscription", v.ID, 200, nil)
		write(w, 200, v)
		return
	}
	if path == "/account/v1/logs" && r.Method == "GET" {
		s.callLogs(w, r, u.ID)
		return
	}

	if path == "/account/v1/keys" && r.Method == "GET" {
		st, ok := s.store.(interface {
			OwnCredentials(context.Context, string) ([]model.Credential, error)
		})
		if !ok {
			write(w, 503, map[string]string{"error": "凭据不可读取"})
			return
		}
		list, e := st.OwnCredentials(r.Context(), u.ID)
		if e != nil {
			write(w, 503, map[string]string{"error": "凭据读取失败"})
			return
		}
		write(w, 200, list)
		return
	}
	if strings.HasPrefix(path, "/account/v1/keys/") && strings.HasSuffix(path, "/ip-policy") && r.Method == "PUT" {
		s.keyIPPolicy(w, r, u, false)
		return
	}
	if path == "/account/v1/keys" && r.Method == "POST" {
		var req payload
		if !read(w, r, &req) {
			return
		}
		if e := s.reauthenticate(r, u, req); e != nil {
			write(w, 403, map[string]string{"error": "请验证当前密码与双重验证"})
			return
		}
		s.createKey(w, r, u, u.ID, req)
		return
	}
	if strings.HasPrefix(path, "/account/v1/keys/") && strings.HasSuffix(path, "/rotate") && r.Method == "POST" {
		var req payload
		if !read(w, r, &req) {
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/account/v1/keys/"), "/rotate")
		key, e := s.store.GetCredential(id)
		if e != nil || key.OwnerUserID != u.ID {
			write(w, 404, nil)
			return
		}
		if !req.Confirm {
			write(w, 400, nil)
			return
		}
		if e = s.reauthenticate(r, u, req); e != nil {
			write(w, 403, nil)
			return
		}
		if e = audit.New(s.store, nil).RecordChecked(r.Context(), auditActor(u), r, "credential.rotate.requested", "credential", id, 202, nil); e != nil {
			write(w, 503, nil)
			return
		}
		s.rotateKey(w, r, u, key)
		return
	}
	if strings.HasPrefix(path, "/account/v1/keys/") && strings.HasSuffix(path, "/reveal") && r.Method == "POST" {
		s.revealOwnKey(w, r, u)
		return
	}
	if strings.HasPrefix(path, "/account/v1/keys/") && r.Method == "DELETE" {
		var req payload
		if !read(w, r, &req) {
			return
		}
		if err := s.reauthenticate(r, u, req); err != nil {
			write(w, 403, map[string]string{"error": "请重新验证当前密码与双重验证"})
			return
		}
		id := strings.TrimPrefix(path, "/account/v1/keys/")
		v, e := s.store.GetCredential(id)
		if e != nil || v.OwnerUserID != u.ID {
			write(w, 404, map[string]string{"error": "凭据不存在"})
			return
		}
		v.Revoked = true
		if e = s.store.UpdateCredential(v); e != nil {
			write(w, 503, map[string]string{"error": "凭据暂不可吊销"})
			return
		}
		s.users.RecordAudit(auditActor(u), r, "account.key.revoke", "credential", v.ID, 200, nil)
		write(w, 200, map[string]bool{"revoked": true})
		return
	}
	write(w, 404, map[string]string{"error": "not found"})
}
func billingError(err error) string {
	switch {
	case errors.Is(err, store.ErrFunds):
		return "可用余额不足"
	case errors.Is(err, store.ErrPlanQuota):
		return "套餐额度已用完"
	case errors.Is(err, store.ErrConflict):
		return "操作编号冲突，或已有待生效续购。请刷新账户后确认。"
	}
	return "结算暂不可用"
}
func (s *Service) admin(w http.ResponseWriter, r *http.Request, u model.User, cfg model.SecuritySettings) {
	path := r.URL.Path
	if strings.HasPrefix(path, "/account/v1/admin/keys/") && strings.HasSuffix(path, "/ip-policy") && r.Method == "PUT" {
		s.keyIPPolicy(w, r, u, true)
		return
	}
	if path == "/account/v1/admin/card-plans" && r.Method == "GET" {
		v, e := s.store.(store.BillingStore).Plans(r.Context(), true)
		if e != nil {
			write(w, 503, nil)
		} else {
			write(w, 200, v)
		}
		return
	}
	if strings.HasPrefix(path, "/account/v1/admin/cards") {
		s.cards(w, r, u)
		return
	}
	if path == "/account/v1/admin/usage" && r.Method == "GET" {
		s.userUsage(w, r)
		return
	}
	if strings.HasPrefix(path, "/account/v1/admin/keys") {
		s.adminKeys(w, r, u)
		return
	}
	if path == "/account/v1/admin/plan-assignment" && r.Method == "POST" {
		s.assignPlan(w, r, u)
		return
	}
	if path == "/account/v1/admin/logs" && r.Method == "GET" {
		s.callLogs(w, r, "")
		return
	}
	if path == "/account/v1/admin/credentials" && r.Method == "GET" {
		s.credentialDirectory(w, r)
		return
	}
	billing, _ := s.store.(store.BillingStore)
	if billing == nil {
		write(w, 503, map[string]string{"error": "结算服务不可用"})
		return
	}
	if path == "/account/v1/admin/review" {
		st, ok := s.store.(interface {
			ReviewCharges(context.Context) ([]model.Charge, error)
			ResolveCharge(context.Context, string, bool) error
		})
		if !ok {
			write(w, 503, map[string]string{"error": "核对服务不可用"})
			return
		}
		if r.Method == "GET" {
			list, e := st.ReviewCharges(r.Context())
			if e != nil {
				write(w, 503, map[string]string{"error": "记录不可读取"})
				return
			}
			write(w, 200, list)
			return
		}
		if r.Method == "POST" {
			var req payload
			if !read(w, r, &req) {
				return
			}
			if !req.Confirm || req.OperationID == "" || (req.Purpose != "charged" && req.Purpose != "refunded") {
				write(w, 400, map[string]string{"error": "请确认结算结果"})
				return
			}
			if e := s.reauthenticate(r, u, req); e != nil {
				write(w, 403, map[string]string{"error": "请重新验证账号"})
				return
			}
			if e := audit.New(s.store, nil).RecordChecked(r.Context(), auditActor(u), r, "billing.charge.resolve.requested", "charge", req.OperationID, 202, map[string]any{"outcome": req.Purpose}); e != nil {
				write(w, 503, map[string]string{"error": "审计不可用，未结算"})
				return
			}
			if e := st.ResolveCharge(r.Context(), req.OperationID, req.Purpose == "charged"); e != nil {
				write(w, 409, map[string]string{"error": "记录不可结算，请刷新"})
				return
			}
			write(w, 200, map[string]bool{"resolved": true})
			return
		}
	}
	if path == "/account/v1/admin/wallet" && r.Method == "GET" {
		id := r.URL.Query().Get("user_id")
		if _, e := s.store.GetUserByID(id); e != nil {
			write(w, 404, map[string]string{"error": "用户不存在"})
			return
		}
		wallet, e := billing.Wallet(r.Context(), id)
		if e != nil {
			write(w, 503, map[string]string{"error": "余额不可读取"})
			return
		}
		ledger, e := billing.Ledger(r.Context(), id)
		if e != nil {
			write(w, 503, map[string]string{"error": "流水不可读取"})
			return
		}
		sub, e := billing.Subscription(r.Context(), id)
		if e != nil {
			write(w, 503, map[string]string{"error": "套餐不可读取"})
			return
		}
		write(w, 200, map[string]any{"wallet": wallet, "ledger": ledger, "subscription": sub})
		return
	}
	if path == "/account/v1/admin/settings" && r.Method == "GET" {
		write(w, 200, map[string]any{"settings": cfg, "github_secret_set": cfg.GitHubSecret != "", "google_secret_set": cfg.GoogleSecret != "", "telegram_secret_set": cfg.TelegramSecret != "", "turnstile_secret_set": cfg.TurnstileSecret != "", "mail_available": s.mail.MailAvailable()})
		return
	}
	if path == "/account/v1/admin/settings" && r.Method == "PUT" {
		var request struct {
			TurnstileToken  string                 `json:"turnstile_token"`
			Settings        model.SecuritySettings `json:"settings"`
			GitHubSecret    string                 `json:"github_secret"`
			TelegramSecret  string                 `json:"telegram_secret"`
			GoogleSecret    string                 `json:"google_secret"`
			TurnstileSecret string                 `json:"turnstile_secret"`
			CurrentPassword string                 `json:"current_password"`
			TOTPCode        string                 `json:"totp_code"`
		}
		if !read(w, r, &request) {
			return
		}
		if e := s.ReauthenticateAdmin(r, request.CurrentPassword, request.TurnstileToken); e != nil {
			write(w, 403, map[string]string{"error": "请验证当前管理员密码"})
			return
		}
		if e := s.Guard(r, "sensitive", request.TurnstileToken); e != nil {
			write(w, 403, map[string]string{"error": e.Error()})
			return
		}
		next := request.Settings
		if next.AllowedEmailDomains == nil {
			next.AllowedEmailDomains = cfg.AllowedEmailDomains
		}
		domains, e := normalizeEmailDomains(next.AllowedEmailDomains)
		if e != nil {
			write(w, 400, map[string]string{"error": e.Error()})
			return
		}
		next.AllowedEmailDomains = domains
		if next.Version != cfg.Version {
			write(w, 409, map[string]string{"error": "设置已被更新，请刷新"})
			return
		}
		next.GitHubSecret = cfg.GitHubSecret
		next.GoogleSecret = cfg.GoogleSecret
		next.TelegramSecret = cfg.TelegramSecret
		next.TurnstileSecret = cfg.TurnstileSecret
		if request.GitHubSecret != "" {
			next.GitHubSecret = request.GitHubSecret
		}
		if request.TelegramSecret != "" {
			next.TelegramSecret = request.TelegramSecret
		}
		if request.GoogleSecret != "" {
			next.GoogleSecret = request.GoogleSecret
		}
		if request.TurnstileSecret != "" {
			next.TurnstileSecret = request.TurnstileSecret
		}
		if e := validateSettings(next, s.store); e != nil {
			write(w, 400, map[string]string{"error": e.Error()})
			return
		}
		raw, _ := json.Marshal(struct{ Turnstile, GitHub, Google, Telegram string }{next.TurnstileSecret, next.GitHubSecret, next.GoogleSecret, next.TelegramSecret})
		encrypted, e := auth.EncryptSecret(s.key+":authentication", string(raw))
		if e != nil {
			write(w, 503, map[string]string{"error": "安全配置不可保存"})
			return
		}
		if e = audit.New(s.store, nil).RecordChecked(r.Context(), auditActor(u), r, "account.settings.update", "authentication", "settings", 202, nil); e != nil {
			write(w, 503, map[string]string{"error": "审计不可用，配置未修改"})
			return
		}
		if e = s.accounts.SaveSecuritySettings(r.Context(), next, encrypted); e != nil {
			write(w, 503, map[string]string{"error": "配置保存失败"})
			return
		}
		write(w, 200, map[string]bool{"saved": true})
		return
	}
	if path == "/account/v1/admin/plans" && r.Method == "GET" {
		list, e := billing.Plans(r.Context(), true)
		if e != nil {
			write(w, 503, map[string]string{"error": "套餐不可读取"})
			return
		}
		write(w, 200, list)
		return
	}
	if path == "/account/v1/admin/plans" && r.Method == "PUT" {
		var v model.Plan
		if !read(w, r, &v) {
			return
		}
		if v.ID == "" {
			v.ID = ids.NewUUID()
		}
		if v.Name == "" || len(v.Name) > 120 || v.PriceMicros < 0 || v.PriceMicros > 1000000000000000 || v.Days < 1 || v.Days > 366 || v.Hourly < 0 || v.Daily < 0 || v.Monthly < 0 || len(v.Description) > 512 || v.Hourly > 1000000000 || v.Daily > 1000000000 || v.Monthly > 1000000000 || (v.Hourly == 0 && v.Daily == 0 && v.Monthly == 0) {
			write(w, 400, map[string]string{"error": "套餐配置无效"})
			return
		}
		if e := audit.New(s.store, nil).RecordChecked(r.Context(), auditActor(u), r, "billing.plan.update.requested", "plan", v.ID, 202, nil); e != nil {
			write(w, 503, map[string]string{"error": "审计不可用，未保存"})
			return
		}
		if e := billing.SavePlan(r.Context(), v); e != nil {
			write(w, 503, map[string]string{"error": "套餐保存失败"})
			return
		}
		s.users.RecordAudit(auditActor(u), r, "billing.plan.update", "plan", v.ID, 200, nil)
		write(w, 200, v)
		return
	}
	if path == "/account/v1/admin/balance" && r.Method == "POST" {
		var request struct {
			UserID          string `json:"user_id"`
			TurnstileToken  string `json:"turnstile_token"`
			AmountMicros    int64  `json:"amount_micros"`
			OperationID     string `json:"operation_id"`
			Note            string `json:"note"`
			CurrentPassword string `json:"current_password"`
			TOTPCode        string `json:"totp_code"`
		}
		if !read(w, r, &request) {
			return
		}
		if len(request.OperationID) != 36 || strings.TrimSpace(request.Note) == "" || len(request.Note) > 512 || request.AmountMicros == 0 {
			write(w, 400, map[string]string{"error": "金额、原因和操作编号必填"})
			return
		}
		if e := s.ReauthenticateAdmin(r, request.CurrentPassword, request.TurnstileToken); e != nil {
			write(w, 403, map[string]string{"error": "重新验证失败"})
			return
		}
		if e := s.Guard(r, "sensitive", request.TurnstileToken); e != nil {
			write(w, 403, map[string]string{"error": e.Error()})
			return
		}
		if e := audit.New(s.store, nil).RecordChecked(r.Context(), auditActor(u), r, "billing.balance.adjust.requested", "user", request.UserID, 202, map[string]any{"amount_micros": request.AmountMicros, "note": request.Note}); e != nil {
			write(w, 503, map[string]string{"error": "审计不可用，余额未修改"})
			return
		}
		v, e := billing.AdjustBalance(r.Context(), request.UserID, request.AmountMicros, "adjust:"+request.UserID+":"+request.OperationID, request.Note)
		if e != nil {
			write(w, 400, map[string]string{"error": billingError(e)})
			return
		}
		write(w, 200, v)
		return
	}
	if path == "/account/v1/admin/permissions" && r.Method == "PUT" {
		var req struct {
			Code  string `json:"code"`
			Label string `json:"label"`
		}
		if !read(w, r, &req) {
			return
		}
		if req.Label == "" || utf8.RuneCountInString(req.Label) > 64 {
			write(w, 400, map[string]string{"error": "权限名称无效"})
			return
		}
		st, ok := s.store.(interface {
			RenamePermission(context.Context, string, string) error
		})
		if !ok {
			write(w, 503, map[string]string{"error": "权限服务不可用"})
			return
		}
		if e := st.RenamePermission(r.Context(), req.Code, req.Label); e != nil {
			write(w, 400, map[string]string{"error": "权限保存失败"})
			return
		}
		s.users.RecordAudit(auditActor(u), r, "account.permission.rename", "permission", req.Code, 200, nil)
		write(w, 200, map[string]bool{"saved": true})
		return
	}

	write(w, 404, map[string]string{"error": "not found"})
}
func validateSettings(cfg model.SecuritySettings, roles interface {
	GetRoleByName(string) (model.Role, error)
}) error {
	if cfg.RegistrationEnabled || cfg.GitHubEnabled || cfg.GoogleEnabled || cfg.TelegramEnabled || cfg.TurnstileEnabled {
		u, e := url.Parse(cfg.WebsiteURL)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return errors.New("开放注册或第三方登录须配置 HTTPS 网站地址")
		}
	}
	role := cfg.DefaultRole
	if role == "" {
		role = "member"
	}
	v, e := roles.GetRoleByName(role)
	if e != nil || !publicRole(v) {
		return errors.New("默认注册等级必须为非管理等级")
	}
	if cfg.TurnstileEnabled {
		website, e := url.Parse(cfg.WebsiteURL)
		if e != nil || !strings.EqualFold(website.Hostname(), cfg.TurnstileHost) {
			return errors.New("Turnstile 主机名必须与 HTTPS 网站地址一致")
		}
	}
	if cfg.TurnstileEnabled && (cfg.TurnstileSiteKey == "" || cfg.TurnstileSecret == "" || cfg.TurnstileHost == "") {
		return errors.New("请完整配置 Turnstile")
	}
	if cfg.GitHubEnabled && (cfg.GitHubClientID == "" || cfg.GitHubSecret == "") {
		return errors.New("GitHub 登录配置不完整")
	}
	if cfg.TelegramEnabled && (cfg.TelegramClientID == "" || cfg.TelegramSecret == "") {
		return errors.New("Telegram 登录配置不完整")
	}
	if cfg.GoogleEnabled && (cfg.GoogleClientID == "" || cfg.GoogleSecret == "") {
		return errors.New("Google 登录配置不完整")
	}
	return nil
}
