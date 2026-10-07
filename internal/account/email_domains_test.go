package account

import (
	"api-manager/internal/model"
	"testing"
)

func TestRegistrationDomainPolicyIsExactAndNormalized(t *testing.T) {
	v, e := normalizeEmailDomains([]string{"@Example.com", " gmail.com ", "example.com"})
	if e != nil || len(v) != 2 || v[0] != "example.com" {
		t.Fatal(v, e)
	}
	cfg := model.SecuritySettings{AllowedEmailDomains: v}
	for _, tc := range []struct {
		email string
		want  bool
	}{{"a@example.com", true}, {"a@gmail.com", true}, {"a@sub.example.com", false}, {"a@example.com.evil.test", false}, {"a@other.com", false}} {
		if emailDomainAllowed(cfg, tc.email) != tc.want {
			t.Fatal(tc)
		}
	}
	for _, bad := range []string{"", "*.example.com", "https://example.com", "example.com:25", "127.0.0.1", "@example.com.evil/", "com"} {
		if _, e := normalizeEmailDomains([]string{bad}); e == nil {
			t.Fatal("bad registration policy", bad)
		}
	}
}
