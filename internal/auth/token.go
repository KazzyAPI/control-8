package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/KazzyAPI/control-8/internal/audit"
	"github.com/KazzyAPI/control-8/internal/store"
)

// TokenDef is one C8_TOKENS entry (AUTH-031).
type TokenDef struct {
	Name      string  `json:"name"`
	OwnerSub  string  `json:"owner_sub"`
	Namespace *string `json:"namespace"`
	Role      string  `json:"role"`
	SHA256    string  `json:"sha256"`
	ExpiresAt string  `json:"expires_at"`
}

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Roles lists valid role names, lowest to highest.
var Roles = []string{"viewer", "operator", "admin", "global_admin"}

func ValidRole(r string) bool {
	for _, x := range Roles {
		if x == r {
			return true
		}
	}
	return false
}

// GenToken returns a new plaintext token and its SHA-256 hex (AUTH-030).
func GenToken() (plain, hash string) {
	b := make([]byte, 32)
	rand.Read(b)
	plain = "c8_" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	return plain, HashToken(plain)
}

func HashToken(plain string) string {
	h := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(h[:])
}

// ParseTokens validates C8_TOKENS entries; errors name the entry, never a hash (AUTH-031).
func ParseTokens(raw []json.RawMessage, now time.Time) ([]TokenDef, error) {
	var out []TokenDef
	names, hashes := map[string]bool{}, map[string]bool{}
	for i, r := range raw {
		var t TokenDef
		dec := json.NewDecoder(bytes.NewReader(r))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&t); err != nil {
			return nil, fmt.Errorf("C8_TOKENS[%d]: invalid entry: %v", i, err)
		}
		id := fmt.Sprintf("C8_TOKENS[%d] %q", i, t.Name)
		exp, err := time.Parse(time.RFC3339, t.ExpiresAt)
		switch {
		case t.Name == "" || t.OwnerSub == "" || t.Role == "" || t.SHA256 == "" || t.ExpiresAt == "":
			return nil, fmt.Errorf("%s: name, owner_sub, role, sha256 and expires_at are required", id)
		case !ValidRole(t.Role):
			return nil, fmt.Errorf("%s: invalid role", id)
		case !hex64.MatchString(t.SHA256):
			return nil, fmt.Errorf("%s: sha256 must be 64 lowercase hex chars", id)
		case err != nil:
			return nil, fmt.Errorf("%s: expires_at must be RFC 3339", id)
		case exp.After(now.AddDate(0, 0, 365)):
			return nil, fmt.Errorf("%s: expires_at more than 365 days away", id)
		case names[t.Name]:
			return nil, fmt.Errorf("%s: duplicate name", id)
		case hashes[t.SHA256]:
			return nil, fmt.Errorf("%s: duplicate sha256", id)
		}
		names[t.Name], hashes[t.SHA256] = true, true
		out = append(out, t)
	}
	return out, nil
}

// SyncTokens upserts defs into the token table, revokes entries no longer configured and
// audits the change (AUTH-033). Only hashes and metadata are stored.
func SyncTokens(db *sql.DB, defs []TokenDef, now time.Time, log *slog.Logger) error {
	have := map[string]bool{}
	rows, err := db.Query(`SELECT name, revoked_at IS NULL FROM token`)
	if err != nil {
		return err
	}
	live := map[string]bool{}
	for rows.Next() {
		var n string
		var active bool
		rows.Scan(&n, &active)
		have[n], live[n] = true, active
	}
	rows.Close()
	var added, removed []string
	for _, d := range defs {
		exp, _ := time.Parse(time.RFC3339, d.ExpiresAt)
		if _, err := db.Exec(`INSERT INTO token(id,name,hash,owner_sub,namespace_id,role,expires_at) VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(name) DO UPDATE SET hash=excluded.hash, owner_sub=excluded.owner_sub, namespace_id=excluded.namespace_id,
			role=excluded.role, expires_at=excluded.expires_at, revoked_at=NULL`,
			NewID("tok"), d.Name, d.SHA256, d.OwnerSub, d.Namespace, d.Role, store.Ts(exp)); err != nil {
			return err
		}
		if !live[d.Name] {
			added = append(added, d.Name)
		}
		delete(live, d.Name)
		if n, _ := OwnerOf(db, d.OwnerSub); n == "" {
			log.Warn("token owner not resolved; token rejected until the owner logs in via OIDC", "token", d.Name)
		}
	}
	for n, active := range live {
		if active {
			removed = append(removed, n)
			if _, err := db.Exec(`UPDATE token SET revoked_at=? WHERE name=?`, store.Ts(now), n); err != nil {
				return err
			}
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	if len(added)+len(removed) == 0 {
		return nil
	}
	return audit.Write(db, audit.Event{Action: "token.config_sync", Details: map[string]any{"added": added, "removed": removed}})
}

// OwnerOf returns the id of the single OIDC user whose subject is sub; "" if none or ambiguous (AUTH-032).
func OwnerOf(db *sql.DB, sub string) (string, []string) {
	rows, err := db.Query(`SELECT id, groups FROM "user" WHERE subject=? AND is_break_glass=0`, sub)
	if err != nil {
		return "", nil
	}
	defer rows.Close()
	var id, g string
	n := 0
	for rows.Next() {
		rows.Scan(&id, &g)
		n++
	}
	if n != 1 {
		return "", nil
	}
	var groups []string
	json.Unmarshal([]byte(g), &groups)
	return id, groups
}

// NewID returns prefix_ + 16 lowercase base32 chars (D-24).
func NewID(prefix string) string {
	b := make([]byte, 10)
	rand.Read(b)
	return prefix + "_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

// Bootstrap creates a global_admin binding for group if none exists (AUTH-021).
func Bootstrap(db *sql.DB, group string) error {
	if group == "" {
		return nil
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM role_binding WHERE role='global_admin'`).Scan(&n)
	if n > 0 {
		return nil
	}
	id := NewID("rb")
	if _, err := db.Exec(`INSERT INTO role_binding(id,idp_group,namespace_id,role) VALUES(?,?,NULL,'global_admin')`, id, group); err != nil {
		return err
	}
	return audit.Write(db, audit.Event{Action: "role_binding.create", TargetType: "role_binding", TargetID: id, Details: map[string]any{"group": group, "bootstrap": true}})
}
