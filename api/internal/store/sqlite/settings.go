package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

// InstanceSettings reads what is stored over the defaults, so an instance
// that has set nothing still has behaviour and no caller has to know the
// same defaults twice.
func (s *Store) InstanceSettings(ctx context.Context) (store.InstanceSettings, error) {
	settings := store.DefaultInstanceSettings()

	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM instance_settings`)
	if err != nil {
		return settings, fmt.Errorf("reading instance settings: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return settings, fmt.Errorf("scanning an instance setting: %w", err)
		}
		store.ApplySetting(&settings, key, value)
	}
	return settings, rows.Err()
}

func (s *Store) PutInstanceSettings(ctx context.Context, in store.InstanceSettings) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning settings transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has run

	for _, row := range store.SettingRows(in) {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO instance_settings (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = `+nowSQL+``,
			row[0], row[1]); err != nil {
			return fmt.Errorf("writing instance setting %s: %w", row[0], err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing instance settings: %w", err)
	}
	return nil
}

// The allowance arithmetic, as SQL expressions over the row's OWN columns.
// See the Postgres sibling for why a single statement is the requirement.
//
// NUMBERED placeholders (?1, ?2) rather than plain `?`. An expression that
// names the same bind four times would otherwise need it passed four times,
// and one miscount is a silently wrong limit.
const (
	peerUsed = `(CASE WHEN rate_window_start IS NULL OR rate_window_start < ?1
	                  THEN 0 ELSE rate_window_count END)`
	peerNewCount = `MIN(` + peerUsed + ` + ?2, MAX(?3, ` + peerUsed + `))`
	peerAllowed  = `(` + peerNewCount + ` - ` + peerUsed + `)`
)

// ConsumePeerAllowance takes up to `wanted` from this peer's budget for the
// minute containing `now`. One statement, so two callers cannot both see an
// empty window and both fill it.
func (s *Store) ConsumePeerAllowance(ctx context.Context, peerID string, wanted, limit int64, now time.Time) (store.RateVerdict, error) {
	if limit <= 0 || wanted <= 0 {
		return store.RateVerdict{Allowed: wanted}, nil
	}
	windowStart := formatTime(now.UTC().Truncate(time.Minute))

	var allowed int64
	err := s.db.QueryRowContext(ctx, `
UPDATE federation_peers
SET rate_window_start  = CASE WHEN rate_window_start IS NULL OR rate_window_start < ?1
                              THEN ?1 ELSE rate_window_start END,
    rate_limited_total = rate_limited_total + ?2 - `+peerAllowed+`,
    rate_last_allowed  = `+peerAllowed+`,
    rate_window_count  = `+peerNewCount+`
WHERE id = ?4
RETURNING rate_last_allowed`,
		windowStart, wanted, limit, peerID).Scan(&allowed)
	if err != nil {
		return store.RateVerdict{}, fmt.Errorf("taking the peer's allowance: %w", err)
	}
	return store.RateVerdict{Allowed: allowed, Refused: wanted - allowed}, nil
}

func (s *Store) SetPeerRateLimit(ctx context.Context, peerID string, limit *int64) (*store.Peer, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE federation_peers SET rate_limit_per_minute = ?, updated_at = `+nowSQL+`
		 WHERE id = ?`, limit, peerID)
	if err != nil {
		return nil, fmt.Errorf("setting the peer's rate limit: %w", err)
	}
	if affected, err := res.RowsAffected(); err != nil || affected == 0 {
		if err != nil {
			return nil, fmt.Errorf("setting the peer's rate limit: %w", err)
		}
		return nil, store.ErrNotFound
	}
	return s.PeerByID(ctx, peerID)
}

// DeleteRemoteEventsEndedBefore is the retention sweep. It drops on
// ends_at, not received_at: what matters is that the event is over, not
// when this instance heard about it.
func (s *Store) DeleteRemoteEventsEndedBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM remote_events WHERE ends_at < ?`, formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("dropping expired remote events: %w", err)
	}
	return res.RowsAffected()
}

