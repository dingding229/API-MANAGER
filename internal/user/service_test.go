package user

import (
	"strings"
	"testing"

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
