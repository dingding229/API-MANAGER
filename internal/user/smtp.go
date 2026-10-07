package user

import (
	"api-manager/internal/mailtemplates"
	"api-manager/internal/model"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

type SMTPMailer struct {
	SiteName, SiteURL              string
	Templates                      model.EmailTemplates
	tlsConfig                      *tls.Config // Internal test transport only; never exposed as website configuration.
	DialContext                    func(context.Context, string, string) (net.Conn, error)
	Host                           string
	Port                           int
	Username, Password, From, Mode string
}

func NewSMTPMailer(host string, port int, username, password, from, mode string) (*SMTPMailer, error) {
	if host == "" || strings.ContainsAny(host, "/\\\r\n ") || port < 1 || port > 65535 || !validEmail(from) || (mode != "starttls" && mode != "tls") || ((username == "") != (password == "")) {
		return nil, errors.New("invalid SMTP configuration; TLS and sender address are required")
	}
	return &SMTPMailer{Host: host, Port: port, Username: username, Password: password, From: normalizeEmail(from), Mode: mode}, nil
}
func (m *SMTPMailer) SendReset(ctx context.Context, to, link string) error {
	if !validEmail(to) || strings.ContainsAny(link, "\r\n") {
		return errors.New("invalid reset message")
	}
	subject, htmlBody, e := m.renderTemplate(mailtemplates.FillDefaults(m.Templates).Reset, to, "", "密码重置", link, "15")
	if e != nil {
		return e
	}
	return m.send(ctx, to, subject, "You requested an API Manager password reset.\r\n\r\nOpen this link within 15 minutes:\r\n"+link+"\r\n\r\nIf you did not request this, ignore this email. Do not share the link.\r\n", htmlBody)
}
func (m *SMTPMailer) SendTest(ctx context.Context, to, name string) error {
	if !validEmail(to) || strings.ContainsAny(name, "\r\n") {
		return errors.New("invalid test message")
	}
	subject, body, e := m.renderTemplate(mailtemplates.FillDefaults(m.Templates).Test, to, "", "邮件服务测试", "", "10")
	if e != nil {
		return e
	}
	return m.send(ctx, to, subject, "This is a configuration test from "+name+". No reset token, password or API credential is included.\r\n", body)
}
func (m *SMTPMailer) send(ctx context.Context, to, subject, text string, htmlBody ...string) error {
	d := net.Dialer{Timeout: 10 * time.Second}
	dial := m.DialContext
	if dial == nil {
		dial = d.DialContext
	}
	conn, err := dial(ctx, "tcp", net.JoinHostPort(m.Host, strconv.Itoa(m.Port)))
	if err != nil {
		return errors.New("SMTP connection failed")
	}
	defer conn.Close()
	rawConnection := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConnection.Close() })
	defer stop()
	deadline := time.Now().Add(15 * time.Second)
	if v, ok := ctx.Deadline(); ok && v.Before(deadline) {
		deadline = v
	}
	if err = conn.SetDeadline(deadline); err != nil {
		return err
	}
	cfg := &tls.Config{ServerName: m.Host, MinVersion: tls.VersionTLS12}
	if m.tlsConfig != nil {
		cfg = m.tlsConfig.Clone()
	}
	if m.Mode == "tls" {
		secure := tls.Client(conn, cfg)
		if err = secure.HandshakeContext(ctx); err != nil {
			return errors.New("SMTP TLS failed")
		}
		conn = secure
	}
	c, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		return errors.New("SMTP greeting failed")
	}
	defer c.Close()
	if m.Mode == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("SMTP requires STARTTLS")
		}
		if err = c.StartTLS(cfg); err != nil {
			return errors.New("SMTP STARTTLS failed")
		}
	}
	if m.Username != "" {
		if err = c.Auth(smtp.PlainAuth("", m.Username, m.Password, m.Host)); err != nil {
			return errors.New("SMTP authentication failed")
		}
	}
	if err = c.Mail(m.From); err != nil {
		return err
	}
	if err = c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	bodyHTML := ""
	if len(htmlBody) > 0 {
		bodyHTML = htmlBody[0]
	}
	message, e := smtpMessage(m.From, to, subject, text, bodyHTML)
	if e != nil {
		return e
	}
	if _, err = w.Write([]byte(message)); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func (m *SMTPMailer) SendVerification(ctx context.Context, to, code, purpose string) error {
	if !validEmail(to) || strings.ContainsAny(code+purpose, "\r\n") {
		return errors.New("invalid verification message")
	}
	title := map[string]string{"register": "账号注册", "email-login": "邮箱登录", "verify-email": "邮箱验证", "change-email": "修改邮箱"}[purpose]
	if title == "" {
		title = "邮箱验证"
	}
	subject, body, e := m.renderTemplate(mailtemplates.FillDefaults(m.Templates).Verification, to, code, title, "", "10")
	if e != nil {
		return e
	}
	return m.send(ctx, to, subject, "本次"+title+"的验证码为："+code+"。有效期 10 分钟。请勿转发。若非本人操作，请忽略。\r\n", body)
}

func (m *SMTPMailer) renderTemplate(t model.EmailTemplate, to, code, purpose, link, expires string) (string, string, error) {
	name := m.SiteName
	if name == "" {
		name = "API Manager"
	}
	return mailtemplates.Render(t, map[string]string{"site_name": name, "site_url": m.SiteURL, "email": to, "code": code, "purpose": purpose, "reset_url": link, "expires_minutes": expires})
}
func smtpMessage(from, to, subject, text, htmlBody string) (string, error) {
	if strings.ContainsAny(subject, "\r\n\x00") {
		return "", errors.New("invalid mail subject")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writePart := func(kind, value string) error {
		headers := textproto.MIMEHeader{}
		headers.Set("Content-Type", kind+"; charset=UTF-8")
		headers.Set("Content-Transfer-Encoding", "base64")
		part, e := writer.CreatePart(headers)
		if e != nil {
			return e
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(value))
		for len(encoded) > 76 {
			if _, e = io.WriteString(part, encoded[:76]+"\r\n"); e != nil {
				return e
			}
			encoded = encoded[76:]
		}
		_, e = io.WriteString(part, encoded+"\r\n")
		return e
	}
	if e := writePart("text/plain", text); e != nil {
		return "", e
	}
	if htmlBody == "" {
		htmlBody = "<p>" + html.EscapeString(text) + "</p>"
	}
	if e := writePart("text/html", htmlBody); e != nil {
		return "", e
	}
	if e := writer.Close(); e != nil {
		return "", e
	}
	return fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n%s", from, to, mime.QEncoding.Encode("UTF-8", subject), writer.Boundary(), body.String()), nil
}
