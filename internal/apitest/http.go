// Package apitest runs tightly-scoped authenticated tests, not an arbitrary URL proxy.
package apitest

import (
	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"api-manager/internal/httpx"
	"api-manager/internal/model"
	"api-manager/internal/ratelimit"
	"api-manager/internal/store"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Users interface {
	ValidateSession(string) (model.User, error)
	CurrentSession(string) (model.Session, error)
	Can(string, string) bool
	RecordAudit(audit.Actor, *http.Request, string, string, string, int, map[string]any)
}
type Executor interface {
	ServeTest(http.ResponseWriter, *http.Request, string, string, time.Time)
}
type Handler struct {
	store    store.Store
	users    Users
	executor Executor
	limiter  ratelimit.Limiter
	site     func() model.PublicSiteInfo
}

func New(s store.Store, u Users, e Executor, l ratelimit.Limiter, site func() model.PublicSiteInfo) *Handler {
	return &Handler{s, u, e, l, site}
}

type Request struct {
	APIID   string            `json:"api_id"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}
type input struct {
	Request Request `json:"request"`
	Ticket  string  `json:"ticket,omitempty"`
}

var headerName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost || !auth.CookieMutationAllowed(r) {
		reply(w, 403, "请从本站的测试界面提交请求。")
		return
	}
	token, cookie := auth.SessionToken(r)
	if !cookie {
		reply(w, 401, "请先登录。")
		return
	}
	u, err := h.users.ValidateSession(token)
	if err != nil {
		reply(w, 401, "登录已过期。")
		return
	}
	if !h.users.Can(u.ID, "api.test") {
		reply(w, 403, "当前账号没有在线测试权限。")
		return
	}
	session, err := h.users.CurrentSession(token)
	if err != nil {
		reply(w, 401, "登录已过期。")
		return
	}
	client := httpx.Client(r)
	agent := r.UserAgent()
	if session.LoginIP == "" || session.LoginIP != client.IP || session.UserAgent != agent || len(agent) > 512 {
		h.users.RecordAudit(audit.Actor{ID: u.ID, Type: "user", Email: u.Username}, r, "api.test.denied", "api_test", "", 403, map[string]any{"reason": "source_changed"})
		reply(w, 403, "登录设备或来源已变化，请重新登录后测试。")
		return
	}
	var body input
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		reply(w, 400, "测试参数格式不正确。")
		return
	}

	a, target, ok := h.resolve(body.Request)
	if !ok || !testOwnerAllowed(u, a) {
		reply(w, 403, "此接口未开放在线测试，或调用方式已经变化。")
		return
	}
	headers := make(http.Header)
	if len(body.Request.Headers) > 40 {
		reply(w, 400, "请求头过多。")
		return
	}
	seenHeaders := map[string]bool{}
	for name, value := range body.Request.Headers {
		lower := strings.ToLower(name)
		if seenHeaders[lower] {
			reply(w, 400, "请求头不能重复。")
			return
		}
		seenHeaders[lower] = true
		if !headerName.MatchString(name) || strings.ContainsAny(value, "\r\n\x00") || len(value) > 1024 || strings.HasPrefix(lower, "sec-") || strings.HasPrefix(lower, "proxy-") || strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "cf-") {
			reply(w, 400, "请求头不允许用于在线测试。")
			return
		}
		switch lower {
		case "authorization", "x-api-key", "cookie", "host", "content-length", "connection", "transfer-encoding", "origin", "forwarded", "x-api-request", "x-admin-token", "x-csrf-token":
			reply(w, 400, "请求头不允许用于在线测试。")
			return
		}
		if lower != "content-type" {
			declared := false
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			_ = json.Unmarshal(a.ParametersSchema, &schema)
			var fields struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			_ = json.Unmarshal(schema.Properties["header"], &fields)
			for field := range fields.Properties {
				if strings.EqualFold(name, field) {
					declared = true
				}
			}
			if !declared {
				reply(w, 400, "只能填写接口声明的请求头。")
				return
			}
		}
		headers.Set(name, value)
	}
	raw, _ := json.Marshal(struct {
		Request   Request
		UpdatedAt time.Time
	}{body.Request, a.UpdatedAt})
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	tickets, ok := h.store.(store.TestTicketStore)
	if !ok {
		reply(w, 503, "测试授权服务不可用。")
		return
	}
	if r.URL.Path == "/test/v1/prepare" {
		if !h.limiter.Allow("test-prepare:"+u.ID, 20, time.Minute, time.Now()) {
			reply(w, 429, "测试授权过于频繁，请稍后再试。")
			return
		}
		random := make([]byte, 32)
		if _, err = rand.Read(random); err != nil {
			reply(w, 503, "测试授权服务不可用。")
			return
		}
		value := "tt_" + hex.EncodeToString(random)
		err = tickets.PutTestTicket(r.Context(), model.APITestTicket{Hash: auth.HashAPIKey(value), SessionHash: auth.HashAPIKey(token), APIID: a.ID, Digest: digest, ClientIP: client.IP, UserAgent: agent, ExpiresAt: time.Now().Add(30 * time.Second)})
		if err != nil {
			reply(w, 503, "已有测试等待执行，或授权服务暂不可用。")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ticket": value, "expires_in": 30})
		return
	}
	if r.URL.Path != "/test/v1/invoke" {
		reply(w, 404, "not found")
		return
	}
	if len(body.Ticket) != 67 || !strings.HasPrefix(body.Ticket, "tt_") {
		reply(w, 403, "测试凭证不正确。")
		return
	}
	_, err = tickets.ConsumeTestTicket(r.Context(), auth.HashAPIKey(body.Ticket), auth.HashAPIKey(token), digest, client.IP, agent, time.Now())
	if err != nil {
		reply(w, 403, "测试凭证已过期、已使用或与请求不匹配。")
		return
	}
	if !h.limiter.Allow("test-run:"+u.ID, 10, time.Minute, time.Now()) || !h.limiter.Allow("test-day:"+u.ID, 200, 24*time.Hour, time.Now()) {
		reply(w, 429, "测试额度已用完，请稍后再试。")
		return
	}
	if _, err := h.users.ValidateSession(token); err != nil {
		reply(w, 401, "登录会话已退出。")
		return
	}
	if !h.users.Can(u.ID, "api.test") || !testOwnerAllowed(u, a) {
		reply(w, 403, "账号权限已变化。")
		return
	}
	actor := audit.Actor{ID: u.ID, Type: "user", Email: u.Username}
	if err := audit.New(h.store, nil).RecordChecked(r.Context(), actor, r, "api.test.requested", "api", a.ID, 202, map[string]any{"method": body.Request.Method}); err != nil {
		reply(w, 503, "审计记录不可用，测试未执行。")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, body.Request.Method, target.String(), bytes.NewBufferString(body.Request.Body))
	req.Header = headers
	req.RemoteAddr = r.RemoteAddr
	req.Host = r.Host
	if h.site != nil {
		info := h.site()
		origin := info.APIDomain
		if origin == "" {
			origin = info.WebsiteURL
		}
		if base, e := url.Parse(origin); e == nil && base.Host != "" {
			req.Host = base.Host
		}
	}
	capture := &bounded{headers: make(http.Header)}
	h.executor.ServeTest(capture, req, a.ID, u.ID, a.UpdatedAt)
	if capture.status == 0 {
		capture.status = http.StatusOK
	}
	if body.Request.Method == http.MethodHead {
		capture.body.Reset()
		capture.truncated = false
	}
	h.users.RecordAudit(audit.Actor{ID: u.ID, Type: "user", Email: u.Username}, r, "api.test", "api", a.ID, capture.status, map[string]any{"method": body.Request.Method, "response_bytes": capture.body.Len(), "truncated": capture.truncated})
	_ = json.NewEncoder(w).Encode(map[string]any{"status": capture.status, "content_type": capture.headers.Get("Content-Type"), "body": capture.body.String(), "truncated": capture.truncated, "cache": capture.headers.Get("X-Plugin-Cache"), "duration_ms": time.Since(started).Milliseconds()})
}
func (h *Handler) resolve(request Request) (model.API, *url.URL, bool) {
	target, err := url.ParseRequestURI(request.Path)
	if err != nil || target.IsAbs() || target.Host != "" || !strings.HasPrefix(target.Path, "/api/") || target.Fragment != "" || strings.ContainsAny(request.Path, "\\\r\n\x00") || strings.ContainsAny(target.Path, "\\\r\n\x00") || len(request.Path) > 4096 || len(request.Body) > 512<<10 || ((request.Method == "GET" || request.Method == "HEAD") && request.Body != "") {
		return model.API{}, nil, false
	}
	var list []model.API
	var errList error
	if checked, ok := h.store.(interface{ ListAPIsChecked() ([]model.API, error) }); ok {
		list, errList = checked.ListAPIsChecked()
	} else {
		list = h.store.ListAPIs()
	}
	if errList != nil {
		return model.API{}, nil, false
	}
	for _, a := range list {
		hash := sha256.Sum256([]byte(a.Path))
		if request.APIID != hex.EncodeToString(hash[:8]) || !a.Enabled || a.PublishedAt == nil || !a.PublicVisible || (a.AuthMode != "api_key" && a.AuthMode != "none") {
			continue
		}
		allowed := false
		for _, method := range a.HTTPMethods() {
			if method == request.Method {
				allowed = true
			}
		}
		if !allowed {
			continue
		}
		want, got := strings.Split(a.Path, "/"), strings.Split(target.Path, "/")
		if len(want) != len(got) {
			continue
		}
		match := true
		for i, part := range want {
			if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
				if got[i] == "" || got[i] == "." || got[i] == ".." {
					match = false
				}
			} else if part != got[i] {
				match = false
			}
		}
		if match {
			return a, target, true
		}
	}
	return model.API{}, nil, false
}
func reply(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

type bounded struct {
	headers   http.Header
	body      bytes.Buffer
	status    int
	truncated bool
}

func (b *bounded) Header() http.Header { return b.headers }
func (b *bounded) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}
func (b *bounded) Write(data []byte) (int, error) {
	if b.status == 0 {
		b.status = 200
	}
	remaining := (128 << 10) - b.body.Len()
	if remaining < len(data) {
		b.truncated = true
	}
	if remaining > 0 {
		_, _ = b.body.Write(data[:min(remaining, len(data))])
	}
	return len(data), nil
}

func testOwnerAllowed(u model.User, a model.API) bool {
	developer, admin := u.Role == "api_developer", u.Role == "super_admin"
	for _, r := range u.Roles {
		developer = developer || r == "api_developer"
		admin = admin || r == "super_admin"
	}
	return !developer || admin || (a.OwnerUserID != "" && a.OwnerUserID == u.ID)
}
