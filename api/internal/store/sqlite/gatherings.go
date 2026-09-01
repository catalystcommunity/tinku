package sqlite

import (
	"context"
	"fmt"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

// gatheringColumnsSQL counts a series once rather than once per occurrence:
// event_count is the standalone events plus the series, which is what a
// reader means by "how much is going on here".
//
// next_event_at reads the clock in SQL rather than taking a bind, because a
// bind here shares an argument list with a count query that does not
// contain this subquery — see the Postgres sibling.
const gatheringColumnsSQL = `
SELECT gt.id, gt.slug, gt.origin_domain, gt.name, gt.blurb, gt.description, gt.publish_events, gt.created_at, gt.updated_at,
       (SELECT count(*) FROM gathering_members m WHERE m.gathering_id = gt.id),
       (SELECT count(*) FROM events e WHERE e.gathering_id = gt.id AND e.series_id IS NULL)
         + (SELECT count(*) FROM event_series es WHERE es.gathering_id = gt.id),
       (SELECT min(e.starts_at) FROM events e WHERE e.gathering_id = gt.id
          AND e.starts_at > strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
FROM gatherings gt`

func scanGathering(row rowScanner) (*store.Gathering, error) {
	var g store.Gathering
	var createdAt, updatedAt string
	var nextEventAt, publish *string
	err := row.Scan(&g.ID, &g.Slug, &g.OriginDomain, &g.Name, &g.Blurb, &g.Description,
		&publish, &createdAt, &updatedAt, &g.MemberCount, &g.EventCount, &nextEventAt)
	if err != nil {
		return nil, notFoundIfNoRows(err, "scanning gathering")
	}
	if publish != nil {
		g.PublishEvents = store.PublishSetting(*publish)
	}
	if g.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, err
	}
	if g.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return nil, err
	}
	if g.NextEventAt, err = parseOptionalTime(nextEventAt); err != nil {
		return nil, err
	}
	return &g, nil
}

// ownerValues splits an OwnerRef into the two nullable columns the
// gathering_owners CHECK insists on exactly one of.
func ownerValues(o store.OwnerRef) (userID, organizationID *string) {
	id := o.ID
	if o.Kind == store.OwnerKindOrganization {
		return nil, &id
	}
	return &id, nil
}

func (s *Store) CreateGathering(ctx context.Context, in store.GatheringInput, firstOwner store.OwnerRef) (*store.Gathering, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning gathering transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has run

	id := store.NewID()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO gatherings (id, slug, origin_domain, name, blurb, description, search_text, publish_events)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, in.Slug, in.OriginDomain, in.Name, in.Blurb, in.Description, in.SearchText,
		nullablePublish(in.PublishEvents)); err != nil {
		return nil, fmt.Errorf("creating gathering: %w", err)
	}
	userID, organizationID := ownerValues(firstOwner)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO gathering_owners (id, gathering_id, user_id, organization_id) VALUES (?, ?, ?, ?)`,
		store.NewID(), id, userID, organizationID); err != nil {
		return nil, fmt.Errorf("creating gathering owner: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing gathering: %w", err)
	}
	return s.GatheringByID(ctx, id)
}

func (s *Store) UpdateGathering(ctx context.Context, id string, in store.GatheringInput) (*store.Gathering, error) {
	res, err := s.db.ExecContext(ctx, `
UPDATE gatherings SET name = ?, blurb = ?, description = ?, search_text = ?,
                     publish_events = ?, updated_at = `+nowSQL+`
