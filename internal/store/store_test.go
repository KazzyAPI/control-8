package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenMigrateTwiceIsNoop(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n)
	if n != 9 {
		t.Fatalf("schema_migrations rows = %d, want 9", n)
	}
	if _, err := db.Exec(`SELECT name FROM target`); err != nil {
		t.Fatal(err)
	}
	var wal string
	var fk, bt int
	db.QueryRow(`PRAGMA journal_mode`).Scan(&wal)
	db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	db.QueryRow(`PRAGMA busy_timeout`).Scan(&bt)
	if wal != "wal" || fk != 1 || bt != 5000 {
		t.Fatalf("pragmas wal=%s fk=%d bt=%d", wal, fk, bt)
	}
}

// PRD-002, PRD-003, PRD-010, GRT: migration 0009 on a pre-product database. Every asset lands in `unassigned`,
// slug collisions are renamed and audited, grants and bindings survive, and a second run changes nothing.
func TestMigrate0009_Products(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "c.db")+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	db.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`)
	files, _ := migrations.ReadDir("migrations")
	for _, f := range files[:8] {
		b, _ := migrations.ReadFile("migrations/" + f.Name())
		if _, err := db.Exec(string(b)); err != nil {
			t.Fatal(f.Name(), err)
		}
		db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, f.Name())
	}
	for _, q := range []string{
		`INSERT INTO namespace(id,slug,display_name,environment_tier,created_at,updated_at) VALUES('n1','a','A','prod','x','x'),('n2','b','B','non-prod','x','x')`,
		`INSERT INTO asset(id,namespace_id,slug,name,created_at,updated_at) VALUES('as1','n1','web','W','2020-01-01','x'),('as2','n2','web','W2','2020-01-02','x'),('as3','n2','db','D','2020-01-03','x')`,
		`INSERT INTO role_binding(id,idp_group,namespace_id,role) VALUES('rb1','g','n1','operator'),('rb2','ga',NULL,'global_admin')`,
		`INSERT INTO access_grant(id,requester_id,target_id,namespace_id,reason,duration_minutes,status,approver_group,created_at,updated_at) VALUES('gr1','u','t','n1','a reason here',30,'requested','apr','x','x')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err, q)
		}
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	slugs := func() string {
		var s string
		db.QueryRow(`SELECT group_concat(product_id||':'||home_namespace_id||':'||slug) FROM (SELECT * FROM asset ORDER BY id)`).Scan(&s)
		return s
	}
	want := "prd_unassigned:n1:web,prd_unassigned:n2:web-b,prd_unassigned:n2:db"
	if got := slugs(); got != want {
		t.Fatalf("assets %q want %q", got, want)
	}
	count := func(q string) (n int) { db.QueryRow(q).Scan(&n); return }
	if n := count(`SELECT count(*) FROM audit_event WHERE action='asset.migrated_rename' AND actor_type='system' AND target_id='as2'`); n != 1 {
		t.Fatalf("rename audits = %d", n)
	}
	if n := count(`SELECT count(*) FROM access_grant WHERE id='gr1' AND product_id='prd_unassigned' AND status='requested'`); n != 1 {
		t.Fatal("grant lost or not scoped to unassigned")
	}
	if n := count(`SELECT count(*) FROM role_binding WHERE product_id IS NULL AND ((id='rb1' AND namespace_id='n1' AND role='operator') OR (id='rb2' AND namespace_id IS NULL))`); n != 2 {
		t.Fatal("role bindings changed")
	}
	if n := count(`SELECT count(*) FROM product WHERE slug='unassigned' AND builtin=1`); n != 1 {
		t.Fatal("unassigned product missing")
	}
	// second run is a no-op
	before := slugs()
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if slugs() != before || count(`SELECT count(*) FROM audit_event WHERE action='asset.migrated_rename'`) != 1 || count(`SELECT count(*) FROM product`) != 1 || count(`SELECT count(*) FROM schema_migrations`) != 9 {
		t.Fatal("second Migrate changed the database")
	}
}
