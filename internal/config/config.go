// Package config parses C8_* environment variables (ARC config table).
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Listen, DataDir, PublicURL                         string
	OIDCIssuer, OIDCClientID, OIDCClientSecret         string
	OIDCGroupsClaim, BreakglassHash, BootstrapAdminGrp string
	GrantMax, GrantMaxCap                              time.Duration
	Targets, Tokens                                    []json.RawMessage
	WebhookURL, WebhookSecret, RegistryAuth            string
	InventoryPoll, RolloutTimeout, DigestPoll          time.Duration
	DockerHost, ParachuteURL, ParachuteToken           string
	ParachutePoll                                      time.Duration
	ParachuteScope, LogLevel                           string
	EventsReplay, EventsMaxConns                       int
	EventsHeartbeat                                    time.Duration
}

// Error names the offending variable; secret values are never included.
type Error struct{ Var, Msg string }

func (e *Error) Error() string { return fmt.Sprintf("config: %s: %s", e.Var, e.Msg) }

// Load reads config via getenv and readFile (injected for tests).
func Load(getenv func(string) string, readFile func(string) ([]byte, error)) (*Config, error) {
	var err error
	fail := func(name, msg string) {
		if err == nil {
			err = &Error{name, msg}
		}
	}
	// str returns env, or the contents of <name>_FILE; both set is an error.
	str := func(name string) string {
		v, f := getenv(name), getenv(name+"_FILE")
		if v != "" && f != "" {
			fail(name, "both "+name+" and "+name+"_FILE are set")
			return ""
		}
		if f != "" {
			b, e := readFile(f)
			if e != nil {
				fail(name+"_FILE", "cannot read file")
				return ""
			}
			return strings.TrimRight(string(b), "\r\n")
		}
		return v
	}
	def := func(name, d string) string {
		if v := getenv(name); v != "" {
			return v
		}
		return d
	}
	dur := func(name, d string, allowZero bool) time.Duration {
		v, e := time.ParseDuration(def(name, d))
		if e != nil || v < 0 || (v == 0 && !allowZero) {
			fail(name, "invalid duration")
		}
		return v
	}
	list := func(name string) []json.RawMessage {
		s := str(name)
		if s == "" {
			return nil
		}
		var l []json.RawMessage
		if e := json.Unmarshal([]byte(s), &l); e != nil {
			fail(name, "must be a JSON list")
		}
		return l
	}

	num := func(name, d string) int {
		v, e := strconv.Atoi(def(name, d))
		if e != nil || v <= 0 {
			fail(name, "must be a positive integer")
		}
		return v
	}

	c := &Config{
		Listen:            def("C8_LISTEN", ":8000"),
		DataDir:           def("C8_DATA_DIR", "/var/lib/control8"),
		PublicURL:         getenv("C8_PUBLIC_URL"),
		OIDCIssuer:        getenv("C8_OIDC_ISSUER"),
		OIDCClientID:      getenv("C8_OIDC_CLIENT_ID"),
		OIDCClientSecret:  str("C8_OIDC_CLIENT_SECRET"),
		OIDCGroupsClaim:   def("C8_OIDC_GROUPS_CLAIM", "groups"),
		BreakglassHash:    str("C8_BREAKGLASS_HASH"),
		BootstrapAdminGrp: getenv("C8_BOOTSTRAP_ADMIN_GROUP"),
		GrantMax:          dur("C8_GRANT_MAX", "4h", false),
		GrantMaxCap:       dur("C8_GRANT_MAX_CAP", "24h", false),
		Targets:           list("C8_TARGETS"),
		Tokens:            list("C8_TOKENS"),
		WebhookURL:        getenv("C8_WEBHOOK_URL"),
		WebhookSecret:     str("C8_WEBHOOK_SECRET"),
		RegistryAuth:      str("C8_REGISTRY_AUTH"),
		InventoryPoll:     dur("C8_INVENTORY_POLL", "30s", false),
		RolloutTimeout:    dur("C8_ROLLOUT_TIMEOUT", "5m", false),
		DigestPoll:        dur("C8_DIGEST_POLL", "15m", true),
		DockerHost:        getenv("C8_DOCKER_HOST"),
		ParachuteURL:      getenv("C8_PARACHUTE_URL"),
		ParachuteToken:    str("C8_PARACHUTE_TOKEN"),
		ParachutePoll:     dur("C8_PARACHUTE_POLL", "30s", false),
		ParachuteScope:    def("C8_PARACHUTE_SCOPE", "control-8"),
		LogLevel:          def("C8_LOG_LEVEL", "info"),
		EventsReplay:      num("C8_EVENTS_REPLAY", "10000"),
		EventsMaxConns:    num("C8_EVENTS_MAX_CONNS", "500"),
		EventsHeartbeat:   dur("C8_EVENTS_HEARTBEAT", "15s", false),
	}
	if err != nil {
		return nil, err
	}
	if u, e := url.Parse(c.PublicURL); c.PublicURL == "" || e != nil || u.Scheme == "" || u.Host == "" {
		fail("C8_PUBLIC_URL", "required absolute URL")
	}
	if c.GrantMax > c.GrantMaxCap {
		fail("C8_GRANT_MAX", "must be <= C8_GRANT_MAX_CAP")
	}
	if (c.OIDCIssuer != "") != (c.OIDCClientID != "") {
		fail("C8_OIDC_CLIENT_ID", "C8_OIDC_ISSUER and C8_OIDC_CLIENT_ID must be set together")
	}
	if c.WebhookURL != "" {
		if u, e := url.Parse(c.WebhookURL); e != nil || u.Host == "" {
			fail("C8_WEBHOOK_URL", "invalid URL")
		}
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		fail("C8_LOG_LEVEL", "must be debug|info|warn|error")
	}
	return c, err
}

// FromOS loads config from the process environment.
func FromOS() (*Config, error) { return Load(os.Getenv, os.ReadFile) }
