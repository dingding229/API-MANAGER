package model

import (
	"encoding/json"
	"time"
)

type API struct {
	PublicVisible      bool              `json:"public_visible"`
	PublicTitle        string            `json:"public_title"`
	PublicSummary      string            `json:"public_summary"`
	PublicCategory     string            `json:"public_category"`
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Description        string            `json:"description,omitempty"`
	Method             string            `json:"method"`
	Path               string            `json:"path"`
	AuthMode           string            `json:"auth_mode"`
	AuthConfig         map[string]string `json:"auth_config,omitempty"`
	RateLimitPerMinute int               `json:"rate_limit_per_minute,omitempty"`
	DailyQuota         int               `json:"daily_quota,omitempty"`
	MonthlyQuota       int               `json:"monthly_quota,omitempty"`
	ResponseStatus     int               `json:"response_status"`
	ResponseBody       string            `json:"response_body,omitempty"`
	RequestSchema      json.RawMessage   `json:"request_schema,omitempty"`
	ResponseSchema     json.RawMessage   `json:"response_schema,omitempty"`
	ParametersSchema   json.RawMessage   `json:"parameters_schema,omitempty"`
	Plugin             string            `json:"plugin,omitempty"`
	UpstreamAuthRef    string            `json:"upstream_auth_ref,omitempty"`
	UpstreamURL        string            `json:"upstream_url,omitempty"`
	UpstreamPath       string            `json:"upstream_path,omitempty"`
	StripPath          bool              `json:"strip_path,omitempty"`
	UpstreamTimeoutMS  int               `json:"upstream_timeout_ms,omitempty"`
	UpstreamRetries    int               `json:"upstream_retries,omitempty"`
	CircuitThreshold   int               `json:"circuit_breaker_threshold,omitempty"`
	CircuitResetSecs   int               `json:"circuit_breaker_reset_seconds,omitempty"`
	Enabled            bool              `json:"enabled"`
	PublishedAt        *time.Time        `json:"published_at,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

type CreateAPIRequest struct {
	PublicVisible      bool              `json:"public_visible"`
	PublicTitle        string            `json:"public_title"`
	PublicSummary      string            `json:"public_summary"`
	PublicCategory     string            `json:"public_category"`
	Name               string            `json:"name"`
	Description        string            `json:"description"`
	Method             string            `json:"method"`
	Path               string            `json:"path"`
	AuthMode           string            `json:"auth_mode"`
	AuthConfig         map[string]string `json:"auth_config"`
	RateLimitPerMinute int               `json:"rate_limit_per_minute"`
	DailyQuota         int               `json:"daily_quota"`
	MonthlyQuota       int               `json:"monthly_quota"`
	ResponseStatus     int               `json:"response_status"`
	ResponseBody       string            `json:"response_body"`
	RequestSchema      json.RawMessage   `json:"request_schema"`
	ResponseSchema     json.RawMessage   `json:"response_schema"`
	ParametersSchema   json.RawMessage   `json:"parameters_schema"`
	Plugin             string            `json:"plugin"`
	UpstreamAuthRef    string            `json:"upstream_auth_ref,omitempty"`
	UpstreamURL        string            `json:"upstream_url"`
	UpstreamPath       string            `json:"upstream_path"`
	StripPath          bool              `json:"strip_path"`
	UpstreamTimeoutMS  int               `json:"upstream_timeout_ms"`
	UpstreamRetries    int               `json:"upstream_retries"`
	CircuitThreshold   int               `json:"circuit_breaker_threshold"`
	CircuitResetSecs   int               `json:"circuit_breaker_reset_seconds"`
}

type UpdateAPIRequest = CreateAPIRequest

type OpenAPIImportRequest struct {
	Document     json.RawMessage   `json:"document"`
	DocumentYAML string            `json:"document_yaml"`
	UpstreamURL  string            `json:"upstream_url"`
	AuthMode     string            `json:"auth_mode"`
	AuthConfig   map[string]string `json:"auth_config"`
	PathPrefix   string            `json:"path_prefix"`
}

type Credential struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Prefix       string     `json:"prefix"`
	Hash         string     `json:"-"`
	EncryptedKey string     `json:"-"`
	KeyAvailable bool       `json:"api_key_available"`
	Revoked      bool       `json:"revoked"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

type CreateCredentialRequest struct {
	Name      string     `json:"name"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role,omitempty"`
	Roles        []string  `json:"roles,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type CreateUserRequest struct {
	Username string   `json:"username"`
	Email    string   `json:"email"`
	Password string   `json:"password"`
	Role     string   `json:"role,omitempty"`
	Roles    []string `json:"roles,omitempty"`
}

// UpdateUserProfileRequest never accepts role, status, or password hashes.
// Omitted password preserves the existing password; an explicit empty one is invalid.
type UpdateUserProfileRequest struct {
	Username        *string `json:"username,omitempty"`
	Email           *string `json:"email,omitempty"`
	Password        *string `json:"password,omitempty"`
	CurrentPassword string  `json:"current_password,omitempty"`
}

// UserProfileUpdate is an internal optimistic guard for an atomic credential update.
type UserProfileUpdate struct {
	Username             string
	Email                string
	ExpectedEmail        string
	PasswordHash         string
	ExpectedUsername     string
	ExpectedPasswordHash string
}

type LoginRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type Release struct {
	ID          int64     `json:"id"`
	APIID       string    `json:"api_id"`
	Version     int       `json:"version"`
	Snapshot    API       `json:"snapshot"`
	PublishedAt time.Time `json:"published_at"`
}

type Permission struct {
	ID          string `json:"id"`
	Code        string `json:"code"`
	Description string `json:"description"`
}

type Role struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

type CreateRoleRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

type UpdateUserRolesRequest struct {
	Roles []string `json:"roles"`
}

type UpdateUserStatusRequest struct {
	Status string `json:"status"`
}

type PluginData struct {
	PluginName string          `json:"plugin_name"`
	Namespace  string          `json:"namespace"`
	Key        string          `json:"key"`
	Value      json.RawMessage `json:"value"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

type Plugin struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Runtime     string          `json:"runtime"`
	Manifest    json.RawMessage `json:"manifest"`
	Checksum    string          `json:"checksum"`
	StoragePath string          `json:"-"`
	Enabled     bool            `json:"enabled"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type AuditLog struct {
	ID           int64          `json:"id"`
	ActorID      string         `json:"actor_id,omitempty"`
	ActorType    string         `json:"actor_type"`
	ActorEmail   string         `json:"actor_email,omitempty"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id,omitempty"`
	RequestID    string         `json:"request_id,omitempty"`
	Method       string         `json:"method,omitempty"`
	Path         string         `json:"path,omitempty"`
	RemoteAddr   string         `json:"remote_addr,omitempty"`
	UserAgent    string         `json:"user_agent,omitempty"`
	StatusCode   int            `json:"status_code,omitempty"`
	Details      map[string]any `json:"details,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

type AuditLogQuery struct {
	Page         int
	PageSize     int
	Action       string
	ResourceType string
	ActorID      string
	RequestID    string
	From         *time.Time
	To           *time.Time
}

type AuditLogPage struct {
	Items    []AuditLog `json:"items"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	Total    int64      `json:"total"`
}

type Session struct {
	// Creation-only guards prevent a login verified against old credentials from
	// creating a usable session after a concurrent rename or password reset.
	AuthenticatedUsername     string    `json:"-"`
	AuthenticatedPasswordHash string    `json:"-"`
	Hash                      string    `json:"-"`
	UserID                    string    `json:"user_id"`
	ExpiresAt                 time.Time `json:"expires_at"`
}

// Recovery credentials are never serialized into API responses or stored in plaintext.
type PasswordReset struct {
	Hash         string    `json:"-"`
	UserID       string    `json:"-"`
	Username     string    `json:"-"`
	Email        string    `json:"-"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"-"`
	ExpiresAt    time.Time `json:"-"`
}
