package store

import (
	"api-manager/internal/model"
	"sync"
	"testing"
)

func TestMemoryMethodOverlapConcurrent(t *testing.T) {
	m := NewMemory()
	var wg sync.WaitGroup
	wins := make(chan bool, 2)
	for _, api := range []model.API{{ID: "a", Method: "GET", Methods: []string{"GET", "POST"}, Path: "/api/test/{id}"}, {ID: "b", Method: "POST", Methods: []string{"POST", "PUT"}, Path: "/api/test/{name}"}} {
		wg.Add(1)
		go func(a model.API) { defer wg.Done(); wins <- m.CreateAPI(a) == nil }(api)
	}
	wg.Wait()
	close(wins)
	count := 0
	for ok := range wins {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("ambiguous route writes accepted: %d", count)
	}
}

func TestLastAdministratorSurvivesConcurrentDemotion(t *testing.T) {
	m := NewMemory()
	for _, id := range []string{"a", "b"} {
		if err := m.CreateUser(model.User{ID: id, Username: id, Role: "super_admin", Roles: []string{"super_admin"}, Status: "active"}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	wins := make(chan bool, 2)
	for _, id := range []string{"a", "b"} {
		wg.Add(1)
		go func(id string) { defer wg.Done(); wins <- m.AssignUserRoles(id, []string{"viewer"}) == nil }(id)
	}
	wg.Wait()
	close(wins)
	count := 0
	for ok := range wins {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("demotions accepted=%d", count)
	}
}
