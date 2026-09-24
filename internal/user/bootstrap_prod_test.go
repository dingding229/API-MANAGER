package user

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"api-manager/internal/store"
)

type failingCountStore struct{ *store.Memory }

func (f failingCountStore) CountUsersChecked() (int, error) {
	return 0, errors.New("database unavailable")
}

func TestBootstrapFailsClosedWhenUserCountFails(t *testing.T) {
	memory := store.NewMemory()
	handler := NewHTTP(NewService(failingCountStore{memory}, "test-jwt-secret", 0), "bootstrap-secret")
	r := httptest.NewRequest(http.MethodPost, "/auth/v1/bootstrap", strings.NewReader(`{"email":"admin@example.com","password":"password123"}`))
	r.Header.Set("X-Admin-Token", "bootstrap-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || memory.CountUsers() != 0 {
		t.Fatalf("status=%d users=%d", w.Code, memory.CountUsers())
	}
}

func TestConcurrentBootstrapCreatesOnlyOneAdministrator(t *testing.T) {
	memory := store.NewMemory()
	handler := NewHTTP(NewService(memory, "test-jwt-secret", 0), "bootstrap-secret")
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for _, email := range []string{"first@example.com", "second@example.com"} {
		wg.Add(1)
		go func(email string) {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodPost, "/auth/v1/bootstrap", strings.NewReader(`{"email":"`+email+`","password":"password123"}`))
			r.Header.Set("X-Admin-Token", "bootstrap-secret")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			statuses <- w.Code
		}(email)
	}
	wg.Wait()
	close(statuses)
	var created, denied int
	for status := range statuses {
		if status == http.StatusCreated {
			created++
		} else if status == http.StatusForbidden {
			denied++
		} else {
			t.Fatalf("unexpected status %d", status)
		}
	}
	if created != 1 || denied != 1 || memory.CountUsers() != 1 {
		t.Fatalf("created=%d denied=%d users=%d", created, denied, memory.CountUsers())
	}
}
