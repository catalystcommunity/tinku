package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

const userColumnsSQL = `
SELECT id, linkkeys_domain, linkkeys_user_id, handle, display_name, kind, created_at, is_admin, admin_granted_at
FROM users`

// UserByHandle resolves one federated address to a user.
func (s *Store) UserByHandle(ctx context.Context, handle, domain string) (*store.User, error) {
	// Folded on both sides. The caller lowercases what it was given
	// (csilservices.FindUser), and Postgres `=` and `LIKE` are
	// case-SENSITIVE where SQLite's LIKE is not — comparing the raw column
	// is how the member picker works in every test and finds nobody in
	// production.
	row := s.db.QueryRowContext(ctx,
		userColumnsSQL+` WHERE lower(handle) = $1 AND lower(linkkeys_domain) = $2`, handle, domain)
	return scanUser(row)
}

// SearchUsers completes a handle prefix.
func (s *Store) SearchUsers(ctx context.Context, prefix string, page store.Page) ([]store.User, error) {
	limit, offset := clamp(page)
	rows, err := s.db.QueryContext(ctx,
		userColumnsSQL+` WHERE lower(handle) LIKE $1 ESCAPE '\' ORDER BY handle LIMIT $2 OFFSET $3`,
		like(prefix)[1:], limit, offset) // drop the leading '%': a prefix match, not a substring one
	if err != nil {
		return nil, fmt.Errorf("searching users: %w", err)
	}
	defer rows.Close()
	return scanUsers(rows)
}

// SetAdmin grants or revokes the global admin role.
//
// admin_granted_at is cleared on revoke rather than kept as a history of
// the last grant: the column answers "since when", and a stale value on a
// user who no longer holds the role would answer it wrongly.
func (s *Store) SetAdmin(ctx context.Context, userID string, granted bool) error {
	var grantedAt *time.Time
	if granted {
		now := time.Now().UTC()
		grantedAt = &now
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET is_admin = $1, admin_granted_at = $2 WHERE id = $3`, granted, grantedAt, userID)
	if err != nil {
		return fmt.Errorf("setting admin role: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("setting admin role: %w", err)
	}
	if affected == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ListAdmins returns everybody holding the role, oldest grant first.
func (s *Store) ListAdmins(ctx context.Context) ([]store.User, error) {
	rows, err := s.db.QueryContext(ctx, userColumnsSQL+` WHERE is_admin ORDER BY admin_granted_at, handle`)
	if err != nil {
		return nil, fmt.Errorf("listing admins: %w", err)
	}
	defer rows.Close()
	return scanUsers(rows)
}

func scanUsers(rows *sql.Rows) ([]store.User, error) {
	users := []store.User{}
	for rows.Next() {
		var u store.User
		var kind string
		if err := rows.Scan(&u.ID, &u.LinkkeysDomain, &u.LinkkeysUserID, &u.Handle, &u.DisplayName,
			&kind, &u.CreatedAt, &u.IsAdmin, &u.AdminGrantedAt); err != nil {
			return nil, fmt.Errorf("scanning user: %w", err)
		}
		u.Kind = store.UserKind(kind)
		users = append(users, u)
	}
	return users, rows.Err()
}

// rowScanner is what *sql.Row and *sql.Rows have in common, so one scan
// function serves both a single lookup and a row of a listing.
type rowScanner interface{ Scan(dest ...any) error }

// rowsScanner is a result set being walked, for a collector that does not
// care which query produced it.
type rowsScanner interface {
	rowScanner
	Next() bool
	Err() error
}

// notFoundIfNoRows maps the driver's "no rows" to the package sentinel, so
// every lookup in this backend answers a missing row the same way.
func notFoundIfNoRows(err error, what string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	return fmt.Errorf("%s: %w", what, err)
}
