package coredb

import (
	"database/sql"
	"fmt"

	// Registers the "pgx" driver for Postgres targets.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	// Registers the "sqlite" driver for SQLite targets. modernc.org/sqlite is
	// a pure-Go translation of SQLite, so this needs no cgo and no system
	// libsqlite3 — that is what makes `sqlite:./dev.db` a genuinely
	// zero-dependency local backend.
	_ "modernc.org/sqlite"
)

// Open opens a *sql.DB against dbURI and verifies the connection. Callers
// must Close the returned *sql.DB. The Target is returned alongside it
// because callers need the dialect too: the store emits different
// placeholders per dialect.
func Open(dbURI string) (*sql.DB, Target, error) {
	target, err := ParseTarget(dbURI)
	if err != nil {
		return nil, Target{}, err
	}

	db, err := sql.Open(target.Driver, target.DSN)
	if err != nil {
		return nil, Target{}, fmt.Errorf("opening %s database: %w", target.Dialect, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, Target{}, fmt.Errorf("pinging %s database: %w", target.Dialect, err)
	}
	return db, target, nil
}

// openGoose opens dbURI and points goose at the embedded migration tree for
// that dialect.
func openGoose(dbURI string) (*sql.DB, Target, error) {
	db, target, err := Open(dbURI)
	if err != nil {
		return nil, Target{}, err
	}
	goose.SetBaseFS(Migrations)
	if err := goose.SetDialect(target.gooseDialect()); err != nil {
		db.Close()
		return nil, Target{}, fmt.Errorf("setting goose dialect %s: %w", target.gooseDialect(), err)
	}
	return db, target, nil
}

// Up runs every pending migration against dbURI.
//
// On Postgres it takes a session-level advisory lock first, so two replicas
// booting at once do not both try to apply the same migration — the second
// blocks until the first is done, then finds nothing pending. SQLite needs
// no equivalent: it is a single file with a single writer, and the busy
// timeout in the DSN already serializes concurrent writers.
func Up(dbURI string) error {
	db, target, err := openGoose(dbURI)
	if err != nil {
		return err
	}
	defer db.Close()

	if target.Dialect == DialectPostgres {
		if _, err := db.Exec("SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
			return fmt.Errorf("acquiring migration advisory lock: %w", err)
		}
		defer db.Exec("SELECT pg_advisory_unlock($1)", migrationLockID) //nolint:errcheck // best-effort release; the session ending frees it anyway
	}

	if err := goose.Up(db, target.migrationsDir()); err != nil {
		return fmt.Errorf("running migrations up: %w", err)
	}
	return nil
}

// migrationLockID is the arbitrary but fixed key every tinku process uses
// for the Postgres migration advisory lock. It only has to agree with itself
// across replicas.
const migrationLockID = 4173

// Reset rolls every applied migration back, leaving the database at version
// zero. This is what `./tools.sh migrate down` invokes: with a single
// migration today "down" and "down to zero" are the same operation, and
// Reset keeps that true as more migrations land.
func Reset(dbURI string) error {
	db, target, err := openGoose(dbURI)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := goose.DownTo(db, target.migrationsDir(), 0); err != nil {
		return fmt.Errorf("running migrations down to zero: %w", err)
	}
	return nil
}

// Status prints the applied/pending state of every migration to stdout via
// goose's own logger.
func Status(dbURI string) error {
	db, target, err := openGoose(dbURI)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := goose.Status(db, target.migrationsDir()); err != nil {
		return fmt.Errorf("getting migration status: %w", err)
	}
	return nil
}

// Pending reports whether any migration has not yet been applied. `tinku
// serve` calls it after running migrations to confirm the schema really is
// current before it flips readiness on — a migration run that succeeded but
// left work behind (an operator ran `serve --skip-migrate` against a stale
// database, say) must not be reported as ready.
func Pending(dbURI string) (bool, error) {
	db, target, err := openGoose(dbURI)
	if err != nil {
		return false, err
	}
	defer db.Close()

	current, err := goose.GetDBVersion(db)
	if err != nil {
		return false, fmt.Errorf("reading current migration version: %w", err)
	}
	migrations, err := goose.CollectMigrations(target.migrationsDir(), 0, goose.MaxVersion)
	if err != nil {
		return false, fmt.Errorf("collecting migrations: %w", err)
	}
	latest, err := migrations.Last()
	if err != nil {
		return false, fmt.Errorf("finding latest migration: %w", err)
	}
	return current < latest.Version, nil
}
