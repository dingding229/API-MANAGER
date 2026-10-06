package model

import (
	"errors"
	"time"
)

const MaxPluginCacheBody = 1 << 20

type PluginCacheConfig struct {
	Enabled    bool `json:"enabled"`
	TTLSeconds int  `json:"ttl_seconds"`
	MaxEntries int  `json:"max_entries"`
	CachePOST  bool `json:"cache_post"`
}

func (c PluginCacheConfig) Validate(plugin string) error {
	if c.TTLSeconds < 0 || c.TTLSeconds > 604800 || c.MaxEntries < 0 || c.MaxEntries > 10000 {
		return errors.New("缓存有效期须在 1 至 604800 秒之间，最多缓存条目须在 1 至 10000 之间")
	}
	if c.Enabled && (plugin == "" || c.TTLSeconds < 1 || c.MaxEntries < 1) {
		return errors.New("启用缓存须选择插件，并填写有效期和条目上限")
	}
	return nil
}

type PluginCacheEntry struct {
	APIID        string
	Key          string
	Ciphertext   string
	APIUpdatedAt time.Time
	CreatedAt    time.Time
	ExpiresAt    time.Time
}
type PluginCacheStats struct {
	Entries int `json:"entries"`
	Expired int `json:"expired"`
}
