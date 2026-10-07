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
