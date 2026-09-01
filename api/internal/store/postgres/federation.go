package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

// peerColumnsSQL joins the queue depth in, because every screen that lists
// a peer wants to know what is waiting for it.
const peerColumnsSQL = `
SELECT p.id, p.address, p.handle, p.domain,
       p.subject_user_id, p.subject_domain, p.application_id, p.instance_id,
       p.base_url, p.inbound_status, p.outbound_status, p.note,
       p.suspended_at, p.first_failure_at, p.last_failure_at, p.last_failure_reason,
       p.last_success_at, p.rate_limit_per_minute, p.rate_limited_total,
       p.created_at, p.updated_at,
       (SELECT count(*) FROM federation_outbox o WHERE o.peer_id = p.id)
FROM federation_peers p`

func scanPeer(row rowScanner) (*store.Peer, error) {
	var p store.Peer
	var inbound, outbound string
	err := row.Scan(&p.ID, &p.Address, &p.Handle, &p.Domain,
		&p.Identity.SubjectUserID, &p.Identity.SubjectDomain, &p.Identity.ApplicationID, &p.Identity.InstanceID,
		&p.BaseURL, &inbound, &outbound, &p.Note,
		&p.SuspendedAt, &p.FirstFailureAt, &p.LastFailureAt, &p.LastFailureReason,
		&p.LastSuccessAt, &p.RateLimitPerMinute, &p.RateLimitedTotal,
		&p.CreatedAt, &p.UpdatedAt, &p.PendingDeliveries)
	if err != nil {
		return nil, notFoundIfNoRows(err, "scanning peer")
	}
	p.InboundStatus = store.PeerStatus(inbound)
	p.OutboundStatus = store.PeerStatus(outbound)
	return &p, nil
}

func (s *Store) CreatePeer(ctx context.Context, in store.PeerInput) (*store.Peer, error) {
	id := store.NewID()
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO federation_peers (id, address, handle, domain, subject_user_id, subject_domain, application_id, instance_id, base_url, note)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		id, in.Address, in.Handle, in.Domain,
		in.Identity.SubjectUserID, in.Identity.SubjectDomain, in.Identity.ApplicationID, in.Identity.InstanceID,
		in.BaseURL, in.Note); err != nil {
		return nil, fmt.Errorf("creating peer: %w", err)
	}
	return s.PeerByID(ctx, id)
}

func (s *Store) PeerByID(ctx context.Context, id string) (*store.Peer, error) {
	return scanPeer(s.db.QueryRowContext(ctx, peerColumnsSQL+` WHERE p.id = $1`, id))
}

func (s *Store) PeerByAddress(ctx context.Context, address string) (*store.Peer, error) {
	return scanPeer(s.db.QueryRowContext(ctx, peerColumnsSQL+` WHERE p.address = $1`, address))
}

func (s *Store) ListPeers(ctx context.Context, page store.Page) ([]store.Peer, int64, error) {
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM federation_peers`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting peers: %w", err)
	}
	limit, offset := clamp(page)
	rows, err := s.db.QueryContext(ctx, peerColumnsSQL+` ORDER BY p.address LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing peers: %w", err)
	}
	defer rows.Close()
	return collectPeers(rows, total)
}

func (s *Store) UpdatePeer(ctx context.Context, id string, in store.PeerInput) (*store.Peer, error) {
	res, err := s.db.ExecContext(ctx, `
UPDATE federation_peers SET base_url = $1, note = $2,
                            updated_at = now()
WHERE id = $3`, in.BaseURL, in.Note, id)
	if err != nil {
		return nil, fmt.Errorf("updating peer: %w", err)
	}
	if affected, err := res.RowsAffected(); err != nil || affected == 0 {
		if err != nil {
			return nil, fmt.Errorf("updating peer: %w", err)
		}
		return nil, store.ErrNotFound
	}
	return s.PeerByID(ctx, id)
}

