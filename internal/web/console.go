package web

import (
	"embed"
	"html"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

//go:embed assets/*
var assets embed.FS

func Console() http.Handler { return ConsoleAt("/admin") }
func ConsoleAt(base string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(405)
			return
		}
		if r.URL.Path == base {
			http.Redirect(w, r, base+"/", http.StatusPermanentRedirect)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, base+"/")
		if path == "" || path == "index.html" {
			path = "index.html"
		}
		if path != "index.html" && path != "app.css" && path != "app.js" {
			http.NotFound(w, r)
			return
		}
		contents, err := assets.ReadFile("assets/" + path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if path == "index.html" {
			contents = []byte(strings.ReplaceAll(string(contents), "__ADMIN_PATH__", html.EscapeString(base)))
		}
		if contentType := mime.TypeByExtension(filepath.Ext(path)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "HEAD" {
			// #nosec G705 -- bytes are embedded assets; the only substitution is the HTML-escaped, validated server-side ADMIN_PATH, never request input.
			_, _ = w.Write(contents)
		}
	})
}
