package model

import "testing"

func TestNormalizeMethods(t *testing.T) {
	for _, tc := range []struct {
		primary string
		methods []string
		valid   bool
		count   int
	}{
		{"get", nil, true, 1}, {"", []string{"get", " POST ", "GET"}, true, 2}, {"POST", []string{"GET"}, false, 0}, {"", nil, false, 0}, {"", []string{"CONNECT"}, false, 0},
	} {
		methods, err := NormalizeMethods(tc.primary, tc.methods)
		if (err == nil) != tc.valid || (err == nil && len(methods) != tc.count) {
			t.Fatalf("%+v: %v %v", tc, methods, err)
		}
	}
}
func TestRoutesConflictAllMethods(t *testing.T) {
	a := API{Method: "GET", Methods: []string{"GET", "POST"}, Path: "/api/items/{id}"}
	for _, tc := range []struct {
		b        API
		conflict bool
	}{
		{API{Method: "POST", Path: "/api/items/{name}"}, true}, {API{Method: "DELETE", Path: a.Path}, false},
		{API{Method: "POST", Path: "/api/items/latest"}, false}, {API{Method: "POST", Path: "/api/other/{id}"}, false},
	} {
		if RoutesConflict(a, tc.b) != tc.conflict {
			t.Fatalf("%+v", tc)
		}
	}
}