// SetPeerStatus uses COALESCE so a nil status argument leaves that
// direction as it is. Approving one direction must never move the other.
// identity, when non-nil, replaces the peer's stored canonical identity —
// all four columns together, never partially.
func (s *Store) SetPeerStatus(ctx context.Context, id string, inbound, outbound *store.PeerStatus, identity *store.PeerIdentity) (*store.Peer, error) {
	var in, out *string
	if inbound != nil {
		v := string(*inbound)
		in = &v
	}
	if outbound != nil {
		v := string(*outbound)
		out = &v
	}
	var subjectUserID, subjectDomain, applicationID, instanceID *string
	if identity != nil {
		subjectUserID, subjectDomain = &identity.SubjectUserID, &identity.SubjectDomain
		applicationID, instanceID = &identity.ApplicationID, &identity.InstanceID
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE federation_peers
SET inbound_status  = COALESCE($1, inbound_status),
    outbound_status = COALESCE($2, outbound_status),
    subject_user_id = COALESCE($3, subject_user_id),
    subject_domain  = COALESCE($4, subject_domain),
    application_id  = COALESCE($5, application_id),
    instance_id     = COALESCE($6, instance_id),
    updated_at      = now()
WHERE id = $7`, in, out, subjectUserID, subjectDomain, applicationID, instanceID, id)
	if err != nil {
		return nil, fmt.Errorf("setting peer status: %w", err)
	}
	if affected, err := res.RowsAffected(); err != nil || affected == 0 {
		if err != nil {
			return nil, fmt.Errorf("setting peer status: %w", err)
		}
		return nil, store.ErrNotFound
	}
	return s.PeerByID(ctx, id)
}

func (s *Store) DeletePeer(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM federation_peers WHERE id = $1`, id); err != nil {
		return fmt.Errorf("deleting peer: %w", err)
	}
	return nil
}

func (s *Store) RecordDeliverySuccess(ctx context.Context, peerID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE federation_peers
SET last_success_at = $1, first_failure_at = NULL, last_failure_reason = '',
    updated_at = now()
WHERE id = $2`, at.UTC(), peerID)
	if err != nil {
		return fmt.Errorf("recording delivery success: %w", err)
	}
	return nil
}

// RecordDeliveryFailure opens a failure run if none is open, then suspends
// the peer when that run started at or before suspendIfStartedBefore. One
// comparison, rather than a counter whose meaning depends on how often the
// sender happened to run.
func (s *Store) RecordDeliveryFailure(ctx context.Context, peerID, reason string, at, suspendIfStartedBefore time.Time) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE federation_peers
SET first_failure_at    = COALESCE(first_failure_at, $1),
    last_failure_at     = $1,
    last_failure_reason = $2,
    suspended_at        = CASE
                            WHEN suspended_at IS NOT NULL THEN suspended_at
                            WHEN COALESCE(first_failure_at, $1) <= $3 THEN $1
                            ELSE NULL
                          END,
    updated_at          = now()
WHERE id = $4`, at.UTC(), reason, suspendIfStartedBefore.UTC(), peerID)
	if err != nil {
		return fmt.Errorf("recording delivery failure: %w", err)
	}
	return nil
}

func (s *Store) ResumePeer(ctx context.Context, peerID string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning resume transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has run

	if _, err := tx.ExecContext(ctx, `
UPDATE federation_peers
SET suspended_at = NULL, first_failure_at = NULL, last_failure_reason = '',
    updated_at = now()
WHERE id = $1`, peerID); err != nil {
		return fmt.Errorf("resuming peer: %w", err)
	}
	// Everything waiting becomes due now, and its attempt count resets —
	// otherwise the backoff earned while the peer was broken would keep
	// delaying the first delivery after it was fixed.
	if _, err := tx.ExecContext(ctx,
		`UPDATE federation_outbox SET next_attempt_at = $1, attempts = 0 WHERE peer_id = $2`,
		now.UTC(), peerID); err != nil {
		return fmt.Errorf("requeueing deliveries: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing resume: %w", err)
	}
	return nil
}

// EnqueueDelivery replaces whatever was queued for this (peer, event). The
// unique index is what makes that one statement instead of a read and a
// branch.
func (s *Store) EnqueueDelivery(ctx context.Context, peerID, eventID string, payload []byte, dueAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO federation_outbox (id, peer_id, event_id, payload, next_attempt_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (peer_id, event_id) DO UPDATE
SET payload = EXCLUDED.payload, next_attempt_at = EXCLUDED.next_attempt_at,
    attempts = 0, last_error = ''`,
		store.NewID(), peerID, eventID, payload, dueAt.UTC())
	if err != nil {
		return fmt.Errorf("enqueueing delivery: %w", err)
	}
	return nil
}

const dueDeliveriesSQL = `
SELECT o.id, o.peer_id, p.address, p.base_url, o.event_id, o.payload,
       o.attempts, o.next_attempt_at, o.last_error, o.created_at
FROM federation_outbox o JOIN federation_peers p ON p.id = o.peer_id
WHERE o.next_attempt_at <= $1
  AND p.outbound_status = 'approved'
  AND p.suspended_at IS NULL
ORDER BY o.next_attempt_at
LIMIT $2`

func (s *Store) DueDeliveries(ctx context.Context, now time.Time, limit int) ([]store.OutboxItem, error) {
	rows, err := s.db.QueryContext(ctx, dueDeliveriesSQL, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("reading due deliveries: %w", err)
	}
	defer rows.Close()

	items := []store.OutboxItem{}
	for rows.Next() {
		var it store.OutboxItem
		if err := rows.Scan(&it.ID, &it.PeerID, &it.PeerAddress, &it.PeerBaseURL, &it.EventID,
			&it.Payload, &it.Attempts, &it.NextAttemptAt, &it.LastError, &it.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning due delivery: %w", err)
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

func (s *Store) MarkDelivered(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM federation_outbox WHERE id = $1`, id); err != nil {
		return fmt.Errorf("marking delivered: %w", err)
	}
	return nil
}

