package user

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/store"
)

type capturedMail struct{ address, link string }
type captureMailer struct{ ch chan capturedMail }

func (m *captureMailer) SendReset(_ context.Context, to, link string) error {
	m.ch <- capturedMail{to, link}
	return nil
}
func TestEmailIsSeparateAndRecoveryIsSingleUse(t *testing.T) {
	m := store.NewMemory()
	s := NewService(m)
	u, err := s.CreateWithContact("reader", "reader@example.test", "ReadMe88", []string{"member"})
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := s.Authenticate("reader", "ReadMe88")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Authenticate("reader@example.test", "ReadMe88"); err == nil {
		t.Fatal("contact email was accepted as username")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mailer := &captureMailer{make(chan capturedMail, 2)}
	if err = s.ConfigureRecovery(ctx, mailer, "https://console.example.test/admin/"); err != nil {
		t.Fatal(err)
	}
	h := NewHTTP(s)
	req := httptest.NewRequest("POST", "/auth/v1/forgot-password", strings.NewReader(`{"email":"reader@example.test"}`))
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 202 {
		t.Fatalf("forgot=%d", w.Code)
	}
	var mail capturedMail
	select {
	case mail = <-mailer.ch:
	case <-time.After(3 * time.Second):
		t.Fatal("email not delivered")
	}
	parsed, err := url.Parse(mail.link)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(parsed.Fragment, "reset=")
	if mail.address != u.Email || len(token) != 64 || parsed.RawQuery != "" {
		t.Fatal("invalid reset email")
	}
	if strings.Contains(w.Body.String(), token) {
		t.Fatal("token leaked in response")
	}
	persisted, err := m.GetUserByID(u.ID)
	if err != nil || persisted.PasswordHash != u.PasswordHash {
		t.Fatal("forgot request modified password")
	}
	reset := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/auth/v1/reset-password", strings.NewReader(`{"token":"`+token+`","password":"NewPwd88"}`))
		r.RemoteAddr = "127.0.0.2:1"
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		return out
	}
	if out := reset(); out.Code != 200 {
		t.Fatalf("reset=%d %s", out.Code, out.Body.String())
	}
	if _, err = s.ValidateSession(session); err == nil {
		t.Fatal("old session survived")
	}
	if _, _, err = s.Authenticate("reader", "ReadMe88"); err == nil {
		t.Fatal("old password survived")
	}
	if _, _, err = s.Authenticate("reader", "NewPwd88"); err != nil {
		t.Fatal(err)
	}
	if out := reset(); out.Code != 400 {
		t.Fatal("reset token reused")
	}
}
func TestForgotPasswordDoesNotRevealAccountOrToken(t *testing.T) {
	s := NewService(store.NewMemory())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.ConfigureRecovery(ctx, &captureMailer{make(chan capturedMail, 2)}, "https://console.example.test/admin/"); err != nil {
		t.Fatal(err)
	}
	h := NewHTTP(s)
	for _, email := range []string{"unknown@example.test", "other@example.test"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/auth/v1/forgot-password", strings.NewReader(`{"email":"`+email+`"}`))
		h.ServeHTTP(w, r)
		if w.Code != 202 {
			t.Fatal(w.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 1 || body["message"] == nil {
			t.Fatal("account/token leaked")
		}
	}
	for i := 0; i < 4; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/auth/v1/forgot-password", strings.NewReader(`{"email":"unknown@example.test"}`)))
		if i == 3 && w.Code != 429 {
			t.Fatal("request rate bound missing")
		}
	}
}
func TestResetStoreRejectsExpiredAndChangedContact(t *testing.T) {
	m := store.NewMemory()
	s := NewService(m)
	u, err := s.CreateWithContact("reader", "reader@example.test", "ReadMe88", []string{"member"})
	if err != nil {
		t.Fatal(err)
	}
	r := model.PasswordReset{Hash: auth.HashAPIKey("token"), UserID: u.ID, Username: u.Username, Email: u.Email, PasswordHash: u.PasswordHash, CreatedAt: time.Now().Add(-2 * time.Minute), ExpiresAt: time.Now().Add(-time.Second)}
	if _, err = m.IssuePasswordReset(r); err != nil {
		t.Fatal(err)
	}
	if _, err = m.CompletePasswordReset(r.Hash, "hash"); err == nil {
		t.Fatal("expired token accepted")
	}
	r.ExpiresAt = time.Now().Add(time.Minute)
	if _, err = m.IssuePasswordReset(r); err != nil {
		t.Fatal(err)
	}
	email := "new@example.test"
	if _, _, err = s.UpdateProfile(u.ID, u.ID, model.UpdateUserProfileRequest{Email: &email, CurrentPassword: "ReadMe88"}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.CompletePasswordReset(r.Hash, "hash"); err == nil {
		t.Fatal("contact change did not revoke reset")
	}
}
func TestRecoveryConfigurationRequiresTrustedHTTPSAndTLS(t *testing.T) {
	for _, base := range []string{"http://example.test/admin/", "https://user:pass@example.test/admin/", "https://example.test/admin/?token=x"} {
		if err := NewService(store.NewMemory()).ConfigureRecovery(context.Background(), &captureMailer{make(chan capturedMail, 1)}, base); err == nil {
			t.Fatalf("unsafe URL accepted: %s", base)
		}
	}
	if _, err := NewSMTPMailer("smtp.example.test", 587, "user", "pass", "from@example.test", "plaintext"); err == nil {
		t.Fatal("plaintext SMTP accepted")
	}
}

func TestRecoveryCanBeReconfiguredWhileRequestsRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewService(store.NewMemory())
	mailer := &captureMailer{ch: make(chan capturedMail, 2)}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 30; n++ {
				if err := s.ConfigureRecovery(ctx, mailer, "https://example.test/admin/"); err != nil {
					t.Error(err)
				}
				s.DisableRecovery()
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := NewHTTP(s)
			for n := 0; n < 30; n++ {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", "/auth/v1/recovery", nil))
			}
		}()
	}
	wg.Wait()
	s.DisableRecovery()
}
