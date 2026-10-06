package user

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type SMTPMailer struct {
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
	return m.send(ctx, to, "API Manager password reset", "You requested an API Manager password reset.\r\n\r\nOpen this link within 15 minutes:\r\n"+link+"\r\n\r\nIf you did not request this, ignore this email. Do not share the link.\r\n")
}
func (m *SMTPMailer) SendTest(ctx context.Context, to, name string) error {
	if !validEmail(to) || strings.ContainsAny(name, "\r\n") {
		return errors.New("invalid test message")
	}
	return m.send(ctx, to, name+" SMTP test", "This is a configuration test from "+name+". No reset token, password or API credential is included.\r\n")
}
func (m *SMTPMailer) send(ctx context.Context, to, subject, text string) error {
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
	body := base64.StdEncoding.EncodeToString([]byte(text))
	var lines []string
	for len(body) > 76 {
		lines = append(lines, body[:76])
		body = body[76:]
	}
	lines = append(lines, body)
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n", m.From, to, mime.QEncoding.Encode("UTF-8", subject), strings.Join(lines, "\r\n"))
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
	return m.send(ctx, to, "API Manager 验证码", "本次"+purpose+"的验证码为："+code+"。有效期 10 分钟。请勿转发。若非本人操作，请忽略。\r\n")
}
