package account

import (
	"net/http"
	"strings"
)

func accountPermission(r *http.Request) string {
	p := r.URL.Path
	switch {
	case p == "/account/v1/basic" || p == "/account/v1/verify-email":
		return "account.profile"
	case p == "/account/v1/totp" || strings.Contains(p, "/identities") || p == "/account/v1/profile":
		return "account.security"
	case strings.HasPrefix(p, "/account/v1/sessions"):
		return "account.sessions"
	case p == "/account/v1/session-users":
		return "user.read"
	case p == "/account/v1/logs":
		return "account.logs"
	case strings.HasPrefix(p, "/account/v1/keys"):
		if strings.HasSuffix(p, "/reveal") {
			return "account.keys.reveal"
		}
		if r.Method == "GET" {
			return "account.keys.read"
		}
		return "account.keys.write"
	case p == "/account/v1/subscription":
		return "account.billing.purchase"
	case p == "/account/v1/redeem":
		return "account.billing.redeem"
	case strings.HasPrefix(p, "/account/v1/wallet") || p == "/account/v1/plans" || p == "/account/v1/subscriptions":
		return "account.billing.read"
	}
	return ""
}
func adminAccountPermission(r *http.Request) string {
	p := r.URL.Path
	switch {
	case p == "/account/v1/admin/card-plans", strings.HasPrefix(p, "/account/v1/admin/cards"):
		return "card.manage"
	case p == "/account/v1/admin/usage":
		return "user.read"
	case p == "/account/v1/admin/logs":
		return "audit.read"
	case p == "/account/v1/admin/credentials":
		return "credential.read"
	case strings.HasPrefix(p, "/account/v1/admin/keys"):
		if strings.HasSuffix(p, "/reveal") {
			return "credential.reveal"
		}
		if r.Method == "GET" {
			return "credential.read"
		}
		return "credential.write"
	case p == "/account/v1/admin/settings":
		return "*"
	default:
		if r.Method == "GET" {
			return "billing.read"
		}
		return "billing.manage"
	}
}
