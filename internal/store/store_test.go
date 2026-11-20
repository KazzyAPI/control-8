package store

import (
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
	if n != 6 {
		t.Fatalf("schema_migrations rows = %d, want 6", n)
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
