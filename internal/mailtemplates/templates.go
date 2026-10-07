package mailtemplates

import (
	"api-manager/internal/model"
	"errors"
	htmlparser "golang.org/x/net/html"
	"html"
	"net/url"
	"regexp"
	"strings"
)

var variables = regexp.MustCompile(`\{\{\s*([a-z_]+)\s*\}\}`)
var allowedVariables = map[string]bool{"site_name": true, "email": true, "code": true, "purpose": true, "expires_minutes": true, "reset_url": true, "site_url": true}
var allowedTags = map[string]bool{"html": true, "head": true, "body": true, "title": true, "meta": true, "div": true, "p": true, "span": true, "table": true, "thead": true, "tbody": true, "tr": true, "td": true, "th": true, "h1": true, "h2": true, "h3": true, "a": true, "img": true, "br": true, "hr": true, "strong": true, "b": true, "em": true, "i": true, "ul": true, "ol": true, "li": true, "blockquote": true}

func Defaults() model.EmailTemplates {
	wrapper := `<div style="background:#f5f6fb;padding:32px;font-family:Arial,sans-serif;color:#1e293b"><div style="max-width:560px;margin:auto;background:#fff;border:1px solid #e2e8f0;border-radius:14px;padding:32px"><p style="color:#4f46e5;font-weight:bold">{{site_name}}</p>`
	end := `<p style="font-size:12px;color:#64748b;border-top:1px solid #e2e8f0;padding-top:20px">如非本人操作，请忽略此邮件。请勿转发验证码或操作链接。</p></div></div>`
	return model.EmailTemplates{Verification: model.EmailTemplate{Subject: "{{site_name}} · {{purpose}}验证码", HTML: wrapper + `<h1 style="font-size:24px">确认你的邮箱</h1><p>你好，{{email}}：</p><p>你正在进行{{purpose}}，请填写以下验证码：</p><p style="font-size:32px;font-weight:bold;letter-spacing:8px;color:#4f46e5;background:#eef2ff;padding:18px;text-align:center;border-radius:8px">{{code}}</p><p>验证码在 {{expires_minutes}} 分钟内有效。</p>` + end}, Reset: model.EmailTemplate{Subject: "{{site_name}} · 重置密码", HTML: wrapper + `<h1 style="font-size:24px">重置登录密码</h1><p>你好，{{email}}：</p><p>请点击下方按钮设置新密码。链接在 {{expires_minutes}} 分钟内有效。</p><p><a href="{{reset_url}}" style="display:inline-block;background:#4f46e5;color:#fff;text-decoration:none;padding:12px 20px;border-radius:8px">设置新密码</a></p><p>若按钮无法打开，请复制链接：<br><a href="{{reset_url}}">{{reset_url}}</a></p>` + end}, Test: model.EmailTemplate{Subject: "{{site_name}} · 邮件服务测试", HTML: wrapper + `<h1 style="font-size:24px">邮件服务已连接</h1><p>你好，{{email}}：</p><p>这封 HTML 邮件用于确认 {{site_name}} 的发信设置可用。</p>` + end}}
}
func FillDefaults(v model.EmailTemplates) model.EmailTemplates {
	d := Defaults()
	if v.Verification.HTML == "" {
		v.Verification = d.Verification
	}
	if v.Reset.HTML == "" {
		v.Reset = d.Reset
	}
	if v.Test.HTML == "" {
		v.Test = d.Test
	}
	return v
}
func Validate(v model.EmailTemplates) error { return validate(v, true) }
func validate(v model.EmailTemplates, required bool) error {
	if required {
		has := func(source, name string) bool {
			for _, m := range variables.FindAllStringSubmatch(source, -1) {
				if m[1] == name {
					return true
				}
			}
			return false
		}
		if !has(v.Verification.HTML, "code") {
			return errors.New("验证码模板须保留 {{code}} 变量")
		}
		if !has(v.Reset.HTML, "reset_url") {
			return errors.New("密码重置模板须保留 {{reset_url}} 变量")
		}
	}

	for _, t := range []model.EmailTemplate{v.Verification, v.Reset, v.Test} {
		if len(t.Subject) < 1 || len(t.Subject) > 200 || strings.ContainsAny(t.Subject, "\r\n\x00") || len(t.HTML) < 1 || len(t.HTML) > 32<<10 {
			return errors.New("邮件主题或 HTML 模板长度不正确")
		}
		for _, s := range []string{t.Subject, t.HTML} {
			for _, m := range variables.FindAllStringSubmatch(s, -1) {
				if !allowedVariables[m[1]] {
					return errors.New("邮件模板包含不支持的变量")
				}
			}
			remaining := variables.ReplaceAllString(s, "")
			if strings.Contains(remaining, "{{") || strings.Contains(remaining, "}}") {
				return errors.New("请使用已列出的邮件模板变量")
			}
		}
		if e := validateHTML(t.HTML); e != nil {
			return e
		}
	}
	return nil
}
func validateHTML(source string) error {
	document, e := htmlparser.Parse(strings.NewReader(source))
	if e != nil {
		return errors.New("HTML 模板不可解析")
	}
	var walk func(*htmlparser.Node) error
	walk = func(n *htmlparser.Node) error {
		if n.Type == htmlparser.ElementNode {
			if n.Namespace != "" || !allowedTags[n.Data] {
				return errors.New("邮件模板不允许脚本、表单、嵌入页面或未知标签")
			}
			for _, a := range n.Attr {
				k := strings.ToLower(a.Key)
				v := strings.ToLower(strings.TrimSpace(a.Val))
				if strings.HasPrefix(k, "on") || k == "srcdoc" || k == "srcset" || k == "http-equiv" {
					return errors.New("邮件模板不允许可执行属性")
				}
				if k == "style" && (strings.Contains(v, "\\") || strings.Contains(v, "{{") || strings.Contains(v, "url(") || strings.Contains(v, "expression") || strings.Contains(v, "@import") || strings.Contains(v, "javascript") || strings.Contains(v, "behavior") || strings.Contains(v, "binding")) {
					return errors.New("邮件样式不允许外部加载或表达式")
				}
				if k == "href" || k == "src" {
					if a.Val == "{{reset_url}}" || a.Val == "{{site_url}}" {
						continue
					}
					if strings.Contains(a.Val, "{{") {
						return errors.New("链接仅允许 reset_url 和 site_url 变量")
					}
					u, err := url.Parse(a.Val)
					if err != nil || (u.Scheme == "https" && u.Host == "") || u.User != nil || strings.ContainsAny(a.Val, "\r\n\x00") || (u.Scheme != "https" && !(k == "href" && u.Scheme == "mailto")) {
						return errors.New("邮件链接和图片须使用安全的 HTTPS 地址")
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if e := walk(c); e != nil {
				return e
			}
		}
		return nil
	}
	return walk(document)
}
func Render(t model.EmailTemplate, values map[string]string) (string, string, error) {
	v := model.EmailTemplates{Verification: t, Reset: t, Test: t}
	if e := validate(v, false); e != nil {
		return "", "", e
	}
	replace := func(source string, escape bool) string {
		return variables.ReplaceAllStringFunc(source, func(marker string) string {
			k := variables.FindStringSubmatch(marker)[1]
			value := values[k]
			if escape {
				return html.EscapeString(value)
			}
			return value
		})
	}
	subject := replace(t.Subject, false)
	if len(subject) > 300 || strings.ContainsAny(subject, "\r\n\x00") {
		return "", "", errors.New("邮件主题不可包含换行")
	}
	body := replace(t.HTML, true)
	if len(body) > 64<<10 {
		return "", "", errors.New("替换变量后的邮件过长")
	}
	if e := validateHTML(body); e != nil {
		return "", "", e
	}
	return subject, body, nil
}
