package coredb

import (
	"path/filepath"
	"testing"
)

// TestParseTarget pins the URI forms the CLI documents, including the two
// spellings of a relative SQLite path that must resolve to the same file.
func TestParseTarget(t *testing.T) {
	cases := []struct {
		uri     string
		dialect Dialect
		driver  string
		dsn     string
	}{
		{"postgresql://u:p@h:5432/db?sslmode=disable", DialectPostgres, "pgx", "postgresql://u:p@h:5432/db?sslmode=disable"},
		{"postgres://u:p@h:5432/db", DialectPostgres, "pgx", "postgres://u:p@h:5432/db"},
		{"sqlite:./dev.db", DialectSQLite, "sqlite", "file:./dev.db?" + sqlitePragmas},
		{"sqlite://./dev.db", DialectSQLite, "sqlite", "file:./dev.db?" + sqlitePragmas},
		{"file:/tmp/x.db", DialectSQLite, "sqlite", "file:/tmp/x.db?" + sqlitePragmas},
		{"sqlite:./dev.db?_pragma=foreign_keys(0)", DialectSQLite, "sqlite", "file:./dev.db?_pragma=foreign_keys(0)&" + sqlitePragmas},
	}
	for _, tc := range cases {
		got, err := ParseTarget(tc.uri)
		if err != nil {
			t.Fatalf("ParseTarget(%q): %v", tc.uri, err)
		}
		if got.Dialect != tc.dialect || got.Driver != tc.driver || got.DSN != tc.dsn {
			t.Errorf("ParseTarget(%q) = %+v, want dialect %s driver %s dsn %s",
				tc.uri, got, tc.dialect, tc.driver, tc.dsn)
		}
	}

	for _, bad := range []string{"", "mysql://h/db", "./dev.db", "sqlite:"} {
		if _, err := ParseTarget(bad); err == nil {
			t.Errorf("ParseTarget(%q) succeeded, want an error", bad)
		}
	}
}

// TestSQLiteMigrationRoundTrip runs the real migration tree up, confirms
// nothing is left pending, and rolls it back down to zero. SQLite is used
// because it needs no running server, so this covers the migrations on every
// developer machine and in CI without a container.
func TestSQLiteMigrationRoundTrip(t *testing.T) {
	uri := "sqlite:" + filepath.Join(t.TempDir(), "test.db")

	if err := Up(uri); err != nil {
		t.Fatalf("Up: %v", err)
	}

	pending, err := Pending(uri)
	if err != nil {
		t.Fatalf("Pending after Up: %v", err)
	}
	if pending {
		t.Error("Pending reports work outstanding right after a successful Up")
	}

	// The schema the api depends on must actually be there — a migration
	// that runs but creates nothing would pass the version check above.
	db, _, err := Open(uri)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	for _, table := range []string{"users", "sessions", "greetings"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Errorf("table %s missing after Up: %v", table, err)
		}
	}

	if err := Reset(uri); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	pending, err = Pending(uri)
	if err != nil {
		t.Fatalf("Pending after Reset: %v", err)
	}
	if !pending {
		t.Error("Pending reports nothing outstanding after a full rollback")
	}
}

// TestUpIsIdempotent covers the `serve` path: every replica runs Up on boot,
// so a second run against a current database must be a no-op, not an error.
func TestUpIsIdempotent(t *testing.T) {
	uri := "sqlite:" + filepath.Join(t.TempDir(), "test.db")
	if err := Up(uri); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	if err := Up(uri); err != nil {
		t.Fatalf("second Up: %v", err)
	}
}
