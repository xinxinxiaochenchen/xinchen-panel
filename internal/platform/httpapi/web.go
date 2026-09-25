package httpapi

import (
	"bytes"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

// NewWebHandler serves a read-only SPA beside the API. It never shadows API
// routes, and missing assets are 404 rather than the SPA's HTML fallback.
func NewWebHandler(api http.Handler, assets fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		clean := path.Clean("/" + r.URL.Path)
		name := strings.TrimPrefix(clean, "/")
		if name == "" {
			name = "index.html"
		}
		if strings.HasPrefix(name, "assets/") {
			serveWebFile(w, r, assets, name)
			return
		}
		if name != "index.html" && path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		serveWebFile(w, r, assets, "index.html")
	})
}

func serveWebFile(w http.ResponseWriter, r *http.Request, assets fs.FS, name string) {
	contents, err := fs.ReadFile(assets, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(contents))
}
