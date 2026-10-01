package user

import (
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"time"
)

type bootstrapStore interface {
	ConfigureBootstrap(string) error
	BootstrapAvailable(string) (bool, error)
	RegisterInitialAdmin(string, model.User) error
}

var ErrSetupClosed = errors.New("registration unavailable or invalid key")

func bootstrapDigest(key string) string {
	if key == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}
func (s *Service) ConfigureBootstrap(key string) error {
	bs, ok := s.store.(bootstrapStore)
	if !ok {
		return errors.New("persistent bootstrap storage required")
	}
	count, err := s.CountChecked()
	if err != nil {
		return err
	}
	if key != "" && len(key) < 32 {
		return errors.New("bootstrap key requires at least 32 bytes")
	}
	if count == 0 && key == "" {
		return errors.New("ADMIN_BOOTSTRAP_KEY is required to register the first administrator")
	}
	s.bootstrapHash = bootstrapDigest(key)
	return bs.ConfigureBootstrap(s.bootstrapHash)
}
func (s *Service) SetupAvailable() (bool, error) {
	bs, ok := s.store.(bootstrapStore)
	if !ok {
		return false, nil
	}
	return bs.BootstrapAvailable(s.bootstrapHash)
}
func (s *Service) RegisterInitialAdmin(key, username, email, password string) (model.User, error) {
	hash := bootstrapDigest(key)
	if hash == "" || subtle.ConstantTimeCompare([]byte(hash), []byte(s.bootstrapHash)) != 1 {
		return model.User{}, ErrSetupClosed
	}
	available, err := s.SetupAvailable()
	if err != nil {
		return model.User{}, err
	}
	if !available {
		return model.User{}, ErrSetupClosed
	}
	username, email = normalizeUsername(username), normalizeEmail(email)
	if !usernamePattern.MatchString(username) || !validEmail(email) || !validPassword(password) {
		return model.User{}, ErrInvalidProfile
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), passwordHashCost)
	if err != nil {
		return model.User{}, err
	}
	now := time.Now().UTC()
	u := model.User{ID: ids.NewUUID(), Username: username, Email: email, PasswordHash: string(passwordHash), Role: "super_admin", Roles: []string{"super_admin"}, Status: "active", CreatedAt: now, UpdatedAt: now}
	if err = s.store.(bootstrapStore).RegisterInitialAdmin(hash, u); err != nil {
		return model.User{}, ErrSetupClosed
	}
	return u, nil
}
