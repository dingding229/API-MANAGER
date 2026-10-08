package model

import (
	"encoding/json"
	"time"
)

// PluginRuntimePolicy is an explicit, versioned grant, separate from an author's
// manifest declaration and from ordinary plugin business settings.
type PluginRuntimePolicy struct {
	Version            int64    `json:"version"`
	NetworkEnabled     bool     `json:"network_enabled"`
	AllowedOrigins     []string `json:"allowed_origins"`
	TimeoutMS          int      `json:"timeout_ms"`
	ResponseBytes      int      `json:"response_bytes"`
	SessionPersistence bool     `json:"session_persistence"`
	SessionCache       bool     `json:"session_cache"`
	AllowNonexpiring   bool     `json:"allow_nonexpiring_sessions"`
	SessionTTLSeconds  int      `json:"session_ttl_seconds"`
	MaxEntries         int      `json:"max_entries"`
	MaxValueBytes      int      `json:"max_value_bytes"`
}
type PluginSession struct {
	PluginID       string
	KeyHash        string
	EncryptedValue string
	Version        int64
	ExpiresAt      *time.Time
}
type PluginSessionValue struct {
	Value     json.RawMessage `json:"value,omitempty"`
	Found     bool            `json:"found"`
	Version   int64           `json:"version,omitempty"`
	ExpiresAt *time.Time      `json:"expires_at,omitempty"`
}
