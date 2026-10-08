package web

import (
	"api-manager/internal/model"
	"embed"
	"html"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

//go:embed assets/*
var assets embed.FS

func Console() http.Handler              { return ConsoleAt("/admin") }
func ConsoleAt(base string) http.Handler { return ConsoleWithSite(base, nil) }
func ConsoleWithSite(base string, provider func() model.PublicSiteInfo) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != base && !strings.HasPrefix(r.URL.Path, base+"/") {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, base+"/")
		if r.URL.Path == base || path == "" || path == "index.html" {
			path = "index.html"
		}
		if path != "index.html" && path != "app.css" && path != "app.js" && path != "controls.css" && path != "account.js" && path != "consolidation.js" && path != "enhancements.js" && path != "database.js" {
			http.NotFound(w, r)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(405)
			return
		}
		if r.URL.Path == base {
			http.Redirect(w, r, base+"/", http.StatusPermanentRedirect)
			return
		}

		contents, err := assets.ReadFile("assets/" + path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if path == "index.html" {
			if provider != nil {
				info := provider()
				contents = []byte(strings.Replace(string(contents), "<title>API Manager Console</title>", "<title>"+html.EscapeString(info.AdminTitle)+"</title>", 1))
			}

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

// Shared style files are read-only and avoid tying the user center to a custom admin path.
func SharedStyles() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(405)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/ui/")
		if path != "admin.css" && path != "controls.css" {
			http.NotFound(w, r)
			return
		}
		name := "app.css"
		if path == "controls.css" {
			name = path
		}
		data, err := assets.ReadFile("assets/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == "GET" {
			// #nosec G705 -- data is an embedded, allowlisted CSS asset. No request values are reflected; text/css and nosniff prevent HTML interpretation.
			_, _ = w.Write(data)
		}
	})
}

// PasskeyClient is a public, embedded script shared by both UI surfaces.
func PasskeyClient() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		data, e := assets.ReadFile("assets/passkeys.js")
		if e != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-cache")
		if r.Method == "GET" {
			_, _ = w.Write(data)
		}
	})
}
