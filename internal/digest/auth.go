package digest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
)

type cred struct {
	Auth          string `json:"auth"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	IdentityToken string `json:"identitytoken"`
}

// Keychain serves registry credentials parsed from C8_REGISTRY_AUTH (a Docker config.json). It never prints them.
// shortcut: credential helpers (credsStore/credHelpers) are not supported, only inline auths; upgrade if a registry needs one.
type Keychain struct{ auths map[string]cred }

// ParseAuth parses the config.json contents; empty means anonymous. The error never contains the input.
func ParseAuth(raw string) (*Keychain, error) {
	k := &Keychain{auths: map[string]cred{}}
	if strings.TrimSpace(raw) == "" {
		return k, nil
	}
	var c struct {
		Auths map[string]cred `json:"auths"`
	}
	if json.Unmarshal([]byte(raw), &c) != nil {
		return nil, errors.New("not a valid docker config.json")
	}
	for h, v := range c.Auths {
		h = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://"), "/")
		h = strings.TrimSuffix(h, "/v1")
		if h == "index.docker.io" {
			h = "docker.io"
		}
		k.auths[h] = v
	}
	return k, nil
}

func (k *Keychain) Resolve(r authn.Resource) (authn.Authenticator, error) {
	h := r.RegistryStr()
	if h == "index.docker.io" {
		h = "docker.io"
	}
	c, ok := k.auths[h]
	if !ok {
		return authn.Anonymous, nil
	}
	return authn.FromConfig(authn.AuthConfig{Username: c.Username, Password: c.Password, Auth: c.Auth, IdentityToken: c.IdentityToken}), nil
}

// Redact removes every configured secret from s (registry errors may echo request details).
func (k *Keychain) Redact(s string) string {
	for _, c := range k.auths {
		for _, v := range []string{c.Auth, c.Password, c.IdentityToken} {
			if v != "" {
				s = strings.ReplaceAll(s, v, "***")
			}
		}
	}
	return s
}

// Resolver returns the registry digest of repo:tag (K8S-051).
func (k *Keychain) Resolver() func(context.Context, string) (string, error) {
	return func(ctx context.Context, ref string) (string, error) {
		return crane.Digest(ref, crane.WithContext(ctx), crane.WithAuthFromKeychain(k))
	}
}
