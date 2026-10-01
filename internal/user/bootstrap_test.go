package user

import (
	"api-manager/internal/store"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestBootstrapSingleUseConcurrentAndPersistent(t *testing.T) {
	memory := store.NewMemory()
	s := NewService(memory)
	key := strings.Repeat("a", 64)
	if err := s.ConfigureBootstrap(key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterInitialAdmin("wrong", "admin", "admin@example.test", "Password88"); err == nil {
		t.Fatal("wrong key accepted")
	}
	var wg sync.WaitGroup
	successes := make(chan bool, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.RegisterInitialAdmin(key, "admin", "admin@example.test", "Password88")
			successes <- err == nil
		}()
	}
	wg.Wait()
	close(successes)
	count := 0
	for ok := range successes {
		if ok {
			count++
		}
	}
	if count != 1 || s.Count() != 1 {
		t.Fatalf("registration successes=%d users=%d", count, s.Count())
	}
	u, _ := memory.GetUserByUsername("admin")
	if u.Email != "admin@example.test" || u.Role != "super_admin" {
		t.Fatal("profile/role not stored")
	}
	if cost, _ := bcrypt.Cost([]byte(u.PasswordHash)); cost != 12 {
		t.Fatal("bcrypt cost weakened")
	}
	restarted := NewService(memory)
	if err := restarted.ConfigureBootstrap(strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if available, _ := restarted.SetupAvailable(); available {
		t.Fatal("used setup reopened after restart/key rotation")
	}
	// Even account removal must not reopen a consumed bootstrap.
	// Account deletion is intentionally unavailable to public user APIs.
}
func TestBootstrapUpgradeDoesNotModifyAccounts(t *testing.T) {
	memory := store.NewMemory()
	s := NewService(memory)
	u, err := s.Create("existing", "Password88", "super_admin")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConfigureBootstrap(strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if available, _ := s.SetupAvailable(); available {
		t.Fatal("upgrade opened bootstrap")
	}
	after, _ := memory.GetUserByID(u.ID)
	if after.PasswordHash != u.PasswordHash || after.Username != u.Username {
		t.Fatal("existing credentials overwritten")
	}
}
func TestBootstrapStrictDTOAndNoKeyDisclosure(t *testing.T) {
	s := NewService(store.NewMemory())
	key := strings.Repeat("a", 64)
	if err := s.ConfigureBootstrap(key); err != nil {
		t.Fatal(err)
	}
	h := NewHTTP(s)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/auth/v1/setup", nil))
	if strings.Contains(w.Body.String(), key) || strings.Contains(w.Body.String(), s.bootstrapHash) {
		t.Fatal("key disclosed")
	}
	body := `{"key":"` + key + `","username":"admin","email":"admin@example.test","password":"Password88","roles":["super_admin"]}`
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/auth/v1/setup", strings.NewReader(body)))
	if w.Code != http.StatusBadRequest || s.Count() != 0 {
		t.Fatal("role injection accepted")
	}
}
