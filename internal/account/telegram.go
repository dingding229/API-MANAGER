package account

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

const telegramIssuer = "https://oauth.telegram.org"
const telegramJWKS = telegramIssuer + "/.well-known/jwks.json"

type telegramKey struct{ Kid, Kty, Alg, Use, N, E, Crv, X, Y string }
type telegramKeyCache struct {
	mu                   sync.Mutex
	keys                 []telegramKey
	expires, nextRefresh time.Time
}

func (s *Service) telegramKeys(ctx context.Context, kid string) ([]telegramKey, error) {
	c := &s.telegramCache
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if now.Before(c.expires) {
		for _, key := range c.keys {
			if key.Kid == kid {
				return c.keys, nil
			}
		}
		if now.Before(c.nextRefresh) {
			return nil, errors.New("unknown Telegram signing key")
		}
	}
	if now.Before(c.nextRefresh) {
		return nil, errors.New("Telegram signing keys refresh limited")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", telegramJWKS, nil)
	if e != nil {
		return nil, e
	}
	c.nextRefresh = now.Add(time.Minute)
	response, e := s.client.Do(req)
	if e != nil {
		return nil, errors.New("Telegram keys unavailable")
	}
	defer response.Body.Close()
	data, e := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if e != nil || response.StatusCode != 200 || len(data) > 64<<10 {
		return nil, errors.New("Telegram keys unavailable")
	}
	var document struct {
		Keys []telegramKey `json:"keys"`
	}
	if json.Unmarshal(data, &document) != nil || len(document.Keys) < 1 || len(document.Keys) > 16 {
		return nil, errors.New("invalid Telegram keys")
	}
	c.keys = document.Keys
	c.expires = now.Add(10 * time.Minute)
	return c.keys, nil
}
func decodeJWTPart(value string, limit int) ([]byte, error) {
	if len(value) > limit*2 {
		return nil, errors.New("identity token too large")
	}
	data, e := base64.RawURLEncoding.DecodeString(value)
	if e != nil || len(data) > limit {
		return nil, errors.New("invalid identity token")
	}
	return data, nil
}
func verifyTelegramSignature(key telegramKey, alg string, message, signature []byte) bool {
	if key.Use != "" && key.Use != "sig" || key.Alg != "" && key.Alg != alg {
		return false
	}
	digest := sha256.Sum256(message)
	switch alg {
	case "RS256":
		if key.Kty != "RSA" {
			return false
		}
		n, e := decodeJWTPart(key.N, 1024)
		if e != nil || len(n) < 256 {
			return false
		}
		exponent, e := decodeJWTPart(key.E, 4)
		if e != nil {
			return false
		}
		v := new(big.Int).SetBytes(exponent).Int64()
		if v < 3 || v > 2147483647 || v%2 == 0 {
			return false
		}
		return rsa.VerifyPKCS1v15(&rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(v)}, crypto.SHA256, digest[:], signature) == nil
	case "ES256":
		if key.Kty != "EC" || key.Crv != "P-256" || len(signature) != 64 {
			return false
		}
		x, e := decodeJWTPart(key.X, 32)
		if e != nil || len(x) != 32 {
			return false
		}
		y, e := decodeJWTPart(key.Y, 32)
		if e != nil || len(y) != 32 {
			return false
		}
		pub := ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
			return false
		}
		return ecdsa.Verify(&pub, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:]))
	case "EdDSA":
		if key.Kty != "OKP" || key.Crv != "Ed25519" {
			return false
		}
		pub, e := decodeJWTPart(key.X, ed25519.PublicKeySize)
		return e == nil && len(pub) == ed25519.PublicKeySize && ed25519.Verify(ed25519.PublicKey(pub), message, signature)
	}
	return false
}

// Only identity tokens obtained by our server's code exchange are accepted. No browser claims/JWKS URL are trusted.
func (s *Service) telegramIdentity(ctx context.Context, token, clientID, nonce string) (string, string, error) {
	fail := errors.New("Telegram 身份验证失败，请重新开始登录")
	if token == "" || len(token) > 16<<10 || nonce == "" {
		return "", "", fail
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", "", fail
	}
	header, e := decodeJWTPart(parts[0], 2048)
	if e != nil {
		return "", "", fail
	}
	var h struct {
		Alg, Kid string
		Crit     []string `json:"crit"`
	}
	if json.Unmarshal(header, &h) != nil || len(h.Kid) == 0 || len(h.Kid) > 128 || len(h.Crit) != 0 || (h.Alg != "RS256" && h.Alg != "ES256" && h.Alg != "EdDSA") {
		return "", "", fail
	}
	signature, e := decodeJWTPart(parts[2], 1024)
	if e != nil {
		return "", "", fail
	}
	keys, e := s.telegramKeys(ctx, h.Kid)
	if e != nil {
		return "", "", fail
	}
	valid := false
	for _, key := range keys {
		if key.Kid == h.Kid && verifyTelegramSignature(key, h.Alg, []byte(parts[0]+"."+parts[1]), signature) {
			valid = true
			break
		}
	}
	if !valid {
		return "", "", fail
	}
	claims, e := decodeJWTPart(parts[1], 12<<10)
	if e != nil {
		return "", "", fail
	}
	var c struct {
		Iss, Sub, Name, Nonce, Azp string
		Aud                        json.RawMessage
		Iat, Exp, Nbf              int64
	}
	if json.Unmarshal(claims, &c) != nil {
		return "", "", fail
	}
	now := time.Now().Unix()
	if c.Iss != telegramIssuer || len(c.Sub) == 0 || len(c.Sub) > 128 || c.Iat <= 0 || c.Iat > now+60 || c.Exp <= now || c.Exp <= c.Iat || c.Nbf > now+60 || subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(nonce)) != 1 {
		return "", "", fail
	}
	var audience string
	var audiences []string
	if json.Unmarshal(c.Aud, &audience) == nil {
		audiences = []string{audience}
	} else if json.Unmarshal(c.Aud, &audiences) != nil {
		return "", "", fail
	}
	matched := false
	for _, a := range audiences {
		matched = matched || a == clientID
	}
	if !matched || len(audiences) > 1 && c.Azp != clientID {
		return "", "", fail
	}
	if len(c.Name) > 256 {
		c.Name = ""
	}
	return c.Sub, c.Name, nil
}
