// Package publicweb serves static documentation on a separate, read-only listener.
// It accepts only the backend's public DTO handler, never an admin handler/store.
package publicweb

import (
	"api-manager/internal/model"
	"bytes"
	"encoding/json"
	"html"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"api-manager/internal/catalog"
)

const maxCatalogBytes = 2 << 20

type Handler struct {
	assets       fs.FS
	export       http.Handler
	siteProvider func() model.PublicSiteInfo
}

func New(directory string, export http.Handler) (*Handler, error) {
	assets := os.DirFS(directory)
	f, err := assets.Open("index.html")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return &Handler{assets: assets, export: export}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	// Static Next.js bootstrap needs inline script; all catalog content is escaped
	// React text. No backend-supplied HTML, Markdown, scripts or MDX is evaluated.
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/catalog.json" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.serveCatalog(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if r.URL.Path == "/" {
		name = "index.html"
	} else if !strings.HasPrefix(name, "_next/static/") || !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00") {
		http.NotFound(w, r)
		return
	}
	f, err := h.assets.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	if name != "index.html" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	if name == "index.html" && h.siteProvider != nil {
		data, err := io.ReadAll(io.LimitReader(f, 4<<20))
		if err != nil {
			unavailable(w)
			return
		}
		site := h.siteProvider()
		data = pageTitle.ReplaceAllLiteral(data, []byte("<title>"+html.EscapeString(site.PublicTitle)+"</title>"))
		data = pageDescription.ReplaceAllLiteral(data, []byte(`<meta name="description" content="`+html.EscapeString(site.Description)+`"/>`))
		extra := `<meta name="keywords" content="` + html.EscapeString(site.Keywords) + `"/><meta property="og:title" content="` + html.EscapeString(site.PublicTitle) + `"/><meta property="og:description" content="` + html.EscapeString(site.Description) + `"/>`
		if site.WebsiteURL != "" {
			extra += `<link rel="canonical" href="` + html.EscapeString(strings.TrimRight(site.WebsiteURL, "/")+"/") + `"/>`
		}
		data = bytes.Replace(data, []byte("</head>"), []byte(extra+"</head>"), 1)
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
		return
	}
	if seek, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, name, info.ModTime(), seek)
		return
	}
	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "documentation unavailable", 503)
		return
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

func (h *Handler) serveCatalog(w http.ResponseWriter, r *http.Request) {
	// A fresh internal request prevents incoming credentials, cookies, query strings,
	// headers and redirect destinations from crossing the public export boundary.
	req := &http.Request{Method: http.MethodGet, URL: &url.URL{Path: "/public/v1/catalog"}, Header: make(http.Header)}
	req = req.WithContext(r.Context())
	response := &boundedResponse{headers: make(http.Header)}
	h.export.ServeHTTP(response, req)
	if response.status != http.StatusOK || response.oversized {
		unavailable(w)
		return
	}
	var dto catalog.Response
	if json.Unmarshal(response.body.Bytes(), &dto) != nil || dto.Version != 1 || dto.APIs == nil {
		unavailable(w)
		return
	}
	// Struct re-encoding strips any accidental non-public fields in the response.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(dto); err != nil {
		return
	}
}
func unavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "public catalog unavailable"})
}

type boundedResponse struct {
	headers   http.Header
	body      bytes.Buffer
	status    int
	oversized bool
}

func (r *boundedResponse) Header() http.Header { return r.headers }
func (r *boundedResponse) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}
func (r *boundedResponse) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.oversized || r.body.Len()+len(p) > maxCatalogBytes {
		r.oversized = true
		return 0, io.ErrShortBuffer
	}
	return r.body.Write(p)
}

var pageTitle = regexp.MustCompile(`<title>[^<]*</title>`)
var pageDescription = regexp.MustCompile(`<meta name="description" content="[^"]*"/?>`)

func (h *Handler) SetSiteProvider(provider func() model.PublicSiteInfo) { h.siteProvider = provider }
