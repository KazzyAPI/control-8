package web

import (
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestHandler(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":    {Data: []byte("<html>spa</html>")},
		"assets/a-1.js": {Data: []byte("x")},
	}
	h := Handler(fsys)
	cases := []struct {
		method, path string
		code         int
		cache        string
	}{
		{"GET", "/", 200, "no-store"},                                         // UI-005
		{"GET", "/login", 200, "no-store"},                                    // SPA sign-in route (D-103)
		{"GET", "/n/shop/s/web", 200, "no-store"},                             // deep link falls back
		{"GET", "/assets/a-1.js", 200, "public, max-age=31536000, immutable"}, // hashed asset
		{"GET", "/api/v1/nope", 404, ""},                                      // never shadowed
		{"GET", "/auth/nope", 404, ""},
		{"POST", "/n/shop", 404, ""},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(c.method, c.path, nil))
		if w.Code != c.code || w.Header().Get("Cache-Control") != c.cache {
			t.Errorf("UI-005 %s %s: got %d %q", c.method, c.path, w.Code, w.Header().Get("Cache-Control"))
		}
	}
}
