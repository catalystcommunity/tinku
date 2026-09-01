// Package postgres implements store.Store against PostgreSQL. It is the
// backend docker-compose and any real deployment use.
//
// Queries use $N placeholders and let the database keep timestamps as
// timestamptz, so time.Time values round-trip without conversion. The SQLite
// sibling (../sqlite) is the same schema in that dialect's own SQL; keep the
// two in step.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/store"
	"github.com/catalystcommunity/tinku/coredb"
)

// Store is the PostgreSQL implementation of store.Store.
type Store struct {
	db *sql.DB
}

// New wraps an already-open *sql.DB (coredb.Open produced it, so the driver
// and DSN are settled and the connection is verified).
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *Store) Close() error                   { return s.db.Close() }

const upsertUserSQL = `
INSERT INTO users (id, linkkeys_domain, linkkeys_user_id, handle, display_name, kind)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (linkkeys_domain, linkkeys_user_id) DO UPDATE
SET handle = EXCLUDED.handle, display_name = EXCLUDED.display_name
RETURNING id, linkkeys_domain, linkkeys_user_id, handle, display_name, kind, created_at, is_admin, admin_granted_at`

func (s *Store) UpsertUser(ctx context.Context, p store.UpsertUserParams) (*store.User, error) {
	row := s.db.QueryRowContext(ctx, upsertUserSQL,
		store.NewID(), p.LinkkeysDomain, p.LinkkeysUserID, p.Handle, p.DisplayName, string(p.Kind))
	return scanUser(row)
}

const userByIDSQL = `
SELECT id, linkkeys_domain, linkkeys_user_id, handle, display_name, kind, created_at, is_admin, admin_granted_at
FROM users WHERE id = $1`

func (s *Store) UserByID(ctx context.Context, id string) (*store.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, userByIDSQL, id))
}

func scanUser(row *sql.Row) (*store.User, error) {
	var u store.User
	var kind string
	err := row.Scan(&u.ID, &u.LinkkeysDomain, &u.LinkkeysUserID, &u.Handle, &u.DisplayName, &kind, &u.CreatedAt,
		&u.IsAdmin, &u.AdminGrantedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning user: %w", err)
	}
	u.Kind = store.UserKind(kind)
	return &u, nil
}

const createSessionSQL = `
INSERT INTO sessions (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, $4)
RETURNING created_at`

func (s *Store) CreateSession(ctx context.Context, sess *store.Session) error {
	err := s.db.QueryRowContext(ctx, createSessionSQL,
		sess.ID, sess.UserID, sess.TokenHash, sess.ExpiresAt).Scan(&sess.CreatedAt)
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	return nil
}

// sessionByTokenHashSQL filters on expires_at as well as the hash, so an
// expired session is indistinguishable from a missing one to every caller.
const sessionByTokenHashSQL = `
SELECT s.id, s.user_id, s.token_hash, s.created_at, s.expires_at,
       u.id, u.linkkeys_domain, u.linkkeys_user_id, u.handle, u.display_name, u.kind, u.created_at,
       u.is_admin, u.admin_granted_at
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.expires_at > $2`

func (s *Store) SessionByTokenHash(ctx context.Context, tokenHash string) (*store.Session, *store.User, error) {
	var sess store.Session
	var u store.User
	var kind string
	err := s.db.QueryRowContext(ctx, sessionByTokenHashSQL, tokenHash, time.Now().UTC()).Scan(
		&sess.ID, &sess.UserID, &sess.TokenHash, &sess.CreatedAt, &sess.ExpiresAt,
		&u.ID, &u.LinkkeysDomain, &u.LinkkeysUserID, &u.Handle, &u.DisplayName, &kind, &u.CreatedAt,
		&u.IsAdmin, &u.AdminGrantedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, store.ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("looking up session: %w", err)
	}
	u.Kind = store.UserKind(kind)
	return &sess, &u, nil
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = $1`, id); err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= $1`, time.Now().UTC())
	if err != nil {
		return 0, fmt.Errorf("deleting expired sessions: %w", err)
	}
	return res.RowsAffected()
}

// createGreetingSQL writes the row and reads it back through the same join
// the listing uses, so a freshly created greeting and a listed one are built
// from identical columns.
const createGreetingSQL = `
WITH inserted AS (
    INSERT INTO greetings (id, author_id, message) VALUES ($1, $2, $3)
    RETURNING id, author_id, message, created_at
)
SELECT i.id, i.author_id, u.handle, i.message, i.created_at
FROM inserted i LEFT JOIN users u ON u.id = i.author_id`

func (s *Store) CreateGreeting(ctx context.Context, authorID, message string) (*store.Greeting, error) {
	return scanGreeting(s.db.QueryRowContext(ctx, createGreetingSQL, store.NewID(), authorID, message))
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
		if err := rows.Scan(&g.ID, &g.AuthorID, &g.AuthorHandle, &g.Message, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning greeting: %w", err)
		}
		greetings = append(greetings, g)
	}
	return greetings, rows.Err()
}

func (s *Store) GreetingByID(ctx context.Context, id string) (*store.Greeting, error) {
	return scanGreeting(s.db.QueryRowContext(ctx, greetingColumnsSQL+` WHERE g.id = $1`, id))
}

func scanGreeting(row *sql.Row) (*store.Greeting, error) {
	var g store.Greeting
	err := row.Scan(&g.ID, &g.AuthorID, &g.AuthorHandle, &g.Message, &g.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning greeting: %w", err)
	}
	return &g, nil
}

func init() {
	store.Register(coredb.DialectPostgres, func(dbURI string) (store.Store, error) {
		db, _, err := coredb.Open(dbURI)
		if err != nil {
			return nil, err
		}
		return New(db), nil
	})
}
