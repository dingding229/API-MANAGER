package api

import (
	"api-manager/internal/auth"
	"api-manager/internal/ids"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type mailPreviewEntry struct {
	HTML, UserID, SessionHash string
	Expires                   time.Time
}
type mailPreviewCache struct {
	mu      sync.Mutex
	entries map[string]mailPreviewEntry
}

func (a *Admin) issueMailPreview(r *http.Request, body string) string {
	c := &a.mailPreviews
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]mailPreviewEntry{}
	}
	now := time.Now()
	for key, v := range c.entries {
		if !v.Expires.After(now) || v.UserID == a.actorID(r) {
			delete(c.entries, key)
		}
	}
	if len(c.entries) >= 32 {
		return ""
	}
	id := ids.NewUUID()
	token, _ := auth.SessionToken(r)
	c.entries[id] = mailPreviewEntry{HTML: body, UserID: a.actorID(r), SessionHash: auth.HashAPIKey(token), Expires: now.Add(time.Minute)}
	return "/admin/v1/settings/email/preview/" + id
}
func (a *Admin) showMailPreview(w http.ResponseWriter, r *http.Request) {
	if !a.requireSiteOwner(w, r) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/v1/settings/email/preview/")
	c := &a.mailPreviews
	c.mu.Lock()
	v, ok := c.entries[id]
	token, _ := auth.SessionToken(r)
	if ok && v.UserID == a.actorID(r) && v.SessionHash == auth.HashAPIKey(token) {
		delete(c.entries, id)
	} else {
		ok = false
	}
	c.mu.Unlock()
	if !ok || !v.Expires.After(time.Now()) {
		writeJSON(w, 404, nil)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; script-src 'none'; sandbox; frame-ancestors 'self'; base-uri 'none'; form-action 'none'")
	w.Header().Del("X-Frame-Options")
	_, _ = io.WriteString(w, v.HTML)
}