// RecordOriginAccepted counts one accepted event against its originating
// organization.
//
// It does NOT touch the minute window: ConsumeOriginAllowance owns that,
// because the window is the limiter's state. This owns only the running
// totals. Both writing it was how the window came to be counted twice.
func (s *Store) RecordOriginAccepted(ctx context.Context, peerID, organizationName string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO federation_origin_stats
       (peer_id, organization_name, accepted_total, last_received_at)
VALUES (?, ?, 1, ?)
ON CONFLICT (peer_id, organization_name) DO UPDATE SET
  accepted_total   = federation_origin_stats.accepted_total + 1,
  last_received_at = ?`,
		// A `?` carries no position, so the instant is bound once per
		// appearance.
		peerID, organizationName, formatTime(now), formatTime(now))
	if err != nil {
		return fmt.Errorf("counting an accepted event: %w", err)
	}
	return nil
}

// originVolumeSQL joins the peer's throttle state onto each origin. The
// first bind is the current minute, so a window that has rolled over reads
// as zero rather than as a stale count.
const originVolumeSQL = `
SELECT o.peer_id, p.address, o.organization_name,
       (SELECT count(*) FROM remote_events r
         WHERE r.peer_id = o.peer_id AND r.organization_name = o.organization_name),
       o.accepted_total,
       CASE WHEN o.window_start IS NULL OR o.window_start < ? THEN 0 ELSE o.window_count END,
       o.last_received_at,
       COALESCE(p.rate_limit_per_minute, 0), p.rate_limited_total,
       p.suspended_at IS NOT NULL,
       o.rate_limit_per_minute, COALESCE(o.rate_limit_per_minute, ?), o.rate_limited_total
FROM federation_origin_stats o JOIN federation_peers p ON p.id = o.peer_id`

func (s *Store) ListOriginVolume(ctx context.Context, peerID string, defaultLimit int64, now time.Time, page store.Page) ([]store.OriginVolume, int64, error) {
	a := &args{}
	a.nextTime(now.UTC().Truncate(time.Minute))
	a.next(defaultLimit)
	where := []string{}
	if peerID != "" {
		where = append(where, "o.peer_id = "+a.next(peerID))
	}
	clause := whereClause(where)

	countArgs := []any{}
	countClause := ""
	if peerID != "" {
		countArgs = append(countArgs, peerID)
		countClause = " WHERE o.peer_id = ?"
	}
	var total int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM federation_origin_stats o`+countClause, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting origins: %w", err)
	}

	limit, offset := clamp(page)
	rows, err := s.db.QueryContext(ctx, originVolumeSQL+clause+
		` ORDER BY o.accepted_total DESC, o.organization_name LIMIT `+a.next(limit)+
		` OFFSET `+a.next(offset), a.vals...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing origins: %w", err)
	}
	defer rows.Close()

	origins := []store.OriginVolume{}
	for rows.Next() {
		var o store.OriginVolume
		var lastReceived *string
		if err := rows.Scan(&o.PeerID, &o.PeerAddress, &o.OrganizationName, &o.Held,
			&o.AcceptedTotal, &o.AcceptedThisMinute, &lastReceived,
			&o.PeerRateLimitPerMinute, &o.PeerRateLimitedTotal, &o.PeerSuspended,
			&o.RateLimitPerMinute, &o.EffectiveRateLimitPerMinute, &o.RateLimitedTotal); err != nil {
			return nil, 0, fmt.Errorf("scanning an origin: %w", err)
		}
		var err error
		if o.LastReceivedAt, err = parseOptionalTime(lastReceived); err != nil {
			return nil, 0, err
		}
		origins = append(origins, o)
	}
	return origins, total, rows.Err()
}

