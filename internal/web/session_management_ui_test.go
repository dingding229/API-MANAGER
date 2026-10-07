package web

import (
	"strings"
	"testing"
)

func TestPersonalSessionsAndKeysHaveOnlyUserCenterEntry(t *testing.T) {
	html, _ := assets.ReadFile("assets/index.html")
	js, _ := assets.ReadFile("assets/app.js")
	if strings.Contains(string(html), `data-page="sessions"`) || strings.Contains(string(js), "renderSessions") || strings.Contains(string(js), "/sessions") {
		t.Fatal("duplicated personal session page retained")
	}
	if !strings.Contains(string(js), `location.assign('/account'`) {
		t.Fatal("user-center entry missing")
	}
	if strings.Contains(string(html), `id="login-form"`) || !strings.Contains(string(html), `id="auth-loading"`) || !strings.Contains(string(js), `$('#auth-loading')?.classList.add('hidden')`) {
		t.Fatal("console must not render a separate login form")
	}
}
