// Package authz resolves effective roles from role bindings (AUTH-020) and enforces them (AUTH-022).
package authz

import (
	"database/sql"
	"errors"
	"slices"

	"github.com/KazzyAPI/control-8/internal/auth"
)

var (
	ErrNotFound  = errors.New("not found") // caller cannot view the namespace (API-005)
	ErrForbidden = errors.New("forbidden") // can view, lacks the role
)

// Rank orders roles: viewer 1 < operator 2 < admin 3 < global_admin 4.
func Rank(role string) int { return slices.Index(auth.Roles, role) + 1 }

// Effective returns the principal's rank in namespace ns ("" = global scope, only global_admin counts).
// Tokens get min(token role, owner's role) and only within their namespace (AUTH-032).
func Effective(db *sql.DB, p *auth.Principal, ns string) int { return EffectiveIn(db, p, ns, "") }

// EffectiveIn is Effective with product bindings (AUTH-024, PRD-011): highest of global, the namespace's
// (ns != "") and the product's (product != "", a product id) bindings. A stack passes both, a namespace only
// ns, a product or application only product. No deny bindings: highest wins.
func EffectiveIn(db *sql.DB, p *auth.Principal, ns, product string) int {
	r := 0
	if p.BreakGlass {
		r = 4
	} else if len(p.Groups) > 0 {
		rows, err := db.Query(`SELECT idp_group, namespace_id, product_id, role FROM role_binding`)
		if err != nil {
			return 0
		}
		defer rows.Close()
		for rows.Next() {
			var g, role string
			var n, prod sql.NullString
			rows.Scan(&g, &n, &prod, &role)
			if !slices.Contains(p.Groups, g) {
				continue
			}
			k := Rank(role)
			switch {
			case k == 4 && (n.Valid || prod.Valid), // global_admin is only global
				n.Valid && n.String != ns,
				prod.Valid && prod.String != product:
				continue
			}
			r = max(r, k)
		}
	}
	if ns == "" && product == "" && r < 4 {
		return 0
	}
	if p.IsToken() {
		if p.TokenNS != "" && p.TokenNS != ns {
			return 0
		}
		r = min(r, Rank(p.TokenRole))
	}
	return r
}

// Require checks the principal has at least min in ns ("" = global scope).
func Require(db *sql.DB, p *auth.Principal, ns, min string) error {
	return RequireIn(db, p, ns, "", min)
}

// RequireIn is Require over EffectiveIn; 404 when the caller has no role at all on the resource (API-005).
func RequireIn(db *sql.DB, p *auth.Principal, ns, product, min string) error {
	r := EffectiveIn(db, p, ns, product)
	switch {
	case r >= Rank(min):
		return nil
	case r == 0 && (ns != "" || product != ""):
		return ErrNotFound
	}
	return ErrForbidden
}
