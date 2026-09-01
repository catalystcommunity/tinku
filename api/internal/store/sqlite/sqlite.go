// Package sqlite implements store.Store against SQLite (modernc.org/sqlite,
// a pure-Go translation — no cgo, no system libsqlite3). It is the
// zero-dependency backend for local runs and tests: `tinku serve
// --db-uri=sqlite:./dev.db` needs nothing installed.
//
// Two things differ from the Postgres sibling (../postgres) and account for
// every difference in this file:
//
//   - Placeholders are `?`, not `$N`.
//   - SQLite has no timestamp type. Times are stored as RFC3339 UTC text in
//     the exact layout the schema's `strftime` defaults produce, so text
//     comparison and text ordering mean what the queries assume. formatTime
//     and parseTime own that conversion; nothing else in this file touches
//     the representation.
//
// Keep this file and ../postgres in step: a change to one is a change to both.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/store"
	"github.com/catalystcommunity/tinku/coredb"
)

// timeLayout is the on-disk representation of a timestamp. It must stay
// byte-identical to what the migration's
// `strftime('%Y-%m-%dT%H:%M:%SZ', 'now')` defaults write, because
// expires_at is compared and created_at is ordered as text.
const timeLayout = "2006-01-02T15:04:05Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// parseTime reads a stored timestamp. It parses with the full RFC3339 rules
// rather than timeLayout alone, so a value written with a fractional second
// or a non-Z offset (by a hand-run SQL statement, say) still loads instead
// of failing the whole query.
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing stored timestamp %q: %w", s, err)
	}
	return t.UTC(), nil
}

// parseOptionalTime reads a nullable stored timestamp. A null column and a
// zero time are different things — the second means "the epoch" — so the
// nullable columns carry a *time.Time all the way up.
func parseOptionalTime(s *string) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	t, err := parseTime(*s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Store is the SQLite implementation of store.Store.
type Store struct {
	db *sql.DB
}

// New wraps an already-open *sql.DB (coredb.Open produced it, so the pragmas
// the schema depends on are already set in the DSN).
func New(db *sql.DB) *Store {
	// SQLite serializes writers. Holding more than one connection open buys
	// nothing and turns contention that would have waited on the busy
	// timeout into "database is locked" errors instead.
	db.SetMaxOpenConns(1)
	return &Store{db: db}
}

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *Store) Close() error                   { return s.db.Close() }

const upsertUserSQL = `
INSERT INTO users (id, linkkeys_domain, linkkeys_user_id, handle, display_name, kind)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (linkkeys_domain, linkkeys_user_id) DO UPDATE
SET handle = excluded.handle, display_name = excluded.display_name
RETURNING id, linkkeys_domain, linkkeys_user_id, handle, display_name, kind, created_at, is_admin, admin_granted_at`

func (s *Store) UpsertUser(ctx context.Context, p store.UpsertUserParams) (*store.User, error) {
	row := s.db.QueryRowContext(ctx, upsertUserSQL,
		store.NewID(), p.LinkkeysDomain, p.LinkkeysUserID, p.Handle, p.DisplayName, string(p.Kind))
	return scanUser(row)
}

const userByIDSQL = `
SELECT id, linkkeys_domain, linkkeys_user_id, handle, display_name, kind, created_at, is_admin, admin_granted_at
FROM users WHERE id = ?`

func (s *Store) UserByID(ctx context.Context, id string) (*store.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, userByIDSQL, id))
}