func (s *Store) MarkDeliveryFailed(ctx context.Context, id, reason string, nextAttemptAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE federation_outbox SET attempts = attempts + 1, last_error = $1, next_attempt_at = $2
WHERE id = $3`, reason, nextAttemptAt.UTC(), id)
	if err != nil {
		return fmt.Errorf("marking delivery failed: %w", err)
	}
	return nil
}

// DeferDelivery reschedules without counting an attempt — see the
// interface comment for why backpressure is not failure.
func (s *Store) DeferDelivery(ctx context.Context, id, reason string, nextAttemptAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE federation_outbox SET last_error = $1, next_attempt_at = $2 WHERE id = $3`,
		reason, nextAttemptAt.UTC(), id)
	if err != nil {
		return fmt.Errorf("deferring delivery: %w", err)
	}
	return nil
}

func (s *Store) OutboundPeers(ctx context.Context) ([]store.Peer, error) {
	rows, err := s.db.QueryContext(ctx, peerColumnsSQL+
		` WHERE p.outbound_status = 'approved' AND p.suspended_at IS NULL ORDER BY p.address`)
	if err != nil {
		return nil, fmt.Errorf("listing outbound peers: %w", err)
	}
	defer rows.Close()
	peers, _, err := collectPeers(rows, 0)
	return peers, err
}

func collectPeers(rows rowsScanner, total int64) ([]store.Peer, int64, error) {
	peers := []store.Peer{}
	for rows.Next() {
		peer, err := scanPeer(rows)
		if err != nil {
			return nil, 0, err
		}
		peers = append(peers, *peer)
	}
	return peers, total, rows.Err()
}

// ---- What peers sent ------------------------------------------------------

const remoteEventColumnsSQL = `
SELECT r.id, r.peer_id, p.address, r.remote_id, r.origin_domain, r.canonical_url, r.title,
       r.is_online, r.is_in_person,
       r.loc_name, r.loc_address, r.loc_locality, r.loc_region, r.loc_postal_code, r.loc_country,
       r.loc_latitude, r.loc_longitude,
       r.starts_at, r.ends_at, r.timezone, r.gathering_name, r.organization_name, r.received_at
FROM remote_events r JOIN federation_peers p ON p.id = r.peer_id`

func scanRemoteEvent(row rowScanner) (*store.RemoteEvent, error) {
	var e store.RemoteEvent
	err := row.Scan(&e.ID, &e.PeerID, &e.PeerAddress, &e.RemoteID, &e.OriginDomain,
		&e.CanonicalURL, &e.Title, &e.IsOnline, &e.IsInPerson,
		&e.Location.Name, &e.Location.Address, &e.Location.Locality, &e.Location.Region,
		&e.Location.PostalCode, &e.Location.Country, &e.Location.Latitude, &e.Location.Longitude,
		&e.StartsAt, &e.EndsAt, &e.Timezone, &e.GatheringName, &e.OrganizationName, &e.ReceivedAt)
	if err != nil {
		return nil, notFoundIfNoRows(err, "scanning remote event")
	}
	return &e, nil
}

func (s *Store) UpsertRemoteEvent(ctx context.Context, peerID string, in store.RemoteEventInput) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO remote_events (id, peer_id, remote_id, origin_domain, canonical_url, title, search_text,
  is_online, is_in_person, loc_name, loc_address, loc_locality, loc_region, loc_postal_code,
  loc_country, loc_latitude, loc_longitude, starts_at, ends_at, timezone,
  gathering_name, organization_name)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)
