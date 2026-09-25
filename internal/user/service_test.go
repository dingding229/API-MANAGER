package user

import (
	"errors"
	"strings"
	"testing"
	"time"

	"api-manager/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func TestCreateWithRolesEnforcesPasswordLengthAndCost(t *testing.T) {
	memory := store.NewMemory()
	service := NewService(memory, strings.Repeat("s", 32), time.Hour)
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

func TestAuthenticateReturnsSameErrorForUnknownUserAndWrongPassword(t *testing.T) {
	memory := store.NewMemory()
	service := NewService(memory, strings.Repeat("s", 32), time.Hour)
	if _, err := service.Create("known@example.com", "correct-password", "viewer"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ email, password string }{
		{email: "missing@example.com", password: "correct-password"},
		{email: "known@example.com", password: "wrong-password"},
	} {
		if _, _, err := service.Authenticate(tc.email, tc.password); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("Authenticate(%q) error = %v, want ErrInvalidCredentials", tc.email, err)
		}
	}
}
