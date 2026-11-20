// Package web serves the embedded SPA with history fallback (UI-005).
package web

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Handler serves files from fsys; unknown GET/HEAD paths get index.html. /api and /auth are never
// shadowed. Hashed assets are immutable, index.html is no-store.
func Handler(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || p == "/api" || p == "/auth" ||
			strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/auth/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean(p), "/")
		if st, err := fs.Stat(fsys, name); name == "" || err != nil || st.IsDir() {
			name = "index.html"
		}
		if name == "index.html" {
			w.Header().Set("Cache-Control", "no-store")
		} else if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeFileFS(w, r, fsys, name)
	})
}
