package web

import (
	"strings"
	"testing"
)

func TestDatabaseBrowserIsReadOnlyAndFormatsUntrustedRecordsAsText(t *testing.T) {
	data, e := assets.ReadFile("assets/database.js")
	if e != nil {
		t.Fatal(e)
	}
	s := string(data)
	for _, v := range []string{"database.manage", "super_admin", "textContent=JSON.stringify", "恢复数据库", "current_password", "createObjectURL", "FormData", "24*1024*1024", "AbortSignal.timeout(90000)", "Asia/Shanghai", "openDatabaseReveal", "60000"} {
		if !strings.Contains(s, v) {
			t.Fatal("missing database UI safeguard", v)
		}
	}
	if strings.Contains(s, "eval(") || strings.Contains(s, "innerHTML=JSON.stringify") {
		t.Fatal("database content may execute")
	}
	html, _ := assets.ReadFile("assets/index.html")
	if !strings.Contains(string(html), `data-page="database" data-permission="database.manage" data-super-admin`) {
		t.Fatal("database entry not scoped")
	}
}

func TestGlobalConfirmationControlsAndStructuredCallDetails(t *testing.T) {
	fields, _ := assets.ReadFile("assets/account.js")
	if strings.Contains(string(fields), "data-passkey-choice") || strings.Contains(string(fields), "select name=\"confirmation_method\"") {
		t.Fatal("per-operation confirmation selector still present")
	}
	logs, _ := assets.ReadFile("assets/consolidation.js")
	for _, v := range []string{"data-call-detail", "openCallLogDetail", "log-detail-section", "data-copy-value"} {
		if !strings.Contains(string(logs), v) {
			t.Fatal("structured call modal missing", v)
		}
	}
	if strings.Contains(string(logs), "<details><summary>查看详情</summary>") {
		t.Fatal("inline call details still stretch rows")
	}
	plugins, _ := assets.ReadFile("assets/app.js")
	for _, v := range []string{"plugin-action-primary", "plugin-action-secondary", "plugin-library-status", "data-plugin-update"} {
		if !strings.Contains(string(plugins), v) {
			t.Fatal("plugin action layout missing", v)
		}
	}
}
