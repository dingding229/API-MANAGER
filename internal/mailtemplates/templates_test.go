package mailtemplates

import (
	"api-manager/internal/model"
	"strings"
	"testing"
)

func TestHTMLDefaultsAndContextEscaping(t *testing.T) {
	d := Defaults()
	if e := Validate(d); e != nil {
		t.Fatal(e)
	}
	subject, body, e := Render(d.Verification, map[string]string{"site_name": "网站", "email": "a@example.test", "code": "<script>alert(1)</script>", "purpose": "邮箱验证", "expires_minutes": "10"})
	if e != nil || !strings.Contains(subject, "网站") || strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal(subject, body, e)
	}
	_, body, e = Render(d.Reset, map[string]string{"site_name": "网站", "email": "a@example.test", "reset_url": "https://example.test/login#reset=safe", "expires_minutes": "15"})
	if e != nil || !strings.Contains(body, `href="https://example.test/login#reset=safe"`) {
		t.Fatal(body, e)
	}
}
func TestMailTemplateRejectsExecutableContentAndSecretURLInterpolation(t *testing.T) {
	for _, html := range []string{`<script>alert(1)</script>`, `<img src="https://x.test/p" onerror="alert(1)">`, `<a href="javascript:alert(1)">link</a>`, `<iframe src="https://x.test"></iframe>`, `<a href="https://evil.test/{{code}}">link</a>`, `<p style="background:url(https://evil.test)">x</p>`, `<p>{{unsupported}}</p>`, `<svg onload="alert(1)"></svg>`} {
		v := model.EmailTemplates{Verification: model.EmailTemplate{Subject: "主题", HTML: html}, Reset: Defaults().Reset, Test: Defaults().Test}
		if e := Validate(v); e == nil {
			t.Fatal("unsafe mail accepted", html)
		}
	}
	if _, _, e := Render(Defaults().Verification, map[string]string{"site_name": "name\r\nInjected: yes"}); e == nil {
		t.Fatal("subject injection")
	}
}
