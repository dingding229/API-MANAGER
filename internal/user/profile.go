package user

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"api-manager/internal/model"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidProfile   = errors.New("invalid profile")
	ErrProfileForbidden = errors.New("profile update forbidden")
	ErrCurrentPassword  = errors.New("current password is incorrect")
)

func normalizeUsername(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
func validUsername(value string) bool {
	address, err := mail.ParseAddress(value)
	return usernamePattern.MatchString(value) || (err == nil && address.Address == value && len(value) <= 254)
}
func validPassword(value string) bool { return len(value) >= 12 && len(value) <= 72 }

// UpdateProfile separates self-service reauthentication from delegated user management.
// Credential changes and revocation of all target sessions are a single storage operation.
func (s *Service) UpdateProfile(actorID, userID string, request model.UpdateUserProfileRequest) (model.User, bool, error) {
	actor, err := s.store.GetUserByID(actorID)
	if err != nil || actor.Status != "active" {
		return model.User{}, false, ErrProfileForbidden
	}
	target, err := s.store.GetUserByID(userID)
	if err != nil {
		return model.User{}, false, err
	}
	if actorID == userID {
		if len(request.CurrentPassword) > 72 || bcrypt.CompareHashAndPassword([]byte(target.PasswordHash), []byte(request.CurrentPassword)) != nil {
			return model.User{}, false, ErrCurrentPassword
		}
	} else if !s.canEditProfile(actorID, target) {
		return model.User{}, false, ErrProfileForbidden
	}
	if request.Username == nil && request.Password == nil {
		return model.User{}, false, fmt.Errorf("%w: username or new password is required", ErrInvalidProfile)
	}
	username := target.Email
	if request.Username != nil {
		username = normalizeUsername(*request.Username)
		if !validUsername(username) {
			return model.User{}, false, fmt.Errorf("%w: use a 3 to 64 character username or a valid email address", ErrInvalidProfile)
		}
	}
	passwordHash := target.PasswordHash
	if request.Password != nil {
		if !validPassword(*request.Password) {
			return model.User{}, false, fmt.Errorf("%w: password must contain 12 to 72 bytes", ErrInvalidProfile)
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*request.Password), passwordHashCost)
		if err != nil {
			return model.User{}, false, fmt.Errorf("hash password: %w", err)
		}
		passwordHash = string(hash)
	}
	return s.store.UpdateUserProfile(userID, model.UserProfileUpdate{Username: username, PasswordHash: passwordHash, ExpectedUsername: target.Email, ExpectedPasswordHash: target.PasswordHash})
}

func (s *Service) canEditProfile(actorID string, target model.User) bool {
	if !s.Can(actorID, "user.manage") {
		return false
	}
	if s.Can(actorID, "*") {
		return true
	}
	roles := target.Roles
	if len(roles) == 0 {
		roles = []string{target.Role}
	}
	for _, name := range roles {
		if name == "super_admin" || name == "tenant_admin" {
			return false
		}
		role, err := s.store.GetRoleByName(name)
		if err != nil {
			return false
		}
		for _, code := range role.Permissions {
			if code == "*" || code == "user.manage" {
				return false
			}
		}
	}
	return true
}
