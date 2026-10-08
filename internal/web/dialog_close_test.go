package web

import (
	"strings"
	"testing"
)

func TestDialogHeaderUsesCloseIconAndKeepsFooterReturnNavigation(t *testing.T) {
	raw, e := assets.ReadFile("assets/app.js")
	if e != nil {
		t.Fatal(e)
	}
	source := string(raw)
	for _, name := range []string{"关闭角色编辑", "关闭基本信息编辑"} {
		start := strings.Index(source, `class="modal-close" aria-label="`+name+`"`)
		if start < 0 {
			t.Fatal("missing accessible close control", name)
		}
		end := strings.Index(source[start:], "</button>")
		if end < 0 {
			t.Fatal("close control malformed")
		}
		button := source[start : start+end]
		if !strings.Contains(button, "<svg") || !strings.Contains(button, `aria-hidden="true"`) || strings.Contains(button, "返回") {
			t.Fatal("header close control contains return text", name)
		}
	}
	for _, marker := range []string{"data-role-modal-cancel>${onReturn?'返回':'取消'}", "data-profile-modal-cancel>${onReturn?'返回':'取消'}", "if (!force) void modal._onReturn?.();"} {
		if !strings.Contains(source, marker) {
			t.Fatal("footer/escape return behavior changed", marker)
		}
	}
	extra, e := assets.ReadFile("assets/enhancements.js")
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(extra), "${onReturn?'返回':'关闭'}") || !strings.Contains(string(extra), `data-dialog-close aria-label="关闭"><svg`) {
		t.Fatal("shared dialog header still uses duplicate navigation text")
	}
	css, _ := assets.ReadFile("assets/controls.css")
	if !strings.Contains(string(css), "flex:0 0 44px") || !strings.Contains(string(css), ".modal-heading .modal-close svg") {
		t.Fatal("close icon touch target or alignment missing")
	}
}
