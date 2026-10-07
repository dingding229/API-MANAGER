package api

import (
	"api-manager/internal/audit"
	"api-manager/internal/mailtemplates"
	"api-manager/internal/model"
	"api-manager/internal/sitesettings"
	"api-manager/internal/store"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func (a *Admin) SetSiteSettings(service *sitesettings.Service) { a.siteSettings = service }
func (a *Admin) requireSiteOwner(w http.ResponseWriter, r *http.Request) bool {
	actor, ok := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "admin authentication required"})
		return false
	}
	account, err := a.store.GetUserByID(actor.ID)
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "account unavailable"})
		return false
	}
	if account.Status != "active" || (!containsRole(account.Roles, "super_admin") && account.Role != "super_admin") {
		writeJSON(w, 403, map[string]string{"error": "only super_admin may configure this website"})
		return false
	}
	if a.siteSettings == nil {
		writeJSON(w, 503, map[string]string{"error": "site settings unavailable"})
		return false
	}
	return true
}
func decodeSiteJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "invalid settings JSON"})
		return false
	}
	return true
}
func (a *Admin) siteSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.requireSiteOwner(w, r) {
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, 200, a.siteSettings.View())
		return
	}
	var request model.UpdateSiteSettingsRequest
	if !decodeSiteJSON(w, r, &request) {
		return
	}
	if request.MailTemplates != nil {
		if e := mailtemplates.Validate(*request.MailTemplates); e != nil {
			writeJSON(w, 400, map[string]string{"error": e.Error()})
			return
		}
		if a.credentialGuard == nil || a.credentialGuard(r, request.CurrentPassword, "") != nil {
			writeJSON(w, 403, map[string]string{"error": "保存邮件模板前请验证管理员密码"})
			return
		}
	}
	domain, err := sitesettings.NormalizeAPIDomain(request.Site.APIDomain)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "请填写有效的接口专用域名或完整地址"})
		return
	}
	if domain != "" && request.Site.WebsiteURL == "" && sitesettings.SameHostname(domain, r.Host) {
		writeJSON(w, 400, map[string]string{"error": "接口专用地址不能与网站地址相同"})
		return
	}
	request.Site.APIDomain = domain
	value, err := a.siteSettings.Save(request)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeJSON(w, 409, map[string]string{"error": "设置已经更新，请重新加载后再保存"})
			return
		}
		if errors.Is(err, sitesettings.ErrSameSiteDomain) {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if errors.Is(err, sitesettings.ErrInvalid) {
			writeJSON(w, 400, map[string]string{"error": "请检查必填项、长度、网址、SMTP 端口与 TLS 配置"})
			return
		}
		writeJSON(w, 503, map[string]string{"error": "设置暂时无法保存，请稍后重试"})
		return
	}
	a.recordAudit(r, "settings.update", "site", "global", 200, map[string]any{"version": value.Version, "smtp_enabled": value.SMTP.Enabled, "smtp_password_changed": request.ClearSMTPPassword || (request.SMTPPassword != nil && *request.SMTPPassword != "")})
	writeJSON(w, 200, value)
}
func (a *Admin) testSiteSMTP(w http.ResponseWriter, r *http.Request) {
	if !a.requireSiteOwner(w, r) {
		return
	}
	var request struct {
		Recipient string `json:"recipient"`
	}
	if !decodeSiteJSON(w, r, &request) {
		return
	}
	err := a.siteSettings.TestMail(r.Context(), request.Recipient)
	if err != nil {
		status := 400
		if errors.Is(err, sitesettings.ErrTestLimited) {
			status = 429
			w.Header().Set("Retry-After", "60")
		}
		if errors.Is(err, sitesettings.ErrMailFailed) {
			status = 502
		}
		a.recordAudit(r, "settings.smtp.test.failed", "site", "global", status, nil)
		writeJSON(w, status, map[string]string{"error": "发送失败：请确认 SMTP 已启用并保存，收件地址、账号密码、发件地址及 TLS 配置有效；频繁测试请稍后重试"})
		return
	}
	a.recordAudit(r, "settings.smtp.test", "site", "global", 200, nil)
	writeJSON(w, 200, map[string]string{"message": "SMTP 服务器已接受测试邮件，请检查收件箱及垃圾邮件"})
}

func (a *Admin) previewMailTemplate(w http.ResponseWriter, r *http.Request) {
	if !a.requireSiteOwner(w, r) {
		return
	}
	var q struct {
		Template model.EmailTemplate `json:"template"`
	}
	if !decodeSiteJSON(w, r, &q) {
		return
	}
	site := a.siteSettings.Public()
	origin := site.WebsiteURL
	if origin == "" {
		origin = "https://example.test"
	}
	subject, body, e := mailtemplates.Render(q.Template, map[string]string{"site_name": site.Name, "email": "preview@example.test", "code": "123456", "purpose": "邮箱验证", "expires_minutes": "10", "reset_url": origin + "/login#reset=preview-sample", "site_url": origin})
	if e != nil {
		writeJSON(w, 400, map[string]string{"error": e.Error()})
		return
	}
	previewURL := a.issueMailPreview(r, body)
	if previewURL == "" {
		writeJSON(w, 429, nil)
		return
	}
	writeJSON(w, 200, map[string]string{"subject": subject, "preview_url": previewURL})
}
