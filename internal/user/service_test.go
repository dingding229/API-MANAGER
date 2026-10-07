package user

import (
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"errors"
	"strings"
	"testing"
	"time"

	"api-manager/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func TestCreateWithRolesEnforcesPasswordLengthAndCost(t *testing.T) {
	memory := store.NewMemory()
	service := NewService(memory)
	if _, err := service.Create("short@example.com", "1234567", "member"); err == nil {
		t.Fatal("7-byte password was accepted")
	}
	if _, err := service.Create("eight-byte-user", "12345678", "member"); err != nil {
		t.Fatalf("8-byte password was rejected: %v", err)
	}
	created, err := service.Create("valid-user", "123456789012", "member")
	if err != nil {
		t.Fatalf("12-byte password was rejected: %v", err)
	}
	cost, err := bcrypt.Cost([]byte(created.PasswordHash))
	if err != nil {
		t.Fatalf("read bcrypt cost: %v", err)
	}
	if cost != passwordHashCost {
		t.Fatalf("bcrypt cost = %d, want %d", cost, passwordHashCost)
	}
	if _, err := service.Create("long@example.com", strings.Repeat("x", 73), "member"); err == nil {
		t.Fatal("73-byte password was accepted")
	}
}

func TestExistingAccountsAreNotResetAndSessionHashesArePersisted(t *testing.T) {
	m := store.NewMemory()
	s := NewService(m)
	if err := s.EnsureInitialAdmin("admin", "original-password"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureInitialAdmin("another-admin", "replacement-password"); err != nil {
		t.Fatal(err)
	}
	u, token, err := s.Authenticate("admin", "original-password")
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := m.GetSession(auth.HashAPIKey(token))
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Hash == token || persisted.UserID != u.ID {
		t.Fatal("session stored incorrectly")
	}
	if _, _, err = s.Authenticate("admin", "replacement-password"); err == nil {
		t.Fatal("initial password reset existing account")
	}
	// A fresh Service instance validates the same persisted session.
	if _, err = NewService(m).ValidateSession(token); err != nil {
		t.Fatal(err)
	}
	expired := persisted
	expired.ExpiresAt = time.Now().Add(-time.Second)
	if err = m.CreateSession(expired); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ValidateSession(token); err == nil {
		t.Fatal("expired session accepted")
	}
}
func TestDisabledUserSessionIsImmediatelyRejected(t *testing.T) {
	m := store.NewMemory()
	s := NewService(m)
	user, err := s.Create("member", "viewer-password", "member")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Authenticate("member", "viewer-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetStatus(user.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ValidateSession(token); err == nil {
		t.Fatal("disabled user session accepted")
	}
}

func TestReenablingUserDoesNotReviveOldSessions(t *testing.T) {
	s := NewService(store.NewMemory())
	u, err := s.Create("viewer-two", "viewer-password", "member")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Authenticate("viewer-two", "viewer-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetStatus(u.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetStatus(u.ID, "active"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ValidateSession(token); err == nil {
		t.Fatal("disabled/re-enabled account revived a revoked session")
	}
	if _, _, err = s.Authenticate("viewer-two", "viewer-password"); err != nil {
		t.Fatal("re-enabled user cannot log in")
	}
}

func TestRolePermissionUpdatesValidateAndProtectSuperAdmin(t *testing.T) {
	m := store.NewMemory()
	service := NewService(m)
	if err := service.UpdateRolePermissions("api_developer", []string{"api.read", "api.read", "observability.read"}); err != nil {
		t.Fatal(err)
	}
	role, err := m.GetRoleByName("api_developer")
	if err != nil {
		t.Fatal(err)
	}
	if len(role.Permissions) != 2 {
		t.Fatalf("permissions not normalized: %#v", role.Permissions)
	}
	if err := service.UpdateRolePermissions("api_developer", []string{"unknown.permission"}); err == nil {
		t.Fatal("unknown permission accepted")
	}
	if err := service.UpdateRolePermissions("super_admin", []string{"api.read"}); err == nil {
		t.Fatal("super_admin mutation accepted")
	}
}

func TestUpdateProfileReauthenticatesSelfAndRevokesSessions(t *testing.T) {
	m := store.NewMemory()
	s := NewService(m)
	if err := s.EnsureInitialAdmin("admin", "admin-password"); err != nil {
		t.Fatal(err)
	}
	admin, oldToken, err := s.Authenticate("admin", "admin-password")
	if err != nil {
		t.Fatal(err)
	}
	username := "renamed-admin"
	password := "new-admin-password"
	updated, revoked, err := s.UpdateProfile(admin.ID, admin.ID, model.UpdateUserProfileRequest{Username: &username, Password: &password, CurrentPassword: "admin-password"})
	if err != nil {
		t.Fatal(err)
	}
	if !revoked || updated.Username != username {
		t.Fatalf("profile update result = %#v, revoked=%v", updated, revoked)
	}
	if _, err := s.ValidateSession(oldToken); err == nil {
		t.Fatal("old session survived password update")
	}
	if _, _, err := s.Authenticate("admin", "admin-password"); err == nil {
		t.Fatal("old credentials still accepted")
	}
	if _, _, err := s.Authenticate(username, password); err != nil {
		t.Fatalf("new credentials rejected: %v", err)
	}
}

func TestUpdateProfileRequiresCurrentPasswordForSelfAndProtectsPrivilegedTarget(t *testing.T) {
	m := store.NewMemory()
	s := NewService(m)
	if err := s.EnsureInitialAdmin("admin", "admin-password"); err != nil {
		t.Fatal(err)
	}
	reader, err := s.Create("reader", "reader-password", "member")
	if err != nil {
		t.Fatal(err)
	}
	username := "reader-renamed"
	if _, _, err := s.UpdateProfile(reader.ID, reader.ID, model.UpdateUserProfileRequest{Username: &username}); !errors.Is(err, ErrCurrentPassword) {
		t.Fatalf("missing current password error = %v", err)
	}
	admin, err := m.GetUserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpdateProfile(reader.ID, admin.ID, model.UpdateUserProfileRequest{Username: &username}); !errors.Is(err, ErrProfileForbidden) {
		t.Fatalf("privileged target error = %v", err)
	}
}
