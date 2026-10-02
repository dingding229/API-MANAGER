package model

import "time"

// PublicSiteInfo is the entire public allowlist. SMTP, secrets and security
// settings must never be added to this projection.
type PublicSiteInfo struct {
	Name            string `json:"name"`
	PublicTitle     string `json:"public_title"`
	AdminTitle      string `json:"admin_title"`
	Description     string `json:"description"`
	Keywords        string `json:"keywords"`
	WebsiteURL      string `json:"website_url"`
	APIBaseURL      string `json:"api_base_url"`
	Subtitle        string `json:"subtitle"`
	HeroTitle       string `json:"hero_title"`
	HeroDescription string `json:"hero_description"`
	Announcement    string `json:"announcement"`
	Footer          string `json:"footer"`
	ContactEmail    string `json:"contact_email"`
}
type SMTPSettings struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Mode     string `json:"mode"`
	Username string `json:"username"`
	From     string `json:"from"`
	ResetURL string `json:"reset_url"`
}
type SiteSettings struct {
	Site PublicSiteInfo `json:"site"`
	SMTP SMTPSettings   `json:"smtp"`
}
type SiteSettingsRecord struct {
	Version               int64
	Settings              SiteSettings
	EncryptedSMTPPassword string
	UpdatedAt             time.Time
}
type SiteSettingsView struct {
	Version         int64          `json:"version"`
	Site            PublicSiteInfo `json:"site"`
	SMTP            SMTPSettings   `json:"smtp"`
	SMTPPasswordSet bool           `json:"smtp_password_set"`
	RecoveryEnabled bool           `json:"recovery_enabled"`
	UpdatedAt       time.Time      `json:"updated_at"`
}
type UpdateSiteSettingsRequest struct {
	Version           int64          `json:"version"`
	Site              PublicSiteInfo `json:"site"`
	SMTP              SMTPSettings   `json:"smtp"`
	SMTPPassword      *string        `json:"smtp_password,omitempty"`
	ClearSMTPPassword bool           `json:"clear_smtp_password"`
}