ON CONFLICT (peer_id, remote_id) DO UPDATE SET
  origin_domain = EXCLUDED.origin_domain, canonical_url = EXCLUDED.canonical_url,
  title = EXCLUDED.title, search_text = EXCLUDED.search_text,
  is_online = EXCLUDED.is_online, is_in_person = EXCLUDED.is_in_person,
  loc_name = EXCLUDED.loc_name, loc_address = EXCLUDED.loc_address,
  loc_locality = EXCLUDED.loc_locality, loc_region = EXCLUDED.loc_region,
  loc_postal_code = EXCLUDED.loc_postal_code, loc_country = EXCLUDED.loc_country,
  loc_latitude = EXCLUDED.loc_latitude, loc_longitude = EXCLUDED.loc_longitude,
  starts_at = EXCLUDED.starts_at, ends_at = EXCLUDED.ends_at, timezone = EXCLUDED.timezone,
  gathering_name = EXCLUDED.gathering_name, organization_name = EXCLUDED.organization_name,
  received_at = now()`,
		store.NewID(), peerID, in.RemoteID, in.OriginDomain, in.CanonicalURL, in.Title, in.SearchText,
		in.IsOnline, in.IsInPerson, in.Location.Name, in.Location.Address, in.Location.Locality,
		in.Location.Region, in.Location.PostalCode, in.Location.Country,
		in.Location.Latitude, in.Location.Longitude,
		in.StartsAt.UTC(), in.EndsAt.UTC(), in.Timezone, in.GatheringName, in.OrganizationName)
	if err != nil {
		return fmt.Errorf("storing remote event: %w", err)
	}
	return nil
}

func (s *Store) DeleteRemoteEvent(ctx context.Context, peerID, remoteID string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM remote_events WHERE peer_id = $1 AND remote_id = $2`, peerID, remoteID)
	if err != nil {
		return false, fmt.Errorf("deleting remote event: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("deleting remote event: %w", err)
	}
	return affected > 0, nil
}

func (s *Store) ListRemoteEvents(ctx context.Context, f store.RemoteEventFilter) ([]store.RemoteEvent, int64, error) {
	a := &args{}
	where := []string{}
	if f.Query != "" {
		where = append(where, "r.search_text LIKE "+a.next(like(f.Query))+` ESCAPE '\'`)
	}
	if !f.StartsAfter.IsZero() {
		where = append(where, "r.starts_at >= "+a.nextTime(f.StartsAfter))
	}
	if !f.StartsBefore.IsZero() {
		where = append(where, "r.starts_at <= "+a.nextTime(f.StartsBefore))
	}
	if !f.IncludeStarted {
		where = append(where, "r.starts_at > "+a.nextTime(f.Now))
	}
	if f.OnlineOnly {
		where = append(where, "r.is_online")
	}
	if f.InPersonOnly {
		where = append(where, "r.is_in_person")
	}
	placePredicates(&where, a, "r", f.Place)
	clause := whereClause(where)

	var total int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM remote_events r`+clause, a.vals...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting remote events: %w", err)
	}

	limit, offset := clamp(f.Page)
	rows, err := s.db.QueryContext(ctx, remoteEventColumnsSQL+clause+
		` ORDER BY r.starts_at, r.id LIMIT `+a.next(limit)+` OFFSET `+a.next(offset), a.vals...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing remote events: %w", err)
	}
	defer rows.Close()

	events := []store.RemoteEvent{}
	for rows.Next() {
		e, err := scanRemoteEvent(rows)
		if err != nil {
			return nil, 0, err
		}
		events = append(events, *e)
	}
	return events, total, rows.Err()
}

// RememberBatch records a batch and says whether it is new.
//
// The INSERT is the check. Asking "have I seen this?" and then inserting
// would let two copies of the same replayed envelope both pass the question
// before either wrote; a unique constraint cannot be raced.
func (s *Store) RememberBatch(ctx context.Context, peerID, batchID string, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
INSERT INTO federation_seen_batches (peer_id, batch_id, seen_at) VALUES ($1, $2, $3)
ON CONFLICT (peer_id, batch_id) DO NOTHING`, peerID, batchID, now.UTC())
	if err != nil {
		return false, fmt.Errorf("remembering a batch: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("remembering a batch: %w", err)
	}
	return affected > 0, nil
}

func (s *Store) ForgetBatch(ctx context.Context, peerID, batchID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM federation_seen_batches WHERE peer_id = $1 AND batch_id = $2`, peerID, batchID)
	if err != nil {
		return fmt.Errorf("forgetting a batch: %w", err)
	}
	return nil
}

func (s *Store) ForgetBatchesSeenBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM federation_seen_batches WHERE seen_at < $1`, cutoff.UTC())
	if err != nil {
		return 0, fmt.Errorf("forgetting old batches: %w", err)
	}
	return res.RowsAffected()
}