// The per-origin allowance, in the same single-statement shape as the
// peer's. Numbered placeholders for the same reason. The limit is resolved in SQL because it lives on the row: an
// origin's own override wins over the instance default, and a resolved
// limit of zero or less means no limit at all.
const (
	originLimit = `(CASE WHEN COALESCE(federation_origin_stats.rate_limit_per_minute, ?5) <= 0
	                     THEN 2147483647
	                     ELSE COALESCE(federation_origin_stats.rate_limit_per_minute, ?5) END)`
	originUsed = `(CASE WHEN federation_origin_stats.window_start IS NULL
	                      OR federation_origin_stats.window_start < ?3
	                    THEN 0 ELSE federation_origin_stats.window_count END)`
	originNewCount = `MIN(` + originUsed + ` + ?4, MAX(` + originLimit + `, ` + originUsed + `))`
	originAllowed  = `(` + originNewCount + ` - ` + originUsed + `)`
	// The insert branch: no row yet, so nothing is used and only the
	// instance default can apply.
	originFirstAllowed = `MIN(?4, CASE WHEN ?5 <= 0 THEN 2147483647 ELSE ?5 END)`
)

// ConsumeOriginAllowance takes from ONE organization's budget inside one
// peer. The row is created if it does not exist: an organization's first
// delivery must be counted against a budget like any other.
func (s *Store) ConsumeOriginAllowance(ctx context.Context, peerID, organizationName string, wanted, defaultLimit int64, now time.Time) (store.RateVerdict, error) {
	if wanted <= 0 {
		return store.RateVerdict{}, nil
	}
	windowStart := formatTime(now.UTC().Truncate(time.Minute))

	var allowed int64
	err := s.db.QueryRowContext(ctx, `
INSERT INTO federation_origin_stats
       (peer_id, organization_name, window_start, window_count, rate_last_allowed,
        rate_limited_total, last_received_at)
VALUES (?1, ?2, ?3, `+originFirstAllowed+`, `+originFirstAllowed+`,
        ?4 - (`+originFirstAllowed+`), ?6)
ON CONFLICT (peer_id, organization_name) DO UPDATE SET
  window_start       = CASE WHEN federation_origin_stats.window_start IS NULL
                              OR federation_origin_stats.window_start < ?3
                            THEN ?3 ELSE federation_origin_stats.window_start END,
  rate_limited_total = federation_origin_stats.rate_limited_total + ?4 - `+originAllowed+`,
  rate_last_allowed  = `+originAllowed+`,
  window_count       = `+originNewCount+`
RETURNING rate_last_allowed`,
		peerID, organizationName, windowStart, wanted, defaultLimit, formatTime(now)).Scan(&allowed)
	if err != nil {
		return store.RateVerdict{}, fmt.Errorf("taking the origin's allowance: %w", err)
	}
	return store.RateVerdict{Allowed: allowed, Refused: wanted - allowed}, nil
}

func (s *Store) SetOriginRateLimit(ctx context.Context, peerID, organizationName string, limit *int64, defaultLimit int64, now time.Time) (*store.OriginVolume, error) {
	res, err := s.db.ExecContext(ctx, `
UPDATE federation_origin_stats SET rate_limit_per_minute = ?
WHERE peer_id = ? AND organization_name = ?`, limit, peerID, organizationName)
	if err != nil {
		return nil, fmt.Errorf("setting the origin's rate limit: %w", err)
	}
	if affected, err := res.RowsAffected(); err != nil || affected == 0 {
		if err != nil {
			return nil, fmt.Errorf("setting the origin's rate limit: %w", err)
		}
		return nil, store.ErrNotFound
	}
	// Read the row back by PAGING, not by asking for one page and hoping.
	// The listing is ordered by volume and clamped to maxLimit rows, so a
	// peer carrying more origins than that would answer ErrNotFound for a
	// quiet origin whose limit was just committed — telling an operator the
	// change failed after it succeeded.
	for offset := int64(0); ; offset += maxLimit {
		origins, _, err := s.ListOriginVolume(ctx, peerID, defaultLimit, now,
			store.Page{Limit: maxLimit, Offset: offset})
		if err != nil {
			return nil, err
		}
		if len(origins) == 0 {
			return nil, store.ErrNotFound
		}
		for i := range origins {
			if origins[i].OrganizationName == organizationName {
				return &origins[i], nil
			}
		}
	}
}
