package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

// organizationColumnsSQL joins both roster counts in as scalar subqueries — the
// same shape as the Postgres sibling, for the same reason.
const organizationColumnsSQL = `
SELECT g.id, g.slug, g.origin_domain, g.name, g.blurb, g.description, g.publish_events, g.created_at, g.updated_at,
       (SELECT count(*) FROM organization_members m WHERE m.organization_id = g.id),
       (SELECT count(*) FROM organization_members m WHERE m.organization_id = g.id AND m.role = 'owner')
FROM organizations g`

func scanOrganization(row rowScanner) (*store.Organization, error) {
	var g store.Organization
	var createdAt, updatedAt string
	var publish *string
	err := row.Scan(&g.ID, &g.Slug, &g.OriginDomain, &g.Name, &g.Blurb, &g.Description,
		&publish, &createdAt, &updatedAt, &g.MemberCount, &g.OwnerCount)
	if err != nil {
		return nil, notFoundIfNoRows(err, "scanning organization")
	}
	// A null column is "unset", which is a third state and not a synonym
	// for "out" — see store.PublishSetting.
	if publish != nil {
		g.PublishEvents = store.PublishSetting(*publish)
	}
	if g.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, err
	}
	if g.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return nil, err
	}
	return &g, nil
}

// CreateOrganization writes the organization and its first owner in one transaction: a
// organization with no owner can never be edited and can never be given one.
func (s *Store) CreateOrganization(ctx context.Context, in store.OrganizationInput, firstOwnerID string) (*store.Organization, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning organization transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has run

	id := store.NewID()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO organizations (id, slug, origin_domain, name, blurb, description, search_text, publish_events)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, in.Slug, in.OriginDomain, in.Name, in.Blurb, in.Description, in.SearchText,
		nullablePublish(in.PublishEvents)); err != nil {
		return nil, fmt.Errorf("creating organization: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO organization_members (organization_id, user_id, role) VALUES (?, ?, 'owner')`,
		id, firstOwnerID); err != nil {
		return nil, fmt.Errorf("creating organization owner: %w", err)
	}
	organization, err := scanOrganization(tx.QueryRowContext(ctx, organizationColumnsSQL+` WHERE g.id = ?`, id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing organization: %w", err)
	}
	return organization, nil
}

// UpdateOrganization replaces every own field. The slug is not among them: it is
// half of the organization's federated address, and an address that moves when
// somebody renames an organization is not an address.
func (s *Store) UpdateOrganization(ctx context.Context, id string, in store.OrganizationInput) (*store.Organization, error) {
	res, err := s.db.ExecContext(ctx, `
UPDATE organizations SET name = ?, blurb = ?, description = ?, search_text = ?,
                        publish_events = ?, updated_at = `+nowSQL+`
WHERE id = ?`, in.Name, in.Blurb, in.Description, in.SearchText,
		nullablePublish(in.PublishEvents), id)
	if err != nil {
		return nil, fmt.Errorf("updating organization: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("updating organization: %w", err)
	}
	if affected == 0 {
		return nil, store.ErrNotFound
	}
	return s.OrganizationByID(ctx, id)
}

func (s *Store) OrganizationByID(ctx context.Context, id string) (*store.Organization, error) {
	return scanOrganization(s.db.QueryRowContext(ctx, organizationColumnsSQL+` WHERE g.id = ?`, id))
}

func (s *Store) OrganizationBySlug(ctx context.Context, originDomain, slug string) (*store.Organization, error) {
	return scanOrganization(s.db.QueryRowContext(ctx,
		organizationColumnsSQL+` WHERE g.origin_domain = ? AND g.slug = ?`, originDomain, slug))
}

func (s *Store) ListOrganizations(ctx context.Context, f store.OrganizationFilter) ([]store.Organization, int64, error) {
	a := &args{}
	where := []string{}
	if f.Query != "" {
		where = append(where, "g.search_text LIKE "+a.next(like(f.Query))+` ESCAPE '\'`)
	}
	if f.MemberID != "" {
		where = append(where,
			"EXISTS (SELECT 1 FROM organization_members m WHERE m.organization_id = g.id AND m.user_id = "+a.next(f.MemberID)+")")
	}
	clause := whereClause(where)

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM organizations g`+clause, a.vals...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting organizations: %w", err)
	}

	limit, offset := clamp(f.Page)
	rows, err := s.db.QueryContext(ctx, organizationColumnsSQL+clause+
		` ORDER BY g.created_at DESC, g.id DESC LIMIT `+a.next(limit)+` OFFSET `+a.next(offset), a.vals...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing organizations: %w", err)
	}
	defer rows.Close()

	organizations := []store.Organization{}
	for rows.Next() {
		organization, err := scanOrganization(rows)
		if err != nil {
			return nil, 0, err
		}
		organizations = append(organizations, *organization)
	}
	return organizations, total, rows.Err()
}

// DeleteOrganization relies on ON DELETE CASCADE to take the roster and any
// gathering-ownership rows with it. Deleting an organization that is already gone
// is not an error: the caller asked for it to be absent, and it is.
func (s *Store) DeleteOrganization(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM organizations WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting organization: %w", err)
	}
	return nil
}

func (s *Store) SetOrganizationMember(ctx context.Context, organizationID, userID string, role store.OrganizationRole) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO organization_members (organization_id, user_id, role) VALUES (?, ?, ?)
ON CONFLICT (organization_id, user_id) DO UPDATE SET role = excluded.role`, organizationID, userID, string(role))
	if err != nil {
		return fmt.Errorf("setting organization member: %w", err)
	}
	return nil
}

func (s *Store) RemoveOrganizationMember(ctx context.Context, organizationID, userID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM organization_members WHERE organization_id = ? AND user_id = ?`, organizationID, userID); err != nil {
		return fmt.Errorf("removing organization member: %w", err)
	}
	return nil
}

const organizationMemberColumnsSQL = `
SELECT m.organization_id, m.user_id, u.handle, u.linkkeys_domain, u.display_name, m.role, m.joined_at
FROM organization_members m JOIN users u ON u.id = m.user_id`

func (s *Store) ListOrganizationMembers(ctx context.Context, organizationID string, page store.Page) ([]store.OrganizationMember, int64, error) {
	var total int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM organization_members WHERE organization_id = ?`, organizationID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting organization members: %w", err)
	}

	limit, offset := clamp(page)
	rows, err := s.db.QueryContext(ctx, organizationMemberColumnsSQL+`
WHERE m.organization_id = ?
ORDER BY CASE WHEN m.role = 'owner' THEN 0 ELSE 1 END, m.joined_at, u.handle
LIMIT ? OFFSET ?`, organizationID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing organization members: %w", err)
	}
	defer rows.Close()

	members := []store.OrganizationMember{}
	for rows.Next() {
		var m store.OrganizationMember
		var role, joinedAt string
		if err := rows.Scan(&m.OrganizationID, &m.UserID, &m.Handle, &m.LinkkeysDomain, &m.DisplayName,
			&role, &joinedAt); err != nil {
			return nil, 0, fmt.Errorf("scanning organization member: %w", err)
		}
		m.Role = store.OrganizationRole(role)
		var err error
		if m.JoinedAt, err = parseTime(joinedAt); err != nil {
			return nil, 0, err
		}
		members = append(members, m)
	}
	return members, total, rows.Err()
}

func (s *Store) OrganizationRoleFor(ctx context.Context, organizationID, userID string) (store.OrganizationRole, bool, error) {
	var role string
	err := s.db.QueryRowContext(ctx,
		`SELECT role FROM organization_members WHERE organization_id = ? AND user_id = ?`, organizationID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading organization role: %w", err)
	}
	return store.OrganizationRole(role), true, nil
}
