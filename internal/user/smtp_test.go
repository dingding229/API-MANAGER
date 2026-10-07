package user

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestSMTPTestAndResetDeliveryOverVerifiedTLS(t *testing.T) {
	for _, mode := range []string{"tls", "starttls"} {
		t.Run(mode, func(t *testing.T) {
			certificateServer := httptest.NewTLSServer(http.NotFoundHandler())
			cert := certificateServer.TLS.Certificates[0]
			trusted := certificateServer.Certificate()
			certificateServer.Close()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			captured := make(chan string, 1)
			failures := make(chan error, 1)
			go func() {
				connection, err := listener.Accept()
				if err != nil {
					failures <- err
					return
				}
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(8 * time.Second))
				secure := mode == "tls"
				if secure {
					connection = tls.Server(connection, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
				}
				_, err = io.WriteString(connection, "220 fake SMTP\r\n")
				if err != nil {
					failures <- err
					return
				}
				reader := bufio.NewReader(connection)
				for {
					line, e := reader.ReadString('\n')
					if e != nil {
						failures <- e
						return
					}
					line = strings.TrimSpace(line)
					switch {
					case strings.HasPrefix(line, "EHLO"):
						_, err = io.WriteString(connection, "250-localhost\r\n250-STARTTLS\r\n250 AUTH PLAIN\r\n")
					case line == "STARTTLS":
						_, err = io.WriteString(connection, "220 begin TLS\r\n")
						if err != nil {
							failures <- err
							return
						}
						connection = tls.Server(connection, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
						reader = bufio.NewReader(connection)
						secure = true
					case strings.HasPrefix(line, "AUTH PLAIN "):
						raw, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "AUTH PLAIN "))
						if e != nil || !secure || string(raw) != "\x00sender@example.test\x00test-authorization-code" {
							_, err = io.WriteString(connection, "535 invalid credentials\r\n")
						} else {
							_, err = io.WriteString(connection, "235 accepted\r\n")
						}
					case strings.HasPrefix(line, "MAIL FROM:") || strings.HasPrefix(line, "RCPT TO:"):
						_, err = io.WriteString(connection, "250 accepted\r\n")
					case line == "DATA":
						if !secure {
							failures <- io.ErrUnexpectedEOF
							return
						}
						_, err = io.WriteString(connection, "354 message follows\r\n")
						var message strings.Builder
						for {
							part, e := reader.ReadString('\n')
							if e != nil {
								failures <- e
								return
							}
							if part == ".\r\n" {
								break
							}
							message.WriteString(part)
						}
						captured <- message.String()
						_, err = io.WriteString(connection, "250 queued\r\n")
					case line == "QUIT":
						_, _ = io.WriteString(connection, "221 done\r\n")
						return
					default:
						_, err = io.WriteString(connection, "500 unsupported\r\n")
					}
					if err != nil {
						failures <- err
						return
					}
				}
			}()
			mailer, err := NewSMTPMailer("127.0.0.1", listener.Addr().(*net.TCPAddr).Port, "sender@example.test", "test-authorization-code", "sender@example.test", mode)
			if err != nil {
				t.Fatal(err)
			}
			roots := x509.NewCertPool()
			roots.AddCert(trusted)
			mailer.tlsConfig = &tls.Config{ServerName: "127.0.0.1", RootCAs: roots, MinVersion: tls.VersionTLS12}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err = mailer.SendTest(ctx, "receiver@example.test", "测试网站"); err != nil {
				t.Fatal(err)
			}
			select {
			case raw := <-captured:
				if !strings.Contains(raw, "Subject:") || strings.Contains(raw, "test-authorization-code") || strings.Contains(raw, "#reset=") {
					t.Fatal("test mail includes credentials or lacks subject")
				}
			case err := <-failures:
				t.Fatal(err)
			case <-ctx.Done():
				t.Fatal("mail delivery did not finish")
			}
		})
	}
}
func TestSMTPHungGreetingRespectsCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var b [1]byte
		_, _ = conn.Read(b[:])
	}()
	mailer, _ := NewSMTPMailer("127.0.0.1", listener.Addr().(*net.TCPAddr).Port, "", "", "sender@example.test", "starttls")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := mailer.SendTest(ctx, "receiver@example.test", "Test"); err == nil {
		t.Fatal("hung SMTP accepted")
	}
	if time.Since(start) > time.Second {
		t.Fatal("SMTP request ignored cancellation")
	}
}

func TestSMTPMessageCarriesHTMLAndPlainAlternative(t *testing.T) {
	raw, e := smtpMessage("from@example.test", "to@example.test", "HTML test", "fallback text", "<h1>HTML email</h1>")
	if e != nil {
		t.Fatal(e)
	}
	message, e := mail.ReadMessage(strings.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	kind, p, e := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if e != nil || kind != "multipart/alternative" {
		t.Fatal(kind, e)
	}
	reader := multipart.NewReader(message.Body, p["boundary"])
	var kinds []string
	for {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		data, _ := io.ReadAll(part)
		decoded, e := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if e != nil {
			t.Fatal(e)
		}
		kinds = append(kinds, part.Header.Get("Content-Type"))
		if len(kinds) == 2 && !strings.Contains(string(decoded), "<h1>HTML email</h1>") {
			t.Fatal("HTML missing")
		}
	}
	if len(kinds) != 2 || !strings.HasPrefix(kinds[0], "text/plain") || !strings.HasPrefix(kinds[1], "text/html") {
		t.Fatal(kinds)
	}
}
