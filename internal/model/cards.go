package model

import (
	"encoding/json"
	"time"
)

type CardBatch struct {
	ID           string          `json:"id"`
	CreatorID    string          `json:"creator_id"`
	OperationID  string          `json:"-"`
	RequestHash  string          `json:"-"`
	Kind         string          `json:"kind"`
	AmountMicros int64           `json:"amount_micros"`
	Plan         json.RawMessage `json:"plan,omitempty"`
	Count        int             `json:"count"`
	ExpiresAt    time.Time       `json:"expires_at"`
	CreatedAt    time.Time       `json:"created_at"`
	Cards        []RedeemCard    `json:"cards,omitempty"`
}
type RedeemCard struct {
	ID            string     `json:"id"`
	BatchID       string     `json:"batch_id"`
	CodeHash      string     `json:"-"`
	Prefix        string     `json:"prefix"`
	EncryptedCode string     `json:"-"`
	Revoked       bool       `json:"revoked"`
	RedeemedBy    *string    `json:"redeemed_by,omitempty"`
	RedeemedAt    *time.Time `json:"redeemed_at,omitempty"`
}
