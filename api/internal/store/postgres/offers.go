package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

// offerColumnsSQL joins the three records every screen shows together: the
// gathering being offered, the organization being offered it, and the person
// who offered. A directory spans domains, so the offerer's domain travels
// with their handle — a name on its own is not an identity.
const offerColumnsSQL = `
SELECT o.id, o.gathering_id, gt.name, o.organization_id, org.name,
       o.offered_by, u.handle, u.display_name, u.linkkeys_domain,
       o.note, o.status, o.created_at, o.resolved_at
FROM gathering_offers o
JOIN gatherings gt   ON gt.id  = o.gathering_id
JOIN organizations org ON org.id = o.organization_id
JOIN users u         ON u.id   = o.offered_by`

func scanOffer(row rowScanner) (*store.GatheringOffer, error) {
	var o store.GatheringOffer
	err := row.Scan(&o.ID, &o.GatheringID, &o.GatheringName, &o.OrganizationID, &o.OrganizationName,
		&o.OfferedByID, &o.OfferedByHandle, &o.OfferedByName, &o.OfferedByDomain,
		&o.Note, &o.Status, &o.CreatedAt, &o.ResolvedAt)
	if err != nil {
		return nil, notFoundIfNoRows(err, "scanning gathering offer")
	}
	return &o, nil
}

func (s *Store) CreateGatheringOffer(ctx context.Context, gatheringID, organizationID, offeredBy, note string, now time.Time) (*store.GatheringOffer, error) {
	// A pending offer for this pair already standing is the answer, not an
	// error: the offerer's intent is already recorded, and a second row
	// would give the receiving side two things to answer and one outcome.
	// The partial unique index is what makes this safe under a race; this
	// query is the fast path that avoids relying on the error.
	var id string
	err := s.db.QueryRowContext(ctx, `
SELECT id FROM gathering_offers
WHERE gathering_id = $1 AND organization_id = $2 AND status = 'pending'`,
		gatheringID, organizationID).Scan(&id)
	switch {
	case err == nil:
		return s.GatheringOfferByID(ctx, id)
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("looking for a pending offer: %w", err)
	}

	id = store.NewID()
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO gathering_offers (id, gathering_id, organization_id, offered_by, note, status, created_at)
VALUES ($1, $2, $3, $4, $5, 'pending', $6)`,
		id, gatheringID, organizationID, offeredBy, note, now.UTC()); err != nil {
		return nil, fmt.Errorf("creating gathering offer: %w", err)
	}
	return s.GatheringOfferByID(ctx, id)
}

func (s *Store) GatheringOfferByID(ctx context.Context, id string) (*store.GatheringOffer, error) {
	return scanOffer(s.db.QueryRowContext(ctx, offerColumnsSQL+` WHERE o.id = $1`, id))
}

func (s *Store) ListGatheringOffers(ctx context.Context, f store.GatheringOfferFilter) ([]store.GatheringOffer, int64, error) {
	where := []string{"1 = 1"}
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}

	if f.OrganizationID != "" {
		add("o.organization_id = $%d", f.OrganizationID)
	}
	if f.GatheringID != "" {
		add("o.gathering_id = $%d", f.GatheringID)
	}
	if !f.IncludeResolved {
		where = append(where, "o.status = 'pending'")
	}
	if f.ViewerID != "" {
		// A side in it: they offered it, they own the gathering, or they own
		// the organization. Ownership of the gathering reaches through an
		// organization the same way GatheringAccessFor resolves it.
		args = append(args, f.ViewerID)
		n := len(args)
		where = append(where, fmt.Sprintf(`(
  o.offered_by = $%[1]d
  OR EXISTS (SELECT 1 FROM organization_members om
              WHERE om.organization_id = o.organization_id AND om.user_id = $%[1]d AND om.role = 'owner')
  OR EXISTS (SELECT 1 FROM gathering_owners go2
              WHERE go2.gathering_id = o.gathering_id
                AND (go2.user_id = $%[1]d
                     OR go2.organization_id IN (SELECT om2.organization_id FROM organization_members om2
                                                 WHERE om2.user_id = $%[1]d AND om2.role = 'owner')))
)`, n))
	}

	clause := " WHERE " + strings.Join(where, " AND ")
	var total int64
	if err := s.db.QueryRowContext(ctx, `
SELECT count(*) FROM gathering_offers o`+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting gathering offers: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, offerColumnsSQL+clause+" ORDER BY o.created_at DESC", args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing gathering offers: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only

	offers := []store.GatheringOffer{}
	for rows.Next() {
		o, err := scanOffer(rows)
		if err != nil {
			return nil, 0, err
		}
		offers = append(offers, *o)
	}
	return offers, total, rows.Err()
}

func (s *Store) ResolveGatheringOffer(ctx context.Context, id string, status store.GatheringOfferStatus, now time.Time) (bool, error) {
	// The status check is IN the update, so two people answering the same
	// offer at the same moment produce one resolution and one refusal
	// rather than two writes and a coin toss.
	result, err := s.db.ExecContext(ctx, `
UPDATE gathering_offers SET status = $2, resolved_at = $3
WHERE id = $1 AND status = 'pending'`, id, string(status), now.UTC())
	if err != nil {
		return false, fmt.Errorf("resolving gathering offer: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("resolving gathering offer: %w", err)
	}
	return changed > 0, nil
}
