package digest

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
)

func host(srv *httptest.Server) string { return strings.TrimPrefix(srv.URL, "http://") }

// K8S-051: credentials from C8_REGISTRY_AUTH are used for lookups; the registry error never echoes them; bad config is rejected without the value.
func TestK8S051_RegistryAuth(t *testing.T) {
	reg := registry.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "bot" || p != "s3cret" {
			w.Header().Set("WWW-Authenticate", `Basic realm="r"`)
			http.Error(w, "denied for s3cret", 401)
			return
		}
		reg.ServeHTTP(w, r)
	}))
	defer srv.Close()
	img, _ := random.Image(64, 1)
	if err := crane.Push(img, host(srv)+"/app:1", crane.WithAuth(&authn.Basic{Username: "bot", Password: "s3cret"})); err != nil {
		t.Fatal(err)
	}
	want, _ := img.Digest()

	good := `{"auths":{"` + host(srv) + `":{"auth":"` + base64.StdEncoding.EncodeToString([]byte("bot:s3cret")) + `"}}}`
	kc, err := ParseAuth(good)
	if err != nil {
		t.Fatal(err)
	}
	if d, err := kc.Resolver()(context.Background(), host(srv)+"/app:1"); err != nil || d != want.String() {
		t.Fatal(d, err)
	}
	anon, _ := ParseAuth("")
	_, err = anon.Resolver()(context.Background(), host(srv)+"/app:1")
	if err == nil {
		t.Fatal("anonymous lookup should fail")
	}
	bad := `{"auths":{"` + host(srv) + `":{"username":"bot","password":"s3cret"}}}`
	kb, _ := ParseAuth(bad)
	if kb.Redact("x s3cret y") != "x *** y" {
		t.Fatal("not redacted")
	}
	if _, err := ParseAuth("{nope s3cret"); err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Fatal(err)
	}
}

// K8S-051: only tagged images are followed; untagged means latest; digest-only refs are skipped.
func TestK8S051_ParseRef(t *testing.T) {
	for _, c := range []struct {
		in, repo, tag, pin string
		ok                 bool
	}{
		{"a/b:1", "a/b", "1", "", true},
		{"a/b", "a/b", "latest", "", true},
		{"h:5000/a/b:1@sha256:x", "h:5000/a/b", "1", "sha256:x", true},
		{"h:5000/a/b", "h:5000/a/b", "latest", "", true},
		{"a/b@sha256:x", "a/b", "", "sha256:x", false},
	} {
		r, tg, p, ok := parseRef(c.in)
		if r != c.repo || tg != c.tag || p != c.pin || ok != c.ok {
			t.Errorf("%s -> %s %s %s %v", c.in, r, tg, p, ok)
		}
	}
}
