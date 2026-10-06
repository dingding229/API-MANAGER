package gateway

import (
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/schema"
	"api-manager/internal/store"
	"api-manager/internal/version"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type cachedPluginResponse struct {
	Status  int         `json:"status"`
	Headers http.Header `json:"headers"`
	Body    []byte      `json:"body"`
}

var cachedHeaderNames = []string{"Content-Type", "Content-Language", "Content-Encoding", "Content-Disposition"}

func pluginCacheKey(r *http.Request, a model.API, h plugin.Handler) (string, bool) {
	if !a.PluginCache.Enabled || a.PluginCache.Validate(a.Plugin) != nil || (r.Method != http.MethodGet && r.Method != http.MethodHead && !(r.Method == http.MethodPost && a.PluginCache.CachePOST)) || r.Header.Get("Cookie") != "" || r.Header.Get("Range") != "" || r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" || strings.Contains(strings.ToLower(strings.Join(r.Header.Values("Cache-Control"), ",")), "no-cache") || strings.Contains(strings.ToLower(strings.Join(r.Header.Values("Cache-Control"), ",")), "no-store") {
		return "", false
	}
	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(io.LimitReader(r.Body, model.MaxPluginCacheBody+1))
		if err != nil {
			r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), &cacheBodyReadError{err: err}, r.Body))
		} else {
			r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))
		}
		if err != nil || len(body) > model.MaxPluginCacheBody {
			return "", false
		}
	}
	headers := r.Header.Clone()
	// Infrastructure correlation headers do not select business data. All other
	// headers (including KEY), exact body, path, query and consumer identity do.
	for _, name := range []string{"X-Request-ID", "Traceparent", "Tracestate", "Baggage", "CF-Ray"} {
		headers.Del(name)
	}
	rev := h.Name()
	if identified, ok := h.(interface{ CacheRevision() string }); ok {
		rev = identified.CacheRevision()
	}
	digest, err := json.Marshal(struct {
		API                                                        model.API
		Method, Path, Query, Host, Identity, PluginRevision, Build string
		Headers                                                    http.Header
		Body                                                       []byte
	}{a, r.Method, r.URL.Path, r.URL.RawQuery, r.Host, clientIdentity(r), rev, version.Revision, headers, body})
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(digest)
	return hex.EncodeToString(sum[:]), true
}
func cacheContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 300*time.Millisecond)
}
func (g *Gateway) handlePlugin(w http.ResponseWriter, r *http.Request, a model.API, h plugin.Handler) error {
	cached, ok := g.store.(store.PluginCacheStore)
	key, eligible := pluginCacheKey(r, a, h)
	if !ok || !eligible || g.cacheEncryptionKey == "" {
		return h.Handle(r.Context(), w, r, a)
	}
	// Fixed stripes bound memory. Check again after the lock so concurrent cold
	// requests share one computed result without bypassing per-request auth/quota.
	prefix, _ := hex.DecodeString(key[:2])
	index := int(prefix[0]) % len(g.cacheLocks)
	g.cacheLocks[index].Lock()
	defer g.cacheLocks[index].Unlock()
	if err := r.Context().Err(); err != nil {
		return err
	}
	ctx, cancel := cacheContext(r.Context())
	entry, err := cached.GetPluginCache(ctx, a.ID, key, time.Now().UTC())
	cancel()
	if err == nil && entry.APIUpdatedAt.Equal(a.UpdatedAt) {
		raw, decryptErr := auth.DecryptSecret(g.cacheEncryptionKey+":plugin-cache:"+key, entry.Ciphertext)
		var response cachedPluginResponse
		if decryptErr == nil && len(raw) <= 2<<20 && json.Unmarshal([]byte(raw), &response) == nil && response.Status == 200 && len(response.Body) <= model.MaxPluginCacheBody && (schema.IsEmpty(a.ResponseSchema) || schema.ValidateInstance(a.ResponseSchema, response.Body) == nil) {
			for _, name := range cachedHeaderNames {
				for _, value := range response.Headers.Values(name) {
					w.Header().Add(name, value)
				}
			}
			w.Header().Set("X-Plugin-Cache", "HIT")
			w.WriteHeader(response.Status)
			_, writeErr := w.Write(response.Body)
			return writeErr
		}
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		g.logger.Warn("plugin cache read unavailable", "api_id", a.ID)
	}
	collector := &pluginCacheWriter{ResponseWriter: w, headers: make(http.Header)}
	w.Header().Set("X-Plugin-Cache", "MISS")
	if err := h.Handle(r.Context(), collector, r, a); err != nil {
		return err
	}
	if collector.status != 200 || collector.overflow || collector.headers.Get("Set-Cookie") != "" || strings.Contains(strings.Join(collector.headers.Values("Vary"), ","), "*") || strings.Contains(strings.ToLower(strings.Join(collector.headers.Values("Cache-Control"), ",")), "private") || strings.Contains(strings.ToLower(strings.Join(collector.headers.Values("Cache-Control"), ",")), "no-store") || strings.Contains(strings.ToLower(strings.Join(collector.headers.Values("Cache-Control"), ",")), "no-cache") {
		return nil
	}
	if !schema.IsEmpty(a.ResponseSchema) && schema.ValidateInstance(a.ResponseSchema, collector.body.Bytes()) != nil {
		return nil
	}
	response := cachedPluginResponse{Status: collector.status, Headers: make(http.Header), Body: append([]byte(nil), collector.body.Bytes()...)}
	for _, name := range cachedHeaderNames {
		if values := collector.headers.Values(name); len(values) > 0 {
			response.Headers[name] = append([]string(nil), values...)
		}
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return nil
	}
	encrypted, err := auth.EncryptSecret(g.cacheEncryptionKey+":plugin-cache:"+key, string(raw))
	if err != nil {
		return nil
	}
	now := time.Now().UTC()
	entry = model.PluginCacheEntry{APIID: a.ID, Key: key, Ciphertext: encrypted, APIUpdatedAt: a.UpdatedAt, CreatedAt: now, ExpiresAt: now.Add(time.Duration(a.PluginCache.TTLSeconds) * time.Second)}
	ctx, cancel = cacheContext(r.Context())
	err = cached.PutPluginCache(ctx, entry, a.PluginCache.MaxEntries)
	cancel()
	if err != nil && !errors.Is(err, store.ErrConflict) {
		g.logger.Warn("plugin cache write unavailable", "api_id", a.ID)
	}
	return nil
}

type pluginCacheWriter struct {
	http.ResponseWriter
	headers  http.Header
	body     bytes.Buffer
	status   int
	overflow bool
}

func (w *pluginCacheWriter) Header() http.Header { return w.headers }
func (w *pluginCacheWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	for name, values := range w.headers {
		w.ResponseWriter.Header()[name] = append([]string(nil), values...)
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *pluginCacheWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if !w.overflow {
		if w.body.Len()+len(body) > model.MaxPluginCacheBody {
			w.overflow = true
			w.body.Reset()
		} else {
			_, _ = w.body.Write(body)
		}
	}
	return w.ResponseWriter.Write(body)
}
func (w *pluginCacheWriter) Flush() {
	w.overflow = true
	w.body.Reset()
	if w.status == 0 {
		w.WriteHeader(200)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *pluginCacheWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type cacheBodyReadError struct{ err error }

func (r *cacheBodyReadError) Read([]byte) (int, error) { err := r.err; r.err = io.EOF; return 0, err }
