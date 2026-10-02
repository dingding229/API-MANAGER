package sitesettings

import (
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/store"
	netsecurity "api-manager/internal/upstream/security"
	"api-manager/internal/user"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

type Store interface {
	GetSiteSettings() (model.SiteSettingsRecord, error)
	SaveSiteSettings(model.SiteSettingsRecord, int64) (model.SiteSettingsRecord, error)
}
type snapshot struct {
	apiAuthority string
	apiScheme    string
	record       model.SiteSettingsRecord
	password     string
	mailer       *user.SMTPMailer
}
type Service struct {
	store       Store
	key         string
	ctx         context.Context
	users       *user.Service
	current     atomic.Pointer[snapshot]
	mu          sync.Mutex
	testMu      sync.Mutex
	testStarted time.Time
	testCount   int
	testBusy    bool
}

var ErrInvalid = errors.New("invalid site settings")
var ErrTestLimited = errors.New("test email rate limited")
var ErrMailFailed = errors.New("test email delivery failed; check SMTP credentials, sender, recipient and TLS settings")

func Defaults() model.SiteSettings {
	return model.SiteSettings{Site: model.PublicSiteInfo{Name: "API Manager", PublicTitle: "API Manager · 开放接口目录", AdminTitle: "API Manager Console", Description: "浏览已公开的 API 接口、参数和调用方式。", Subtitle: "开放接口目录", HeroTitle: "找到接口，\n开始你的下一次调用。", HeroDescription: "从用途到参数，从认证方式到调用示例。\n让接口接入清晰、直接、有据可循。", Footer: "已发布的服务信息"}, SMTP: model.SMTPSettings{Port: 587, Mode: "starttls"}}
}
func New(ctx context.Context, s Store, key string, defaults model.SiteSettings, password string, users *user.Service) (*Service, error) {
	service := &Service{store: s, key: key, ctx: ctx, users: users}
	record, err := s.GetSiteSettings()
	if errors.Is(err, store.ErrNotFound) {
		record = model.SiteSettingsRecord{Settings: defaults}
	} else if err != nil {
		return nil, err
	} else if record.EncryptedSMTPPassword != "" {
		password, err = auth.DecryptSecret(key, record.EncryptedSMTPPassword)
		if err != nil {
			return nil, errors.New("cannot decrypt stored SMTP password")
		}
	} else {
		password = ""
	}
	prepared, err := service.prepare(record, password)
	if err != nil {
		return nil, err
	}
	if err = service.activate(prepared); err != nil {
		return nil, err
	}
	return service, nil
}
func (s *Service) View() model.SiteSettingsView {
	snap := s.current.Load()
	r := snap.record
	return model.SiteSettingsView{Version: r.Version, Site: r.Settings.Site, SMTP: r.Settings.SMTP, SMTPPasswordSet: snap.password != "", RecoveryEnabled: snap.mailer != nil, UpdatedAt: r.UpdatedAt}
}
func (s *Service) Public() model.PublicSiteInfo { return s.current.Load().record.Settings.Site }
func (s *Service) Save(req model.UpdateSiteSettingsRequest) (model.SiteSettingsView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.current.Load()
	if req.Version != old.record.Version {
		return model.SiteSettingsView{}, store.ErrConflict
	}
	password := old.password
	if req.ClearSMTPPassword {
		if req.SMTPPassword != nil && *req.SMTPPassword != "" {
			return model.SiteSettingsView{}, ErrInvalid
		}
		password = ""
	} else if req.SMTPPassword != nil && *req.SMTPPassword != "" {
		password = *req.SMTPPassword
	}
	if len(password) > 1024 || strings.ContainsAny(password, "\r\n\x00") {
		return model.SiteSettingsView{}, ErrInvalid
	}
	domain, err := NormalizeAPIDomain(req.Site.APIDomain)
	if err != nil {
		return model.SiteSettingsView{}, err
	}
	req.Site.APIDomain = domain
	record := model.SiteSettingsRecord{Settings: model.SiteSettings{Site: req.Site, SMTP: req.SMTP}, UpdatedAt: time.Now().UTC()}
	prepared, err := s.prepare(record, password)
	if err != nil {
		return model.SiteSettingsView{}, err
	}
	if password != "" {
		record.EncryptedSMTPPassword, err = auth.EncryptSecret(s.key, password)
		if err != nil {
			return model.SiteSettingsView{}, err
		}
	}
	record, err = s.store.SaveSiteSettings(record, req.Version)
	if err != nil {
		// A concurrent writer must not leave the editor permanently stuck on an old revision.
		if errors.Is(err, store.ErrConflict) {
			if latest, e := s.store.GetSiteSettings(); e == nil {
				pass := ""
				if latest.EncryptedSMTPPassword != "" {
					pass, e = auth.DecryptSecret(s.key, latest.EncryptedSMTPPassword)
				}
				if e == nil {
					if loaded, e := s.prepare(latest, pass); e == nil {
						_ = s.activate(loaded)
					}
				}
			}
		}
		return model.SiteSettingsView{}, err
	}
	prepared.record = record
	if err = s.activate(prepared); err != nil {
		return model.SiteSettingsView{}, err
	}
	return s.View(), nil
}
func (s *Service) prepare(record model.SiteSettingsRecord, password string) (*snapshot, error) {
	cfg := record.Settings
	if err := Validate(cfg, password); err != nil {
		return nil, err
	}
	snap := &snapshot{record: record, password: password}
	if cfg.Site.APIDomain != "" {
		domain, err := NormalizeAPIDomain(cfg.Site.APIDomain)
		if err != nil {
			return nil, err
		}
		u, _ := url.Parse(domain)
		snap.apiAuthority = u.Host
		snap.apiScheme = u.Scheme
	}
	if cfg.SMTP.Enabled {
		mailer, err := user.NewSMTPMailer(cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.Username, password, cfg.SMTP.From, cfg.SMTP.Mode)
		if err != nil {
			return nil, ErrInvalid
		}
		// Same public-address pinning boundary as upstream proxies; never probe Redis,
		// PostgreSQL, loopback, metadata services or a rebound private DNS address.
		target := &url.URL{Scheme: "https", Host: net.JoinHostPort(cfg.SMTP.Host, strconv.Itoa(cfg.SMTP.Port))}
		mailer.DialContext = netsecurity.DialContext(nil, "", target)
		snap.mailer = mailer
	}
	return snap, nil
}
func (s *Service) activate(snap *snapshot) error {
	if s.users != nil {
		if snap.mailer == nil {
			s.users.DisableRecovery()
		} else if err := s.users.ConfigureRecovery(s.ctx, snap.mailer, snap.record.Settings.SMTP.ResetURL); err != nil {
			return err
		}
	}
	s.current.Store(snap)
	return nil
}
func (s *Service) TestMail(ctx context.Context, recipient string) error {
	recipient = strings.ToLower(strings.TrimSpace(recipient))
	address, err := mail.ParseAddress(recipient)
	if err != nil || address.Address != recipient || !strings.Contains(recipient, "@") || len(recipient) > 254 {
		return ErrInvalid
	}
	snap := s.current.Load()
	if snap.mailer == nil {
		return errors.New("enable and save SMTP before sending a test email")
	}
	s.testMu.Lock()
	now := time.Now()
	if now.Sub(s.testStarted) >= time.Minute {
		s.testStarted = now
		s.testCount = 0
	}
	if s.testBusy || s.testCount >= 3 {
		s.testMu.Unlock()
		return ErrTestLimited
	}
	s.testBusy = true
	s.testCount++
	s.testMu.Unlock()
	defer func() { s.testMu.Lock(); s.testBusy = false; s.testMu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err = snap.mailer.SendTest(ctx, recipient, snap.record.Settings.Site.Name); err != nil {
		return ErrMailFailed
	}
	return nil
}
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(405)
		return
	}
	_ = json.NewEncoder(w).Encode(s.Public())
}
func validURL(raw string, originOnly bool) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && !strings.ContainsAny(raw, "\r\n\x00") && (!originOnly || u.Path == "" || u.Path == "/") && (u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")))
}
func Validate(cfg model.SiteSettings, password string) error {
	for _, f := range []struct {
		value               string
		max                 int
		required, multiline bool
	}{
		{cfg.Site.Name, 80, true, false}, {cfg.Site.PublicTitle, 120, true, false}, {cfg.Site.AdminTitle, 120, true, false}, {cfg.Site.Description, 600, false, false}, {cfg.Site.Keywords, 300, false, false}, {cfg.Site.Subtitle, 80, false, false}, {cfg.Site.HeroTitle, 160, true, true}, {cfg.Site.HeroDescription, 600, false, true}, {cfg.Site.Announcement, 600, false, true}, {cfg.Site.Footer, 300, false, false},
	} {
		if !utf8.ValidString(f.value) || utf8.RuneCountInString(f.value) > f.max || (f.required && strings.TrimSpace(f.value) == "") || strings.ContainsAny(f.value, "\r\x00") || (!f.multiline && strings.Contains(f.value, "\n")) {
			return ErrInvalid
		}
	}
	if len(cfg.Site.WebsiteURL) > 512 || len(cfg.Site.APIBaseURL) > 512 || !validWebsiteOrigin(cfg.Site.WebsiteURL) || !validURL(cfg.Site.APIBaseURL, true) {
		return ErrInvalid
	}
	if cfg.Site.ContactEmail != "" {
		a, err := mail.ParseAddress(cfg.Site.ContactEmail)
		if err != nil || a.Address != cfg.Site.ContactEmail || len(cfg.Site.ContactEmail) > 254 || !strings.Contains(cfg.Site.ContactEmail, "@") {
			return ErrInvalid
		}
	}
	if len(cfg.Site.APIDomain) > 512 {
		return ErrInvalid
	}
	if _, err := NormalizeAPIDomain(cfg.Site.APIDomain); err != nil {
		return err
	}
	if cfg.Site.APIDomain != "" && cfg.Site.WebsiteURL != "" {
		u, err := url.Parse(cfg.Site.WebsiteURL)
		if err != nil {
			return ErrInvalid
		}
		if SameHostname(cfg.Site.APIDomain, u.Host) {
			return ErrSameSiteDomain
		}
	}
	smtp := cfg.SMTP
	if len(smtp.Host) > 253 || strings.ContainsAny(smtp.Host, "/\\\r\n \x00@?#") || smtp.Port < 1 || smtp.Port > 65535 || (smtp.Mode != "tls" && smtp.Mode != "starttls") || len(smtp.Username) > 254 || strings.ContainsAny(smtp.Username, "\r\n\x00") || len(smtp.From) > 254 || len(smtp.ResetURL) > 512 || !validURL(smtp.ResetURL, false) {
		return ErrInvalid
	}
	if smtp.Enabled {
		if !user.ValidRecoveryURL(smtp.ResetURL) {
			return ErrInvalid
		}
		if _, err := user.NewSMTPMailer(smtp.Host, smtp.Port, smtp.Username, password, smtp.From, smtp.Mode); err != nil {
			return ErrInvalid
		}
	}
	return nil
}

func validWebsiteOrigin(raw string) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	_, ok := canonicalAuthority(u.Host, u.Scheme)
	return ok
}
