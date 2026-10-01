package user

import (
	"api-manager/internal/auth"
	"strings"
	"testing"
	"time"

	"api-manager/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func TestCreateWithRolesEnforcesPasswordLengthAndCost(t *testing.T) {
	memory := store.NewMemory()
	service := NewService(memory)
	if _, err := service.Create("short@example.com", "12345678901", "viewer"); err == nil {
		t.Fatal("11-byte password was accepted")
	}
	created, err := service.Create("valid@example.com", "123456789012", "viewer")
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
	if _, err := service.Create("long@example.com", strings.Repeat("x", 73), "viewer"); err == nil {
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
	user, err := s.Create("viewer", "viewer-password", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Authenticate("viewer", "viewer-password")
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
	u, err := s.Create("viewer-two", "viewer-password", "viewer")
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
	if err := service.UpdateRolePermissions("viewer", []string{"api.read", "api.read", "observability.read"}); err != nil {
		t.Fatal(err)
	}
	role, err := m.GetRoleByName("viewer")
	if err != nil {
		t.Fatal(err)
	}
	if len(role.Permissions) != 2 {
		t.Fatalf("permissions not normalized: %#v", role.Permissions)
	}
	if err := service.UpdateRolePermissions("viewer", []string{"unknown.permission"}); err == nil {
		t.Fatal("unknown permission accepted")
	}
	if err := service.UpdateRolePermissions("super_admin", []string{"api.read"}); err == nil {
		t.Fatal("super_admin mutation accepted")
	}
}
