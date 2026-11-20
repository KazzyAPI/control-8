package config

import (
	"errors"
	"testing"
	"time"
)

// ARC config table: defaults, validation, _FILE variants; exit-2 errors name the variable.
func TestConfigTable(t *testing.T) {
	files := map[string]string{"/s/tok": "[]\n", "/s/bg": "x"}
	cases := []struct {
		name    string
		env     map[string]string
		wantVar string // "" = success
	}{
		{"minimal", map[string]string{"C8_PUBLIC_URL": "http://x"}, ""},
		{"missing public url", map[string]string{}, "C8_PUBLIC_URL"},
		{"bad public url", map[string]string{"C8_PUBLIC_URL": "nope"}, "C8_PUBLIC_URL"},
		{"bad duration", map[string]string{"C8_PUBLIC_URL": "http://x", "C8_GRANT_MAX": "4x"}, "C8_GRANT_MAX"},
		{"grant max over cap", map[string]string{"C8_PUBLIC_URL": "http://x", "C8_GRANT_MAX": "48h"}, "C8_GRANT_MAX"},
		{"digest poll 0 ok", map[string]string{"C8_PUBLIC_URL": "http://x", "C8_DIGEST_POLL": "0"}, ""},
		{"inventory poll 0 bad", map[string]string{"C8_PUBLIC_URL": "http://x", "C8_INVENTORY_POLL": "0s"}, "C8_INVENTORY_POLL"},
		{"targets not json", map[string]string{"C8_PUBLIC_URL": "http://x", "C8_TARGETS": "{"}, "C8_TARGETS"},
		{"tokens from file", map[string]string{"C8_PUBLIC_URL": "http://x", "C8_TOKENS_FILE": "/s/tok"}, ""},
		{"both env and file", map[string]string{"C8_PUBLIC_URL": "http://x", "C8_TOKENS": "[]", "C8_TOKENS_FILE": "/s/tok"}, "C8_TOKENS"},
		{"missing file", map[string]string{"C8_PUBLIC_URL": "http://x", "C8_BREAKGLASS_HASH_FILE": "/nope"}, "C8_BREAKGLASS_HASH_FILE"},
		{"oidc half set", map[string]string{"C8_PUBLIC_URL": "http://x", "C8_OIDC_ISSUER": "https://i"}, "C8_OIDC_CLIENT_ID"},
		{"bad log level", map[string]string{"C8_PUBLIC_URL": "http://x", "C8_LOG_LEVEL": "loud"}, "C8_LOG_LEVEL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Load(func(k string) string { return tc.env[k] }, func(p string) ([]byte, error) {
				if s, ok := files[p]; ok {
					return []byte(s), nil
				}
				return nil, errors.New("nf")
			})
			if tc.wantVar == "" {
				if err != nil {
					t.Fatal(err)
				}
				if c.Listen != ":8000" || c.GrantMax != 4*time.Hour || c.DataDir != "/var/lib/control8" {
					t.Fatalf("bad defaults: %+v", c)
				}
				return
			}
			var ce *Error
			if !errors.As(err, &ce) || ce.Var != tc.wantVar {
				t.Fatalf("want error on %s, got %v", tc.wantVar, err)
			}
		})
	}
}
