package user

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type SMTPMailer struct {
	Host                           string
	Port                           int
	Username, Password, From, Mode string
}

func NewSMTPMailer(host string, port int, username, password, from, mode string) (*SMTPMailer, error) {
	if host == "" || strings.ContainsAny(host, "/\\\r\n ") || port < 1 || port > 65535 || !validEmail(from) || (mode != "starttls" && mode != "tls") || ((username == "") != (password == "")) {
		return nil, errors.New("invalid SMTP configuration; TLS and sender address are required")
	}
	return &SMTPMailer{host, port, username, password, normalizeEmail(from), mode}, nil
}
func (m *SMTPMailer) SendReset(ctx context.Context, to, link string) error {
	if !validEmail(to) || strings.ContainsAny(link, "\r\n") {
		return errors.New("invalid reset message")
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(m.Host, strconv.Itoa(m.Port)))
	if err != nil {
		return errors.New("SMTP connection failed")
	}
	defer conn.Close()
	deadline := time.Now().Add(15 * time.Second)
	if v, ok := ctx.Deadline(); ok {
		deadline = v
	}
	if err = conn.SetDeadline(deadline); err != nil {
		return err
	}
	cfg := &tls.Config{ServerName: m.Host, MinVersion: tls.VersionTLS12}
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
	text := "You requested an API Manager password reset.\r\n\r\nOpen this link within 15 minutes:\r\n" + link + "\r\n\r\nIf you did not request this, ignore this email. Do not share the link.\r\n"
	body := base64.StdEncoding.EncodeToString([]byte(text))
	var lines []string
	for len(body) > 76 {
		lines = append(lines, body[:76])
		body = body[76:]
	}
	lines = append(lines, body)
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: API Manager password reset\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n", m.From, to, strings.Join(lines, "\r\n"))
	if _, err = w.Write([]byte(message)); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
