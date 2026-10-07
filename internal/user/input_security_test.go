package user

import (
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"strings"
	"testing"
	"time"
)

func TestUniqueAlphanumericUsernameAndLiteralSpecialPassword(t *testing.T) {
	m := store.NewMemory()
	s := NewService(m)
	password := `  '<svg>&"  `
	u, e := s.CreateWithContact("Security123", "person@example.test", password, []string{"member"})
	if e != nil {
		t.Fatal(e)
	}
	for _, identifier := range []string{u.Username, "PERSON@example.test"} {
		actual, _, err := s.Authenticate(identifier, password)
		if err != nil || actual.ID != u.ID {
			t.Fatal("literal special password or email identity was altered", err)
		}
	}
	if _, _, err := s.Authenticate(u.Username, strings.TrimSpace(password)); err == nil {
		t.Fatal("password was trimmed")
	}
	if _, _, err := s.Authenticate(u.Username, "  '&lt;svg&gt;&amp;\"  "); err == nil {
		t.Fatal("password was HTML-escaped")
	}
	raw, _ := json.Marshal(u)
	if strings.Contains(string(raw), password) || strings.Contains(string(raw), "$2") {
		t.Fatal("password material leaked")
	}
	if _, err := s.Create("security123", "Password888", "member"); !errors.Is(err, store.ErrConflict) {
		t.Fatal("duplicate normalized username accepted", err)
	}
	for _, name := range []string{"ab", strings.Repeat("x", 21), "name.withdot", "name_underscore", "name-dash", "a<svg>", "a&quote", "name space", "用户名称"} {
		if _, err := s.Create(name, "Password888", "member"); err == nil {
			t.Fatal("unsafe username accepted", name)
		}
	}
	for _, password := range []string{"1234567", strings.Repeat("a", 25)} {
		if _, err := s.Create("Length123", password, "member"); err == nil {
			t.Fatal("password bound ignored")
		}
	}
	if _, err := s.Create("Length123", strings.Repeat("a", 24), "member"); err != nil {
		t.Fatal("valid upper password boundary rejected", err)
	}
}

func TestUpgradePreservesLegacyNameAndLongPassword(t *testing.T) {
	m := store.NewMemory()
	s := NewService(m)
	password := strings.Repeat("a", 40)
	hash, e := bcrypt.GenerateFromPassword([]byte(password), passwordHashCost)
	if e != nil {
		t.Fatal(e)
	}
	u := model.User{ID: ids.NewUUID(), Username: "legacy-name", Email: "legacy@example.test", PasswordHash: string(hash), Role: "member", Roles: []string{"member"}, Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if e = m.CreateUser(u); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Authenticate(u.Username, password); e != nil {
		t.Fatal("legacy login blocked", e)
	}
	name := u.Username
	if _, _, e = s.UpdateProfile(u.ID, u.ID, model.UpdateUserProfileRequest{Username: &name, CurrentPassword: password}); e != nil {
		t.Fatal("unchanged legacy name blocked", e)
	}
}
