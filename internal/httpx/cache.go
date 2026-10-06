package httpx

import (
	"bufio"
	"io/fs"
	"net"
	"net/http"
	"strings"
)

// CachePolicy is the final response boundary: an upstream proxy must not turn a
// dynamic response (even one named *.css) into a shared CDN cache entry.
func CachePolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		candidate := (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.HasPrefix(r.URL.Path, "/_next/static/") && fs.ValidPath(strings.TrimPrefix(r.URL.Path, "/")) && !strings.ContainsAny(r.URL.Path, "\\\x00") && r.Header.Get("Authorization") == "" && r.Header.Get("X-API-Key") == "" && r.Header.Get("Cookie") == ""
		writer := &cacheWriter{ResponseWriter: w, candidate: candidate}
		next.ServeHTTP(writer, r)
		if !writer.written {
			writer.WriteHeader(http.StatusOK)
		}
	})
}

type cacheWriter struct {
	http.ResponseWriter
	candidate, written bool
}

func (w *cacheWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *cacheWriter) WriteHeader(status int) {
	if w.written {
		return
	}
	immutable := strings.Contains(w.Header().Get("Cache-Control"), "immutable")
	// 1xx is not a final response; never advertise it as cacheable.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("CDN-Cache-Control", "no-store")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
	w.Header().Del("Expires")
	w.Header().Del("Surrogate-Control")
	if w.candidate && (status == 200 || status == 304) && w.Header().Get("Set-Cookie") == "" && immutable {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("CDN-Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("Cloudflare-CDN-Cache-Control", "public, max-age=31536000, immutable")
	}
	if status >= 200 {
		w.written = true
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *cacheWriter) Write(data []byte) (int, error) {
	if !w.written {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(data)
}
func (w *cacheWriter) Flush() {
	if !w.written {
		w.WriteHeader(200)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *cacheWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}
