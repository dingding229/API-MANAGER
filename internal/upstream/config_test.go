package upstream

import (
	"net/url"
	"strings"
	"testing"
)

func TestCredentialsPinOriginAndHideErrors(t *testing.T) {
	c, err := Parse(`{"game-discount":{"origin":"http://game-discount:8089/","api_key":"upstream-secret"}}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		url, ref string
		valid    bool
	}{
		{"http://game-discount:8089/v1/offers", "game-discount", true},
		{"http://other:8089/v1/offers", "game-discount", false},
		{"https://game-discount:8089/v1/offers", "game-discount", false},
		{"http://game-discount:80/v1/offers", "game-discount", false},
		{"http://user@game-discount:8089/", "game-discount", false},
		{"http://game-discount:8089/", "missing", false},
	} {
		u, _ := url.Parse(tc.url)
		key, err := c.Key(tc.ref, u)
		if (err == nil) != tc.valid {
			t.Fatalf("%s: %v", tc.url, err)
		}
		if !tc.valid && key != "" {
			t.Fatal("leaked key")
		}
	}
	_, err = Parse(`{"game-discount":{"origin":"http://host/path","api_key":"upstream-secret"}}`)
	if err == nil || strings.Contains(err.Error(), "upstream-secret") {
		t.Fatal("invalid validation")
	}
}

func TestEscapedPathMapping(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"/api/game/offers/example-001", "/v1/offers/example-001"},
		{"/api/game/offers/A%20B", "/v1/offers/A%20B"},
		{"/api/game/offers/A%2FB", "/v1/offers/A%2FB"},
	} {
		got, err := Path("/api/game/offers/{external_id}", tc.path, "/v1/offers/{external_id}")
		if err != nil || got != tc.want {
			t.Fatalf("%q %v", got, err)
		}
	}
	if err := ValidatePath("/api/game/{id}", "/v1/{missing}"); err == nil {
		t.Fatal("unknown parameter accepted")
	}
}