func scanUser(row *sql.Row) (*store.User, error) {
	var u store.User
	var kind, createdAt string
	var grantedAt *string
	err := row.Scan(&u.ID, &u.LinkkeysDomain, &u.LinkkeysUserID, &u.Handle, &u.DisplayName, &kind, &createdAt,
		&u.IsAdmin, &grantedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning user: %w", err)
	}
	u.Kind = store.UserKind(kind)
	if u.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, err
	}
	if u.AdminGrantedAt, err = parseOptionalTime(grantedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

const createSessionSQL = `
INSERT INTO sessions (id, user_id, token_hash, expires_at) VALUES (?, ?, ?, ?)
RETURNING created_at`

func (s *Store) CreateSession(ctx context.Context, sess *store.Session) error {
	var createdAt string
	err := s.db.QueryRowContext(ctx, createSessionSQL,
		sess.ID, sess.UserID, sess.TokenHash, formatTime(sess.ExpiresAt)).Scan(&createdAt)
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	if sess.CreatedAt, err = parseTime(createdAt); err != nil {
		return err
	}
	return nil
}

// sessionByTokenHashSQL filters on expires_at as well as the hash, so an
// expired session is indistinguishable from a missing one to every caller.
// The comparison is a text comparison, which is why every timestamp is
// written in the one fixed-width layout.
const sessionByTokenHashSQL = `
SELECT s.id, s.user_id, s.token_hash, s.created_at, s.expires_at,
       u.id, u.linkkeys_domain, u.linkkeys_user_id, u.handle, u.display_name, u.kind, u.created_at,
       u.is_admin, u.admin_granted_at
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = ? AND s.expires_at > ?`

func (s *Store) SessionByTokenHash(ctx context.Context, tokenHash string) (*store.Session, *store.User, error) {
	var sess store.Session
	var u store.User
	var kind, sessCreated, sessExpires, userCreated string
	var userGranted *string
	err := s.db.QueryRowContext(ctx, sessionByTokenHashSQL, tokenHash, formatTime(time.Now())).Scan(
		&sess.ID, &sess.UserID, &sess.TokenHash, &sessCreated, &sessExpires,
		&u.ID, &u.LinkkeysDomain, &u.LinkkeysUserID, &u.Handle, &u.DisplayName, &kind, &userCreated,
		&u.IsAdmin, &userGranted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, store.ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("looking up session: %w", err)
	}
	u.Kind = store.UserKind(kind)
	for _, conv := range []struct {
		raw string
		dst *time.Time
	}{{sessCreated, &sess.CreatedAt}, {sessExpires, &sess.ExpiresAt}, {userCreated, &u.CreatedAt}} {
		t, err := parseTime(conv.raw)
		if err != nil {
			return nil, nil, err
		}
		*conv.dst = t
	}
	granted, err := parseOptionalTime(userGranted)
	if err != nil {
		return nil, nil, err
	}
	u.AdminGrantedAt = granted
	return &sess, &u, nil
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, formatTime(time.Now()))
	if err != nil {
		return 0, fmt.Errorf("deleting expired sessions: %w", err)
	}
	return res.RowsAffected()
}

// CreateGreeting inserts, then reads back through the same join the listing
// uses. Postgres does this in one statement with a data-modifying CTE;
// SQLite does not allow INSERT inside a CTE, so it is two statements in a
// transaction instead — the transaction is what makes them equivalent.
func (s *Store) CreateGreeting(ctx context.Context, authorID, message string) (*store.Greeting, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning greeting transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has run

	id := store.NewID()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO greetings (id, author_id, message) VALUES (?, ?, ?)`, id, authorID, message); err != nil {
		return nil, fmt.Errorf("creating greeting: %w", err)
	}
	greeting, err := scanGreeting(tx.QueryRowContext(ctx, greetingColumnsSQL+` WHERE g.id = ?`, id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing greeting: %w", err)
	}
	return greeting, nil
}

const greetingColumnsSQL = `
SELECT g.id, g.author_id, u.handle, g.message, g.created_at
FROM greetings g LEFT JOIN users u ON u.id = g.author_id`

func (s *Store) ListGreetings(ctx context.Context) ([]store.Greeting, error) {
	rows, err := s.db.QueryContext(ctx, greetingColumnsSQL+` ORDER BY g.created_at DESC, g.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing greetings: %w", err)
	}
	defer rows.Close()

	greetings := []store.Greeting{}
	for rows.Next() {
		var g store.Greeting
		var createdAt string
		if err := rows.Scan(&g.ID, &g.AuthorID, &g.AuthorHandle, &g.Message, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning greeting: %w", err)
		}
		if g.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		greetings = append(greetings, g)
	}
	return greetings, rows.Err()
}

func (s *Store) GreetingByID(ctx context.Context, id string) (*store.Greeting, error) {
	return scanGreeting(s.db.QueryRowContext(ctx, greetingColumnsSQL+` WHERE g.id = ?`, id))
}

// rowScanner is what scanGreeting needs from either a *sql.Row or a
// transaction's row — CreateGreeting reads inside a transaction, the
// lookups read outside one.
type rowScanner interface{ Scan(dest ...any) error }

// rowsScanner is a result set being walked, for a collector that does not
// care which query produced it.
type rowsScanner interface {
	rowScanner
	Next() bool
	Err() error
}

func scanGreeting(row rowScanner) (*store.Greeting, error) {
	var g store.Greeting
	var createdAt string
	err := row.Scan(&g.ID, &g.AuthorID, &g.AuthorHandle, &g.Message, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning greeting: %w", err)
	}
	if g.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, err
	}
	return &g, nil
}

func init() {
	store.Register(coredb.DialectSQLite, func(dbURI string) (store.Store, error) {
		db, _, err := coredb.Open(dbURI)
		if err != nil {
			return nil, err
		}
		return New(db), nil
	})
}
