package model

import (
	"encoding/json"
	"time"
)

type Passkey struct {
	ID         string          `json:"id"`
	UserID     string          `json:"-"`
	RPID       string          `json:"rp_id"`
	Name       string          `json:"name"`
	Credential json.RawMessage `json:"-"`
	Revision   int64           `json:"-"`
	CreatedAt  time.Time       `json:"created_at"`
	LastUsedAt *time.Time      `json:"last_used_at"`
}
