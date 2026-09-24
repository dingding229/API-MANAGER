package ratelimit

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

var fixedWindowScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
if count <= tonumber(ARGV[2]) then
  return 1
end
return 0
`)

type Redis struct {
	client *redis.Client
}

func NewRedis(ctx context.Context, address, password string, database int) (*Redis, error) {
	return NewRedisWithTLS(ctx, address, "", password, database, false, "")
}

func NewRedisWithTLS(ctx context.Context, address, username, password string, database int, enabled bool, caFile string) (*Redis, error) {
	options := &redis.Options{Addr: address, Username: username, Password: password, DB: database, DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second}
	if enabled {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("redis TLS address must include host and port: %w", err)
		}
		config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
		if caFile != "" {
			pool, err := x509.SystemCertPool()
			if err != nil {
				return nil, fmt.Errorf("load system CAs: %w", err)
			}
			cert, err := os.ReadFile(caFile)
			if err != nil {
				return nil, fmt.Errorf("read redis CA: %w", err)
			}
			if !pool.AppendCertsFromPEM(cert) {
				return nil, fmt.Errorf("redis CA file contains no certificates")
			}
			config.RootCAs = pool
		}
		options.TLSConfig = config
	}
	client := redis.NewClient(options)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &Redis{client: client}, nil
}

func (r *Redis) Close() error                   { return r.client.Close() }
func (r *Redis) Ping(ctx context.Context) error { return r.client.Ping(ctx).Err() }

func (r *Redis) Allow(key string, limit int, windowSize time.Duration, now time.Time) bool {
	if limit <= 0 {
		return true
	}
	if windowSize <= 0 {
		windowSize = time.Minute
	}
	// Include the current window in the key so expired windows never need cleanup races.
	bucket := now.UnixNano() / windowSize.Nanoseconds()
	sum := sha256.Sum256([]byte(key))
	redisKey := fmt.Sprintf("api-manager:rate:%s:%d", hex.EncodeToString(sum[:]), bucket)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	value, err := fixedWindowScript.Run(ctx, r.client, []string{redisKey}, windowSize.Milliseconds(), limit).Int()
	return err == nil && value == 1
}

func (r *Redis) UseNonce(key string, ttl time.Duration) bool {
	sum := sha256.Sum256([]byte(key))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ok, err := r.client.SetNX(ctx, "api-manager:nonce:"+hex.EncodeToString(sum[:]), "1", ttl).Result()
	return err == nil && ok
}
