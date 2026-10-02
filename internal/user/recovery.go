package user

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"golang.org/x/crypto/bcrypt"
)

type ResetMailer interface {
	SendReset(context.Context, string, string) error
}
type recoveryStore interface {
	IssuePasswordReset(model.PasswordReset) (bool, error)
	DeletePasswordReset(string) error
	CompletePasswordReset(string, string) (string, error)
}
type recovery struct {
	ctx     context.Context
	cancel  context.CancelFunc
	mailer  ResetMailer
	baseURL string
	queue   chan string
	mu      sync.Mutex
	ips     map[string]recoveryRate
}
type recoveryRate struct {
	time  time.Time
	count int
}

func (s *Service) ConfigureRecovery(ctx context.Context, mailer ResetMailer, baseURL string) error {
	if mailer == nil {
		return errors.New("reset mailer is required")
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !(u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost"))) {
		return errors.New("PASSWORD_RESET_BASE_URL must be a trusted HTTPS console URL")
	}
	if _, ok := s.store.(recoveryStore); !ok {
		return errors.New("password recovery storage unavailable")
	}
	workerCtx, cancel := context.WithCancel(ctx)
	r := &recovery{ctx: workerCtx, cancel: cancel, mailer: mailer, baseURL: u.String(), queue: make(chan string, 32), ips: make(map[string]recoveryRate)}
	s.recoveryMu.Lock()
	old := s.recovery
	s.recovery = r
	s.recoveryMu.Unlock()
	if old != nil {
		old.cancel()
	}
	go func() {
		for {
			select {
			case <-workerCtx.Done():
				return
			case email := <-r.queue:
				s.deliverReset(workerCtx, r, email)
			}
		}
	}()
	return nil
}
func (s *Service) deliverReset(ctx context.Context, r *recovery, email string) {
	if ctx.Err() != nil {
		return
	}
	u, err := s.store.GetUserByEmail(email)
	if err != nil || u.Status != "active" || u.Email == "" {
		return
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return
	}
	token := hex.EncodeToString(raw)
	now := time.Now().UTC()
	st := s.store.(recoveryStore)
	issued, err := st.IssuePasswordReset(model.PasswordReset{Hash: auth.HashAPIKey(token), UserID: u.ID, Username: u.Username, Email: u.Email, PasswordHash: u.PasswordHash, CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)})
	if err != nil || !issued {
		return
	}
	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// Fragment is not sent in HTTP URLs, referers or access logs.
	link := r.baseURL + "#reset=" + token
	if err = r.mailer.SendReset(sendCtx, u.Email, link); err != nil {
		_ = st.DeletePasswordReset(auth.HashAPIKey(token))
	}
}
func (r *recovery) allow(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, v := range r.ips {
		if now.Sub(v.time) > time.Minute {
			delete(r.ips, k)
		}
	}
	if _, exists := r.ips[host]; !exists && len(r.ips) >= 4096 {
		return false
	}
	v := r.ips[host]
	if v.count >= 5 {
		return false
	}
	if v.count == 0 {
		v.time = now
	}
	v.count++
	r.ips[host] = v
	return true
}
func (h *HTTP) recoveryStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{"enabled": h.service.currentRecovery() != nil})
}
func decodeRecovery(w http.ResponseWriter, r *http.Request, target any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil || d.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON body"})
		return false
	}
	return true
}
func (h *HTTP) forgotPassword(w http.ResponseWriter, r *http.Request) {
	rec := h.service.currentRecovery()
	if rec == nil {
		writeJSON(w, 503, map[string]string{"error": "email recovery is not configured", "code": "recovery_unavailable"})
		return
	}
	if !rec.allow(r.RemoteAddr) {
		writeJSON(w, 429, map[string]string{"error": "too many recovery requests"})
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if !decodeRecovery(w, r, &req) {
		return
	}
	email := normalizeEmail(req.Email)
	if !validEmail(email) {
		writeJSON(w, 400, map[string]string{"error": "valid email required"})
		return
	}
	select {
	case rec.queue <- email:
	default:
	}
	writeJSON(w, 202, map[string]string{"message": "If an active account matches this email, a reset link will be sent. It expires in 15 minutes."})
}
func (h *HTTP) resetPassword(w http.ResponseWriter, r *http.Request) {
	rec := h.service.currentRecovery()
	if rec == nil {
		writeJSON(w, 503, map[string]string{"error": "email recovery is not configured"})
		return
	}
	if !rec.allow(r.RemoteAddr) {
		writeJSON(w, 429, map[string]string{"error": "too many recovery requests"})
		return
	}
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decodeRecovery(w, r, &req) {
		return
	}
	raw, err := hex.DecodeString(req.Token)
	if err != nil || len(raw) != 32 || len(req.Token) != 64 || !validPassword(req.Password) {
		writeJSON(w, 400, map[string]string{"error": "invalid reset token or password", "code": "invalid_reset"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), passwordHashCost)
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "reset unavailable"})
		return
	}
	id, err := h.service.store.(recoveryStore).CompletePasswordReset(auth.HashAPIKey(req.Token), string(hash))
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "reset link is invalid or expired", "code": "invalid_reset"})
		return
	}
	h.service.RecordAudit(audit.Actor{Type: "anonymous"}, r, "auth.password.reset", "user", id, http.StatusOK, nil)
	writeJSON(w, 200, map[string]string{"message": "Password changed. Sign in again."})
}

func (s *Service) currentRecovery() *recovery {
	s.recoveryMu.RLock()
	defer s.recoveryMu.RUnlock()
	return s.recovery
}
func (s *Service) DisableRecovery() {
	s.recoveryMu.Lock()
	old := s.recovery
	s.recovery = nil
	s.recoveryMu.Unlock()
	if old != nil {
		old.cancel()
	}
}
func ValidRecoveryURL(baseURL string) bool {
	u, err := url.Parse(baseURL)
	return err == nil && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")))
}
