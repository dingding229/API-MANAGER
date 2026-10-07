package user

import (
	"api-manager/internal/model"
	"api-manager/internal/store"
	"strings"
	"testing"
)

func TestUsernamesHaveBoundedNewIdentifiersAndPreserveLegacy(t *testing.T) {
	for _, v := range []string{"abc", strings.Repeat("a", 20), "Abc123"} {
		if !validUsername(v) {
			t.Fatal("valid name", v)
		}
	}
	for _, v := range []string{"ab", strings.Repeat("a", 21), "abc@example.test", "<img>", "abc def", "abc.def", "abc_def", "abc-def"} {
		if validUsername(v) {
			t.Fatal("unsafe name", v)
		}
	}
	m := store.NewMemory()
	s := NewService(m)
	u, e := s.Create("legacy", "Password888", "member")
	if e != nil {
		t.Fatal(e)
	}
	name := u.Username
	req := model.UpdateUserProfileRequest{Username: &name, CurrentPassword: "Password888"}
	if _, _, e = s.UpdateProfile(u.ID, u.ID, req); e != nil {
		t.Fatal(e)
	}
}
