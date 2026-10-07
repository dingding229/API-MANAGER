package api

import (
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeveloperAPIManagementIsOwnerScoped(t *testing.T) {
	m := store.NewMemory()
	us := user.NewService(m)
	if e := us.EnsureInitialAdmin("admin", "Password888"); e != nil {
		t.Fatal(e)
	}
	d1, _ := us.Create("devone", "Password888", "api_developer")
	d2, _ := us.Create("devtwo", "Password888", "api_developer")
	_, one, _ := us.Authenticate(d1.Username, "Password888")
	_, two, _ := us.Authenticate(d2.Username, "Password888")
	_, at, _ := us.Authenticate("admin", "Password888")
	a := NewAdminWithUserManagement(m, plugin.NewRegistry(), us, slog.New(slog.NewTextHandler(io.Discard, nil)))
	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	w := call("POST", "/admin/v1/apis", one, `{"name":"own","path":"/api/own","method":"GET","auth_mode":"none","response_status":200}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v model.API
	json.Unmarshal(w.Body.Bytes(), &v)
	if v.OwnerUserID != d1.ID {
		t.Fatal("creator not persisted", v)
	}
	for _, tc := range []struct{ method, tail, body string }{{"GET", "", ""}, {"PUT", "", `{}`}, {"DELETE", "", ""}, {"POST", "/publish", ""}, {"POST", "/unpublish", ""}, {"GET", "/releases", ""}, {"POST", "/rollback/1", ""}, {"GET", "/cache", ""}, {"DELETE", "/cache", `{"confirm":true}`}} {
		if got := call(tc.method, "/admin/v1/apis/"+v.ID+tc.tail, two, tc.body).Code; got != 404 {
			t.Fatal("foreign API accessible", tc, got)
		}
	}
	if w := call("GET", "/admin/v1/apis", two, ""); w.Code != 200 || strings.Contains(w.Body.String(), v.ID) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("GET", "/admin/v1/openapi.json", two, ""); strings.Contains(w.Body.String(), v.Path) {
		t.Fatal("export leaked foreign route")
	}
	if w := call("GET", "/admin/v1/apis/"+v.ID, at, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = call("PUT", "/admin/v1/apis/"+v.ID, one, `{"name":"changed","path":"/api/own","method":"GET","auth_mode":"none","response_status":200}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	actual, _ := m.GetAPI(v.ID)
	if actual.OwnerUserID != d1.ID {
		t.Fatal("ownership lost on edit")
	}
	w = call("POST", "/admin/v1/apis", one, `{"name":"forged","path":"/api/forged","method":"GET","auth_mode":"none","owner_user_id":"bad"}`)
	if w.Code == 201 {
		var created model.API
		json.Unmarshal(w.Body.Bytes(), &created)
		if created.OwnerUserID != d1.ID {
			t.Fatal("client forged API ownership")
		}
	} else if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}

}
