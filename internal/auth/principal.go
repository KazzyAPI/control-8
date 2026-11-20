package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/KazzyAPI/control-8/internal/store"
)

var ErrUnauthenticated = errors.New("unauthenticated")

// Principal is the authenticated caller. For tokens, Groups are the owner's (AUTH-032).
type Principal struct {
	ID, Display, AuthMethod, CSRF string
	Groups                        []string
	BreakGlass                    bool
	TokenRole, TokenNS            string // tokens only; TokenNS "" = every namespace
}

func (p *Principal) IsToken() bool { return p.AuthMethod == "token" }

func randHex(n int) string { b := make([]byte, n); rand.Read(b); return hex.EncodeToString(b) }

// NewSession stores a session (hash only) and returns the cookie value and CSRF token (AUTH-005).
func NewSession(db *sql.DB, userID, method string, ttl time.Duration, now time.Time) (cookie, csrf string, err error) {
	cookie, csrf = randHex(32), randHex(16)
	_, err = db.Exec(`INSERT INTO session(id_hash,user_id,csrf_token,auth_method,expires_at,created_at) VALUES(?,?,?,?,?,?)`,
		HashToken(cookie), userID, csrf, method, store.Ts(now.Add(ttl)), store.Ts(now))
	return
}

func DeleteSession(db *sql.DB, cookie string) {
	db.Exec(`DELETE FROM session WHERE id_hash=?`, HashToken(cookie))
}

// Authenticate resolves a bearer token (AUTH-035: header only) or a session cookie.
func Authenticate(db *sql.DB, authz, cookie string, now time.Time) (*Principal, error) {
	if authz != "" {
		plain, ok := strings.CutPrefix(authz, "Bearer ")
		if !ok || !strings.HasPrefix(plain, "c8_") {
			return nil, ErrUnauthenticated
		}
		return byToken(db, plain, now)
	}
	if cookie == "" {
		return nil, ErrUnauthenticated
	}
	var p Principal
	var groups string
	var bg bool
	err := db.QueryRow(`SELECT u.id, u.display_name, s.auth_method, s.csrf_token, u.groups, u.is_break_glass
		FROM session s JOIN "user" u ON u.id=s.user_id WHERE s.id_hash=? AND s.expires_at>?`, HashToken(cookie), store.Ts(now)).
		Scan(&p.ID, &p.Display, &p.AuthMethod, &p.CSRF, &groups, &bg)
	if err != nil {
		return nil, ErrUnauthenticated
	}
	p.BreakGlass = bg
	json.Unmarshal([]byte(groups), &p.Groups)
	return &p, nil
}

func byToken(db *sql.DB, plain string, now time.Time) (*Principal, error) {
	h := HashToken(plain)
	var id, name, sub, role, exp string
	var ns sql.NullString
	err := db.QueryRow(`SELECT id,name,owner_sub,namespace_id,role,expires_at FROM token WHERE hash=? AND revoked_at IS NULL`, h).
		Scan(&id, &name, &sub, &ns, &role, &exp)
	if err != nil || exp <= store.Ts(now) {
		return nil, ErrUnauthenticated
	}
	owner, groups := OwnerOf(db, sub)
	if owner == "" {
		return nil, ErrUnauthenticated
	}
	// at most once a minute (AUTH-034)
	db.Exec(`UPDATE token SET last_used_at=? WHERE id=? AND (last_used_at IS NULL OR last_used_at<?)`,
		store.Ts(now), id, store.Ts(now.Add(-time.Minute)))
	return &Principal{ID: id, Display: name, AuthMethod: "token", Groups: groups, TokenRole: role, TokenNS: ns.String}, nil
}