WHERE id = ?`, in.Name, in.Blurb, in.Description, in.SearchText,
		nullablePublish(in.PublishEvents), id)
	if err != nil {
		return nil, fmt.Errorf("updating gathering: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("updating gathering: %w", err)
	}
	if affected == 0 {
		return nil, store.ErrNotFound
	}
	return s.GatheringByID(ctx, id)
}

func (s *Store) GatheringByID(ctx context.Context, id string) (*store.Gathering, error) {
	g, err := scanGathering(s.db.QueryRowContext(ctx, gatheringColumnsSQL+` WHERE gt.id = ?`, id))
	if err != nil {
		return nil, err
	}
	owners, err := s.loadOwners(ctx, []string{id})
	if err != nil {
		return nil, err
	}
	g.Owners = owners[id]
	return g, nil
}

// GatheringBySlug does not load owners: its only caller is minting a slug,
// which asks whether the address is free and nothing else.
func (s *Store) GatheringBySlug(ctx context.Context, originDomain, slug string) (*store.Gathering, error) {
	return scanGathering(s.db.QueryRowContext(ctx,
		gatheringColumnsSQL+` WHERE gt.origin_domain = ? AND gt.slug = ?`,
		originDomain, slug))
}

func (s *Store) ListGatherings(ctx context.Context, f store.GatheringFilter) ([]store.Gathering, int64, error) {
	a := &args{}
	where := []string{}
	if f.Query != "" {
		where = append(where, "gt.search_text LIKE "+a.next(like(f.Query))+` ESCAPE '\'`)
	}
	if f.MemberID != "" {
		// "Mine" means joined OR owned, and owned means directly or through
		// an organization whose roster names the caller as an owner. A gathering
		// somebody owns but never joined is still theirs.
		where = append(where, `(
    EXISTS (SELECT 1 FROM gathering_members m WHERE m.gathering_id = gt.id AND m.user_id = `+a.next(f.MemberID)+`)
 OR EXISTS (SELECT 1 FROM gathering_owners o WHERE o.gathering_id = gt.id AND o.user_id = `+a.next(f.MemberID)+`)
 OR EXISTS (SELECT 1 FROM gathering_owners o
              JOIN organization_members gm ON gm.organization_id = o.organization_id
            WHERE o.gathering_id = gt.id AND gm.user_id = `+a.next(f.MemberID)+` AND gm.role = 'owner'))`)
	}
	if f.OwnedByOrganization != "" {
		where = append(where,
			"EXISTS (SELECT 1 FROM gathering_owners o WHERE o.gathering_id = gt.id AND o.organization_id = "+
				a.next(f.OwnedByOrganization)+")")
	}
	if place := f.Place; place.Locality != "" || place.Region != "" || place.Country != "" || place.Box != nil {
		// A gathering has no place of its own. It matches a place when any
		// event under it does.
		inner := []string{"e.gathering_id = gt.id"}
		placePredicates(&inner, a, "e", place)
		where = append(where, "EXISTS (SELECT 1 FROM events e WHERE "+joinAnd(inner)+")")
	}
	clause := whereClause(where)

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM gatherings gt`+clause, a.vals...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting gatherings: %w", err)
	}

	limit, offset := clamp(f.Page)
	rows, err := s.db.QueryContext(ctx, gatheringColumnsSQL+clause+
		` ORDER BY gt.created_at DESC, gt.id DESC LIMIT `+a.next(limit)+` OFFSET `+a.next(offset), a.vals...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing gatherings: %w", err)
	}
	defer rows.Close()

	gatherings := []store.Gathering{}
	ids := []string{}
	for rows.Next() {
		g, err := scanGathering(rows)
		if err != nil {
			return nil, 0, err
		}
		gatherings = append(gatherings, *g)
		ids = append(ids, g.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	// One owners query for the whole page rather than one per row.
	if len(ids) > 0 {
		owners, err := s.loadOwners(ctx, ids)
		if err != nil {
			return nil, 0, err
		}
		for i := range gatherings {
			gatherings[i].Owners = owners[gatherings[i].ID]
		}
	}
	return gatherings, total, nil
}

// loadOwners reads the owners of every gathering in ids at once. The two
// halves of the UNION are the two kinds of owner; each supplies the display
// fields from its own table, so the caller gets one uniform OwnerRef either
// way.
func (s *Store) loadOwners(ctx context.Context, ids []string) (map[string][]store.OwnerRef, error) {
	a := &args{}
	query := `
SELECT o.gathering_id, 'user' AS kind, u.id, u.display_name, u.handle, u.linkkeys_domain
FROM gathering_owners o JOIN users u ON u.id = o.user_id
WHERE o.gathering_id IN ` + a.list(ids) + `
UNION ALL
SELECT o.gathering_id, 'organization' AS kind, g.id, g.name, g.slug, g.origin_domain
FROM gathering_owners o JOIN organizations g ON g.id = o.organization_id
WHERE o.gathering_id IN ` + a.list(ids) + `
ORDER BY 2, 5`
	rows, err := s.db.QueryContext(ctx, query, a.vals...)
	if err != nil {
		return nil, fmt.Errorf("loading gathering owners: %w", err)
	}
	defer rows.Close()

	owners := map[string][]store.OwnerRef{}
	for rows.Next() {
		var gatheringID, kind string
		var o store.OwnerRef
		if err := rows.Scan(&gatheringID, &kind, &o.ID, &o.DisplayName, &o.Handle, &o.OriginDomain); err != nil {
			return nil, fmt.Errorf("scanning gathering owner: %w", err)
		}
		o.Kind = store.OwnerKind(kind)
		owners[gatheringID] = append(owners[gatheringID], o)
	}
	return owners, rows.Err()
}

func (s *Store) DeleteGathering(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM gatherings WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting gathering: %w", err)
	}
	return nil
}

// AddGatheringOwner is idempotent through the two partial unique indexes:
// whichever of the two applies, the conflict target names the column that
// is actually set.
func (s *Store) AddGatheringOwner(ctx context.Context, gatheringID string, owner store.OwnerRef) error {
	userID, organizationID := ownerValues(owner)
	conflict := `(gathering_id, user_id) WHERE user_id IS NOT NULL`
	if owner.Kind == store.OwnerKindOrganization {
		conflict = `(gathering_id, organization_id) WHERE organization_id IS NOT NULL`
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO gathering_owners (id, gathering_id, user_id, organization_id) VALUES (?, ?, ?, ?)
ON CONFLICT `+conflict+` DO NOTHING`, store.NewID(), gatheringID, userID, organizationID)
	if err != nil {
		return fmt.Errorf("adding gathering owner: %w", err)
	}
	return nil
}

