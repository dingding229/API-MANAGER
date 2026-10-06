package web

import (
	"strings"
	"testing"
)

func TestSessionPageIncludesScopedMetadataAndConfirmation(t *testing.T) {
	js, _ := assets.ReadFile("assets/app.js")
	source := string(js)
	for _, term := range []string{"renderSessions", "user.sessions.manage", "login_ip", "user_agent", "last_seen_at", "expires_at", "data-revoke-session", "我确认退出此会话", "body:JSON.stringify({confirm:true})", "public_test_enabled"} {
		if !strings.Contains(source, term) {
			t.Fatal("missing session control", term)
		}
	}
	if strings.Contains(source, "state.token") {
		t.Fatal("session token stored in UI state")
	}
}
