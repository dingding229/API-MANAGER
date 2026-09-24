package web

import (
	"embed"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

//go:embed assets/*
var assets embed.FS

func Console() http.Handler {
	fileServer := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/console" {
			http.Redirect(w, r, "/console/", http.StatusPermanentRedirect)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/console/")
		if path == "" || path == "index.html" {
			contents, err := assets.ReadFile("assets/index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(contents)
			return
		}
		if strings.Contains(path, "..") {
			http.NotFound(w, r)
			return
		}
		if contentType := mime.TypeByExtension(filepath.Ext(path)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		clone := r.Clone(r.Context())
		clone.URL.Path = "/assets/" + path
		fileServer.ServeHTTP(w, clone)
	})
}
