package coredb

import (
	"fmt"
	"net/url"
	"strings"
)

// Dialect names the SQL dialect behind a database URI. Every dialect-aware
// decision in this repo — which goose migration tree to run, which driver to
// open, which placeholder style the store emits — branches on this one value
// so the branches stay findable.
type Dialect string

const (
	// DialectPostgres is the docker-compose and production backend.
	DialectPostgres Dialect = "postgres"
	// DialectSQLite is the zero-dependency local and test backend. It uses
	// modernc.org/sqlite, a pure-Go translation, so no cgo and no system
	// libsqlite3 are needed.
	DialectSQLite Dialect = "sqlite"
)

// Target is a parsed database URI: which dialect it names, which registered
// database/sql driver opens it, and the DSN to hand that driver.
type Target struct {
	Dialect Dialect
	// Driver is the database/sql driver name. "pgx" for Postgres
	// (jackc/pgx/v5/stdlib), "sqlite" for SQLite (modernc.org/sqlite).
	Driver string
	// DSN is what sql.Open receives. For Postgres it is the URI unchanged;
	// for SQLite it is the file path rewritten into a `file:` URI carrying
	// the pragmas the schema depends on.
	DSN string
}

// gooseDialect maps Target onto the name goose's SetDialect expects.
func (t Target) gooseDialect() string {
	if t.Dialect == DialectSQLite {
		return "sqlite3"
	}
	return "postgres"
}

// migrationsDir is the embedded tree of migrations for this dialect. The two
// trees hold the same logical schema in each dialect's own SQL — see the
// header comment on migrations/sqlite/000001_hello.sql.
func (t Target) migrationsDir() string {
	return "migrations/" + string(t.Dialect)
}

// sqlitePragmas are appended to every SQLite DSN. They are set per
// connection, not in the migration, because a pragma set inside a migration
// does not outlive that migration's connection:
//
//	foreign_keys   — SQLite ignores REFERENCES clauses unless this is on, so
//	                 without it the schema's cascades silently do nothing.
//	busy_timeout   — wait rather than fail instantly when another connection
//	                 holds the write lock.
//	journal_mode   — WAL lets reads proceed during a write.
const sqlitePragmas = "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"

// ParseTarget resolves a database URI to a Target. Recognized forms:
//
//	postgresql://user:pass@host:5432/db?sslmode=disable   -> Postgres
//	postgres://…                                          -> Postgres
//	sqlite:./dev.db, sqlite://./dev.db, sqlite3:…         -> SQLite (file)
//	file:./dev.db                                         -> SQLite (file)
//	sqlite::memory:                                       -> SQLite (in-memory)
//
// Anything else is an error rather than a guess: silently defaulting to the
// wrong dialect would run the wrong migration tree against a real database.
func ParseTarget(dbURI string) (Target, error) {
	if dbURI == "" {
		return Target{}, fmt.Errorf("database URI is empty")
	}

	scheme, rest, hasScheme := strings.Cut(dbURI, ":")
	if !hasScheme {
		return Target{}, fmt.Errorf("database URI %q has no scheme: expected postgresql://…, sqlite:…, or file:…", dbURI)
	}

	switch strings.ToLower(scheme) {
	case "postgres", "postgresql":
		return Target{Dialect: DialectPostgres, Driver: "pgx", DSN: dbURI}, nil

	case "sqlite", "sqlite3", "file":
		// Accept sqlite:path, sqlite://path and sqlite:///abs/path alike.
		// Stripping the leading slashes keeps `sqlite://./dev.db` (a URI
		// whose "host" is the relative path) and `sqlite:./dev.db` resolving
		// to the same file instead of two different ones.
		path := strings.TrimPrefix(rest, "//")
		if path == "" {
			return Target{}, fmt.Errorf("SQLite URI %q names no file: use sqlite:./dev.db or sqlite::memory:", dbURI)
		}
		// An existing query string is the caller's own pragma/option set;
		// ours are appended so an explicit choice wins (modernc.org/sqlite
		// applies repeated _pragma values in order).
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		options := sqlitePragmas
		// An in-memory database is PRIVATE to one connection unless it is
		// shared. Without this, `Up` migrates a database that is destroyed
		// when it closes its pool, and the store then opens a second, empty
		// one — every request answering "no such table". The name is what
		// the two pools then agree on.
		if isMemoryPath(path) {
			options = "mode=memory&cache=shared&" + sqlitePragmas
		}
		return Target{Dialect: DialectSQLite, Driver: "sqlite", DSN: "file:" + path + sep + options}, nil
	}

	// A bare scheme that parses as a URL but isn't one we serve is more
	// useful reported by name than as a parse failure.
	if u, err := url.Parse(dbURI); err == nil && u.Scheme != "" {
		return Target{}, fmt.Errorf("unsupported database scheme %q: tinku serves postgres and sqlite", u.Scheme)
	}
	return Target{}, fmt.Errorf("could not parse database URI %q", dbURI)
}

// isMemoryPath reports whether a SQLite path names the in-memory database
// rather than a file, in either spelling the driver accepts.
func isMemoryPath(path string) bool {
	name, _, _ := strings.Cut(path, "?")
	return name == ":memory:" || name == "memory"
}