func (s *Store) RemoveGatheringOwner(ctx context.Context, gatheringID string, owner store.OwnerRef) error {
	column := "user_id"
	if owner.Kind == store.OwnerKindOrganization {
		column = "organization_id"
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM gathering_owners WHERE gathering_id = ? AND `+column+` = ?`,
		gatheringID, owner.ID); err != nil {
		return fmt.Errorf("removing gathering owner: %w", err)
	}
	return nil
}

func (s *Store) CountGatheringOwners(ctx context.Context, gatheringID string) (int64, error) {
	var n int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM gathering_owners WHERE gathering_id = ?`, gatheringID).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting gathering owners: %w", err)
	}
	return n, nil
}

func (s *Store) JoinGathering(ctx context.Context, gatheringID, userID string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO gathering_members (gathering_id, user_id) VALUES (?, ?)
ON CONFLICT (gathering_id, user_id) DO NOTHING`, gatheringID, userID)
	if err != nil {
		return fmt.Errorf("joining gathering: %w", err)
	}
	return nil
}

func (s *Store) LeaveGathering(ctx context.Context, gatheringID, userID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM gathering_members WHERE gathering_id = ? AND user_id = ?`,
		gatheringID, userID); err != nil {
		return fmt.Errorf("leaving gathering: %w", err)
	}
	return nil
}

func (s *Store) ListGatheringMembers(ctx context.Context, gatheringID string, page store.Page) ([]store.GatheringMember, int64, error) {
	var total int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM gathering_members WHERE gathering_id = ?`, gatheringID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting gathering members: %w", err)
	}

	limit, offset := clamp(page)
	rows, err := s.db.QueryContext(ctx, `
SELECT m.gathering_id, m.user_id, u.handle, u.linkkeys_domain, u.display_name, m.joined_at
FROM gathering_members m JOIN users u ON u.id = m.user_id
WHERE m.gathering_id = ?
ORDER BY m.joined_at, u.handle
LIMIT ? OFFSET ?`, gatheringID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing gathering members: %w", err)
	}
	defer rows.Close()

	members := []store.GatheringMember{}
	for rows.Next() {
		var m store.GatheringMember
		var joinedAt string
		if err := rows.Scan(&m.GatheringID, &m.UserID, &m.Handle, &m.LinkkeysDomain,
			&m.DisplayName, &joinedAt); err != nil {
			return nil, 0, fmt.Errorf("scanning gathering member: %w", err)
		}
		var err error
		if m.JoinedAt, err = parseTime(joinedAt); err != nil {
			return nil, 0, err
		}
		members = append(members, m)
	}
	return members, total, rows.Err()
}

// GatheringAccessFor answers membership and ownership together in one round
// trip. Ownership is two separate reachability questions — direct
// individual, and owner-of-an-owning-organization — and asking them separately
// would be three round trips for one permission check.
func (s *Store) GatheringAccessFor(ctx context.Context, gatheringID, userID string) (store.GatheringAccess, error) {
	var access store.GatheringAccess
	if userID == "" {
		return access, nil
	}
	err := s.db.QueryRowContext(ctx, `
SELECT
  EXISTS (SELECT 1 FROM gathering_members m WHERE m.gathering_id = ? AND m.user_id = ?),
  EXISTS (SELECT 1 FROM gathering_owners o WHERE o.gathering_id = ? AND o.user_id = ?)
    OR EXISTS (SELECT 1 FROM gathering_owners o
                 JOIN organization_members gm ON gm.organization_id = o.organization_id
               WHERE o.gathering_id = ? AND gm.user_id = ? AND gm.role = 'owner')`,
		gatheringID, userID, gatheringID, userID, gatheringID, userID).Scan(&access.IsMember, &access.IsOwner)
	if err != nil {
		return access, fmt.Errorf("resolving gathering access: %w", err)
	}
	return access, nil
}
