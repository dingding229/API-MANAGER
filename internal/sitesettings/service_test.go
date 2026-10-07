package sitesettings

import (
	"api-manager/internal/model"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestSiteSettingsPersistEncryptAndDoNotExportSMTP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	memory := store.NewMemory()
	users := user.NewService(memory)
	service, err := New(ctx, memory, strings.Repeat("k", 64), Defaults(), "", users)
	if err != nil {
		t.Fatal(err)
	}
	request := model.UpdateSiteSettingsRequest{Site: service.Public(), SMTP: service.View().SMTP}
	password := "smtp-test-secret-with-quotes"
	request.SMTPPassword = &password
	request.Site.Name = "运营 API"
	request.Site.PublicTitle = "生产文档"
	request.Site.APIBaseURL = "https://api.example.test"
	request.SMTP = model.SMTPSettings{Enabled: true, Host: "smtp.example.test", Port: 465, Mode: "tls", Username: "sender@example.test", From: "sender@example.test", ResetURL: "https://example.test/admin/"}
	saved, err := service.Save(request)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.SMTPPasswordSet || !saved.RecoveryEnabled || saved.Version != 1 {
		t.Fatal("mail config not activated")
	}
	raw, _ := json.Marshal(saved)
	if strings.Contains(string(raw), password) {
		t.Fatal("password returned in admin response")
	}
	stored, err := memory.GetSiteSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.EncryptedSMTPPassword == "" || strings.Contains(stored.EncryptedSMTPPassword, password) {
		t.Fatal("password not encrypted")
	}
	restarted, err := New(ctx, memory, strings.Repeat("k", 64), Defaults(), "environment-must-not-replace", users)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.View().Site.Name != "运营 API" || restarted.current.Load().password != password {
		t.Fatal("saved settings not restored")
	}
	rec := httptest.NewRecorder()
	restarted.ServeHTTP(rec, httptest.NewRequest("GET", "/public/v1/site", nil))
	for _, secret := range []string{password, "smtp.example.test", "sender@example.test", "smtp_password", "reset_url", stored.EncryptedSMTPPassword} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatal("SMTP crossed the public boundary")
		}
	}
	request.Version = 1
	request.SMTPPassword = nil
	request.Site.Name = "继续更新"
	if _, err = restarted.Save(request); err != nil {
		t.Fatal(err)
	}
	if restarted.current.Load().password != password {
		t.Fatal("blank password did not preserve existing secret")
	}
	request.Version = 2
	empty := ""
	request.SMTPPassword = &empty
	if _, err = restarted.Save(request); err != nil {
		t.Fatal(err)
	}
	if restarted.current.Load().password != password {
		t.Fatal("empty password deleted existing secret")
	}
	request.Version = 3
	request.SMTP.Enabled = false
	request.ClearSMTPPassword = true
	request.SMTP.Username = ""
	if _, err = restarted.Save(request); err != nil {
		t.Fatal(err)
	}
	if restarted.View().SMTPPasswordSet || restarted.View().RecoveryEnabled {
		t.Fatal("explicit clear/disable did not apply")
	}
}
func TestSiteSettingsConcurrentUpdateUsesRevisionGuard(t *testing.T) {
	service, err := New(context.Background(), store.NewMemory(), strings.Repeat("k", 64), Defaults(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	request := model.UpdateSiteSettingsRequest{Site: service.Public(), SMTP: service.View().SMTP}
	var wg sync.WaitGroup
	result := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := service.Save(request); result <- err }()
	}
	wg.Wait()
	close(result)
	wins, conflicts := 0, 0
	for err := range result {
		if err == nil {
			wins++
		} else if errors.Is(err, store.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("concurrent write was lost")
	}
}
func TestUnsafeSiteSettingsAreRejected(t *testing.T) {
	for _, change := range []func(*model.SiteSettings){
		func(c *model.SiteSettings) { c.Site.Name = "bad\nheader" }, func(c *model.SiteSettings) { c.Site.WebsiteURL = "javascript:alert(1)" },
		func(c *model.SiteSettings) { c.Site.APIBaseURL = "https://name:password@example.test" }, func(c *model.SiteSettings) { c.SMTP.Mode = "none" },
		func(c *model.SiteSettings) { c.SMTP.Host = "example.test/metadata" }, func(c *model.SiteSettings) {
			c.SMTP.From = "sender@example.test\r\nBcc: leak@example.test"
			c.SMTP.Enabled = true
		},
	} {
		cfg := Defaults()
		change(&cfg)
		if Validate(cfg, "") == nil {
			t.Fatal("unsafe config accepted")
		}
	}
}
func TestSMTPCannotProbePrivateServices(t *testing.T) {
	service, err := New(context.Background(), store.NewMemory(), strings.Repeat("k", 64), Defaults(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	request := model.UpdateSiteSettingsRequest{Site: service.Public(), SMTP: model.SMTPSettings{Enabled: true, Host: "127.0.0.1", Port: 6379, Mode: "tls", From: "sender@example.test", ResetURL: "https://example.test/admin/"}}
	if _, err = service.Save(request); err != nil {
		t.Fatal(err)
	}
	if err = service.TestMail(context.Background(), "admin@example.test"); !errors.Is(err, ErrMailFailed) {
		t.Fatal("private SMTP was not denied")
	}
}

func TestDisabledPersistedMailDoesNotFallBackToEnvironment(t *testing.T) {
	memory := store.NewMemory()
	service, err := New(context.Background(), memory, strings.Repeat("k", 64), Defaults(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Save(model.UpdateSiteSettingsRequest{Site: service.Public(), SMTP: service.View().SMTP}); err != nil {
		t.Fatal(err)
	}
	defaults := Defaults()
	defaults.SMTP = model.SMTPSettings{Enabled: true, Host: "smtp.example.test", Port: 465, Mode: "tls", Username: "sender@example.test", From: "sender@example.test", ResetURL: "https://example.test/admin/"}
	restored, err := New(context.Background(), memory, strings.Repeat("k", 64), defaults, "environment-password", nil)
	if err != nil {
		t.Fatal(err)
	}
	if restored.View().RecoveryEnabled || restored.View().SMTPPasswordSet {
		t.Fatal("disabled saved setting unexpectedly reopened environment mail")
	}
}

func TestWebsiteTimeZoneRejectsInvalidLocation(t *testing.T) {
	cfg := Defaults()
	cfg.Site.TimeZone = "Invalid/TimeZone"
	if Validate(cfg, "") == nil {
		t.Fatal("invalid timezone accepted")
	}
	cfg.Site.TimeZone = "Asia/Kathmandu"
	if err := Validate(cfg, ""); err != nil {
		t.Fatal(err)
	}
}
