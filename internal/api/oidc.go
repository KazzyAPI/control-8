package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/store"
)

const oidcCookie = "c8_oidc"

// provider lazily discovers the issuer (AUTH-001) so a slow IdP does not block startup.
func (s *server) provider(ctx context.Context) (*oidc.Provider, *oauth2.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.oidcProv == nil {
		p, err := oidc.NewProvider(ctx, s.cfg.OIDCIssuer)
		if err != nil {
			return nil, nil, err
		}
		s.oidcProv = p
	}
	return s.oidcProv, &oauth2.Config{ClientID: s.cfg.OIDCClientID, ClientSecret: s.cfg.OIDCClientSecret, Endpoint: s.oidcProv.Endpoint(),
		RedirectURL: strings.TrimRight(s.cfg.PublicURL, "/") + "/auth/callback", Scopes: []string{oidc.ScopeOpenID, "profile", "email", "groups"}}, nil
}

func rnd() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *server) setCookie(w http.ResponseWriter, name, val string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: val, Path: "/auth", HttpOnly: true, Secure: s.secureCookie(), SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}

// oidcLogin starts auth-code + PKCE; state.nonce.verifier ride in a short-lived cookie (AUTH-002).
func (s *server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.OIDCIssuer == "" {
		problem(w, 404, "not_found", "OIDC login is not configured")
		return
	}
	_, oc, err := s.provider(r.Context())
	if err != nil {
		problem(w, 502, "idp_unavailable", "OIDC discovery failed")
		return
	}
	state, nonce, verifier := rnd(), rnd(), oauth2.GenerateVerifier()
	s.setCookie(w, oidcCookie, state+"."+nonce+"."+verifier, 600)
	http.Redirect(w, r, oc.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

func (s *server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if s.cfg.OIDCIssuer == "" {
		problem(w, 404, "not_found", "OIDC login is not configured")
		return
	}
	c, _ := r.Cookie(oidcCookie)
	var parts []string
	if c != nil {
		parts = strings.Split(c.Value, ".")
	}
	s.setCookie(w, oidcCookie, "", -1)
	if len(parts) != 3 || subtle.ConstantTimeCompare([]byte(parts[0]), []byte(r.URL.Query().Get("state"))) != 1 {
		s.audit(r, nil, "auth.login", "user", "", "denied", map[string]any{"reason": "bad_state"})
		problem(w, 400, "invalid_state", "state mismatch")
		return
	}
	fail := func(status int, reason string, err error) {
		s.audit(r, nil, "auth.login", "user", "", "failed", map[string]any{"reason": reason})
		problem(w, status, reason, fmt.Sprint(err))
	}
	prov, oc, err := s.provider(r.Context())
	if err != nil {
		fail(502, "idp_unavailable", err)
		return
	}
	tok, err := oc.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(parts[2]))
	if err != nil {
		fail(400, "code_exchange_failed", err)
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := prov.Verifier(&oidc.Config{ClientID: s.cfg.OIDCClientID}).Verify(r.Context(), raw) // signature, iss, aud, exp
	if err != nil || subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(parts[1])) != 1 {
		fail(400, "invalid_id_token", fmt.Errorf("id token rejected"))
		return
	}
	var claims map[string]json.RawMessage
	idt.Claims(&claims)
	str := func(k string) (v string) { json.Unmarshal(claims[k], &v); return }
	groups := []string{}
	json.Unmarshal(claims[s.cfg.OIDCGroupsClaim], &groups) // missing/invalid claim = no groups (AUTH-003)
	if groups == nil {
		groups = []string{}
	}
	gj, _ := json.Marshal(groups)
	name := str("name")
	if name == "" {
		name = str("email")
	}
	if name == "" {
		name = idt.Subject
	}
	now := s.now()
	s.db.Exec(`INSERT INTO "user"(id,issuer,subject,email,display_name,groups,last_login_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(issuer,subject) DO UPDATE SET email=excluded.email,display_name=excluded.display_name,groups=excluded.groups,last_login_at=excluded.last_login_at`,
		auth.NewID("usr"), idt.Issuer, idt.Subject, str("email"), name, string(gj), store.Ts(now))
	var uid string
	s.db.QueryRow(`SELECT id FROM "user" WHERE issuer=? AND subject=?`, idt.Issuer, idt.Subject).Scan(&uid)
	cookie, _, err := auth.NewSession(s.db, uid, "oidc", 8*time.Hour, now)
	if err != nil {
		fail(500, "internal", err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: cookie, Path: "/", HttpOnly: true, Secure: s.secureCookie(), SameSite: http.SameSiteLaxMode, MaxAge: 8 * 3600})
	p := &auth.Principal{ID: uid, Display: name, AuthMethod: "oidc", Groups: groups}
	s.audit(r, p, "auth.login", "user", uid, "success", map[string]any{"groups": groups})
	dest := "/"
	if !s.hasAccess(groups) {
		dest = "/no-access" // AUTH-006
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// hasAccess: some role binding or namespace approver_group matches one of the groups.
func (s *server) hasAccess(groups []string) bool {
	rows, err := s.db.Query(`SELECT idp_group FROM role_binding UNION SELECT approver_group FROM namespace WHERE approver_group IS NOT NULL`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var g string
		rows.Scan(&g)
		if slices.Contains(groups, g) {
			return true
		}
	}
	return false
}

// approverFor lists namespace slugs whose approver_group is one of groups (AUTH-023).
func (s *server) approverFor(groups []string) []string {
	out := []string{}
	rows, err := s.db.Query(`SELECT slug, approver_group FROM namespace WHERE approver_group IS NOT NULL ORDER BY slug`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var slug, g string
		rows.Scan(&slug, &g)
		if slices.Contains(groups, g) {
			out = append(out, slug)
		}
	}
	return out
}

// approverForProducts lists product slugs whose approver_group is one of groups (PRD-023).
func (s *server) approverForProducts(groups []string) []string {
	out := []string{}
	rows, err := s.db.Query(`SELECT slug, approver_group FROM product WHERE approver_group IS NOT NULL ORDER BY slug`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var slug, g string
		rows.Scan(&slug, &g)
		if slices.Contains(groups, g) {
			out = append(out, slug)
		}
	}
	return out
}

// htmlPage serves the minimal /no-access page (/login is an SPA route).
func htmlPage(title, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><title>%s</title><h1>%s</h1>%s", html.EscapeString(title), html.EscapeString(title), body)
	}
}
