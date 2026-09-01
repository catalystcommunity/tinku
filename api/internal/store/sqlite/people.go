package sqlite

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
	return scanUser(s.db.QueryRowContext(ctx,
		userColumnsSQL+` WHERE lower(handle) = ? AND lower(linkkeys_domain) = ?`, handle, domain))
}

// SearchUsers completes a handle prefix.
func (s *Store) SearchUsers(ctx context.Context, prefix string, page store.Page) ([]store.User, error) {
	limit, offset := clamp(page)
	rows, err := s.db.QueryContext(ctx,
		userColumnsSQL+` WHERE lower(handle) LIKE ? ESCAPE '\' ORDER BY handle LIMIT ? OFFSET ?`,
		like(prefix)[1:], limit, offset) // drop the leading '%': a prefix match, not a substring one
	if err != nil {
		return nil, fmt.Errorf("searching users: %w", err)
	}
	defer rows.Close()
	return scanUsers(rows)
}

// SetAdmin grants or revokes the global admin role. admin_granted_at is
// cleared on revoke: the column answers "since when", and a stale value on
// somebody who no longer holds the role would answer it wrongly.
func (s *Store) SetAdmin(ctx context.Context, userID string, granted bool) error {
	var grantedAt *string
	if granted {
		now := formatTime(time.Now())
		grantedAt = &now
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET is_admin = ?, admin_granted_at = ? WHERE id = ?`, granted, grantedAt, userID)
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
	rows, err := s.db.QueryContext(ctx,
		userColumnsSQL+` WHERE is_admin = 1 ORDER BY admin_granted_at, handle`)
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
		var kind, createdAt string
		var grantedAt *string
		if err := rows.Scan(&u.ID, &u.LinkkeysDomain, &u.LinkkeysUserID, &u.Handle, &u.DisplayName,
			&kind, &createdAt, &u.IsAdmin, &grantedAt); err != nil {
			return nil, fmt.Errorf("scanning user: %w", err)
		}
		u.Kind = store.UserKind(kind)
		var err error
		if u.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		if u.AdminGrantedAt, err = parseOptionalTime(grantedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// notFoundIfNoRows maps the driver's "no rows" to the package sentinel, so
// every lookup in this backend answers a missing row the same way.
func notFoundIfNoRows(err error, what string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	return fmt.Errorf("%s: %w", what, err)
}
