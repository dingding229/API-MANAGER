package model

import "time"

// Money uses 1,000,000 integer units per currency unit, never floating point.
const MoneyScale int64 = 1000000

type Plan struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	PriceMicros int64     `json:"price_micros"`
	Days        int       `json:"days"`
	Hourly      int64     `json:"hourly"`
	Daily       int64     `json:"daily"`
	Monthly     int64     `json:"monthly"`
	Enabled     bool      `json:"enabled"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type Subscription struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	PlanID    string    `json:"plan_id"`
	PlanName  string    `json:"plan_name"`
	Hourly    int64     `json:"hourly"`
	Daily     int64     `json:"daily"`
	Monthly   int64     `json:"monthly"`
	StartsAt  time.Time `json:"starts_at"`
	ExpiresAt time.Time `json:"expires_at"`
}
type Wallet struct {
	BalanceMicros int64  `json:"balance_micros"`
	HeldMicros    int64  `json:"held_micros"`
	Currency      string `json:"currency"`
}
type Ledger struct {
	ID            string    `json:"id"`
	UserID        string    `json:"user_id"`
	Kind          string    `json:"kind"`
	Reference     string    `json:"reference"`
	Note          string    `json:"note"`
	AmountMicros  int64     `json:"amount_micros"`
	BalanceMicros int64     `json:"balance_micros"`
	CreatedAt     time.Time `json:"created_at"`
}
type Charge struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	APIID       string    `json:"api_id"`
	PriceMicros int64     `json:"price_micros"`
	PlanID      string    `json:"subscription_id"`
	Outcome     string    `json:"outcome"`
	CreatedAt   time.Time `json:"created_at"`
}
type CallLog struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	APIID       string    `json:"api_id"`
	APIName     string    `json:"api_name"`
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	ClientIP    string    `json:"client_ip"`
	Status      int       `json:"status"`
	PriceMicros int64     `json:"price_micros"`
	DurationMS  int64     `json:"duration_ms"`
	CreatedAt   time.Time `json:"created_at"`
}
type SecuritySettings struct {
	Version             int64  `json:"version"`
	RegistrationEnabled bool   `json:"registration_enabled"`
	EmailLoginEnabled   bool   `json:"email_login_enabled"`
	TurnstileEnabled    bool   `json:"turnstile_enabled"`
	TurnstileSiteKey    string `json:"turnstile_site_key"`
	TurnstileSecret     string `json:"-"`
	TurnstileHost       string `json:"turnstile_host"`
	GitHubEnabled       bool   `json:"github_enabled"`
	GitHubClientID      string `json:"github_client_id"`
	GitHubSecret        string `json:"-"`
	GoogleEnabled       bool   `json:"google_enabled"`
	GoogleClientID      string `json:"google_client_id"`
	GoogleSecret        string `json:"-"`
	WebsiteURL          string `json:"website_url"`
	DefaultRole         string `json:"default_role"`
}
type AccountRecord struct {
	UserID             string
	Nickname           string
	EmailVerified      bool
	TOTPSecret         string
	TOTPPending        string
	TOTPLastStep       int64
	TOTPPendingExpires time.Time
	RecoveryHashes     []string
}
type Verification struct {
	ID, Purpose, Subject, UserID, CodeHash, Payload string
	ExpiresAt                                       time.Time
	Attempts                                        int
	Binding                                         string
}
type Identity struct{ Provider, Subject, UserID string }
