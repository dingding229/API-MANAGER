package api

import (
	"api-manager/internal/plugin"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
)

func TestPersonalSessionsAreOnlyManagedInUserCenter(t *testing.T) {
	m := store.NewMemory()
	u := user.NewService(m)
	actor, e := u.Create("owner", "Password888", "super_admin")
	if e != nil {
		t.Fatal(e)
	}
	_, token, e := u.Authenticate("owner", "Password888")
	if e != nil {
		t.Fatal(e)
	}
	admin := NewAdminWithUserManagement(m, plugin.NewRegistry(), u, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, method := range []string{"GET", "DELETE"} {
		r := httptest.NewRequest(method, "/admin/v1/users/"+actor.ID+"/sessions", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		admin.ServeHTTP(w, r)
		if w.Code != 410 {
			t.Fatal(method, w.Code, w.Body.String())
		}
	}
}
