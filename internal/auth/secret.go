package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

var ErrSecretUnavailable = errors.New("credential secret is unavailable")

// EncryptSecret keeps a recoverable copy of a newly generated credential for
// authorized administration. The API gateway still authenticates with HashAPIKey.
func EncryptSecret(secret, plaintext string) (string, error) {
	if secret == "" || plaintext == "" {
		return "", ErrSecretUnavailable
	}
	sum := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func DecryptSecret(secret, encoded string) (string, error) {
	if secret == "" || encoded == "" {
		return "", ErrSecretUnavailable
	}
	sum := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	sealed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < gcm.NonceSize() {
		return "", ErrSecretUnavailable
	}
	plaintext, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
	if err != nil {
		return "", ErrSecretUnavailable
	}
	return string(plaintext), nil
}
