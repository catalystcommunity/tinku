package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

const webhookColumnsSQL = `
SELECT id, owner_kind, owner_id, url, secret, scope, note, active, include_details,
       failure_count, last_status, last_attempt_at, created_at, updated_at
FROM webhooks`

func scanWebhook(row rowScanner) (*store.Webhook, error) {
	var w store.Webhook
	err := row.Scan(&w.ID, &w.OwnerKind, &w.OwnerID, &w.URL, &w.Secret, &w.Scope, &w.Note,
		&w.Active, &w.IncludeDetails, &w.FailureCount, &w.LastStatus, &w.LastAttemptAt, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return nil, notFoundIfNoRows(err, "scanning webhook")
	}
	return &w, nil
}

func (s *Store) CreateWebhook(ctx context.Context, w store.Webhook, limit int) (*store.Webhook, error) {
	// The count and the insert are one statement. Checked in the service
	// and again here because two callers can both read four and both write
	// a fifth; the service check is for the error message, this one is the
	// rule.
	id := store.NewID()
	result, err := s.db.ExecContext(ctx, `
INSERT INTO webhooks (id, owner_kind, owner_id, url, secret, scope, note, active, include_details, created_at, updated_at)
SELECT $1, $2, $3, $4, $5, $6, $7, true, $10, $9, $9
WHERE (SELECT count(*) FROM webhooks WHERE owner_kind = $2 AND owner_id = $3) < $8`,
		id, string(w.OwnerKind), w.OwnerID, w.URL, w.Secret, string(w.Scope), w.Note, limit,
		time.Now().UTC(), w.IncludeDetails)
	if err != nil {
		return nil, fmt.Errorf("creating webhook: %w", err)
	}
	added, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("creating webhook: %w", err)
	}
	if added == 0 {
		return nil, store.ErrLimitReached
	}
	return s.WebhookByID(ctx, id)
}

func (s *Store) WebhookByID(ctx context.Context, id string) (*store.Webhook, error) {
	return scanWebhook(s.db.QueryRowContext(ctx, webhookColumnsSQL+` WHERE id = $1`, id))
}

func (s *Store) ListWebhooks(ctx context.Context, kind store.WebhookOwnerKind, ownerID string) ([]store.Webhook, error) {
	rows, err := s.db.QueryContext(ctx,
		webhookColumnsSQL+` WHERE owner_kind = $1 AND owner_id = $2 ORDER BY created_at`,
		string(kind), ownerID)
	if err != nil {
		return nil, fmt.Errorf("listing webhooks: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only

	hooks := []store.Webhook{}
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		hooks = append(hooks, *w)
	}
	return hooks, rows.Err()
}

func (s *Store) UpdateWebhook(ctx context.Context, id string, in store.WebhookInput, now time.Time) (*store.Webhook, error) {
	// Switching it back on clears the failures: an owner who has fixed an
	// endpoint is telling the server to start over, and leaving the count
	// where it was would switch the webhook off again on the first hiccup.
	if _, err := s.db.ExecContext(ctx, `
UPDATE webhooks
SET url = $2, scope = $3, note = $4, active = $5, updated_at = $6, include_details = $7,
    failure_count = CASE WHEN $5 AND NOT active THEN 0 ELSE failure_count END
WHERE id = $1`,
		id, in.URL, string(in.Scope), in.Note, in.Active, now.UTC(), in.IncludeDetails); err != nil {
		return nil, fmt.Errorf("updating webhook: %w", err)
	}
	return s.WebhookByID(ctx, id)
}

func (s *Store) DeleteWebhook(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM webhooks WHERE id = $1`, id); err != nil {
		return fmt.Errorf("deleting webhook: %w", err)
	}
	return nil
}

func (s *Store) QueueWebhookDelivery(ctx context.Context, webhookID string, payload []byte, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO webhook_deliveries (id, webhook_id, payload, next_try_at, created_at)
VALUES ($1, $2, $3, $4, $4)`,
		store.NewID(), webhookID, string(payload), now.UTC()); err != nil {
		return fmt.Errorf("queueing webhook delivery: %w", err)
	}
	return nil
}

func (s *Store) DueWebhookDeliveries(ctx context.Context, now time.Time, limit int) ([]store.WebhookDelivery, error) {
	// Only for a webhook that is still switched on: a delivery queued
	// before an endpoint was disabled must not go out afterwards.
	rows, err := s.db.QueryContext(ctx, `
SELECT d.id, d.webhook_id, d.payload, d.attempts, w.url, w.secret
FROM webhook_deliveries d
JOIN webhooks w ON w.id = d.webhook_id
WHERE d.next_try_at <= $1 AND w.active
ORDER BY d.next_try_at
LIMIT $2`, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("reading due webhook deliveries: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only

	out := []store.WebhookDelivery{}
	for rows.Next() {
		var d store.WebhookDelivery
		var payload string
		if err := rows.Scan(&d.ID, &d.WebhookID, &payload, &d.Attempts, &d.URL, &d.Secret); err != nil {
			return nil, fmt.Errorf("scanning webhook delivery: %w", err)
		}
		d.Payload = []byte(payload)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) WebhookDelivered(ctx context.Context, deliveryID, webhookID string, status int, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE id = $1`, deliveryID); err != nil {
		return fmt.Errorf("clearing webhook delivery: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE webhooks SET failure_count = 0, last_status = $2, last_attempt_at = $3 WHERE id = $1`,
		webhookID, status, now.UTC()); err != nil {
		return fmt.Errorf("recording webhook success: %w", err)
	}
	return nil
}

func (s *Store) WebhookFailed(ctx context.Context, deliveryID, webhookID string, status int, reason string, nextTry time.Time, maxAttempts int) error {
	var attempts int64
	err := s.db.QueryRowContext(ctx, `
UPDATE webhook_deliveries
SET attempts = attempts + 1, next_try_at = $2, last_error = $3
WHERE id = $1
RETURNING attempts`, deliveryID, nextTry.UTC(), reason).Scan(&attempts)
	if err != nil {
		return notFoundIfNoRows(err, "recording webhook failure")
	}

	if _, err := s.db.ExecContext(ctx, `
UPDATE webhooks SET failure_count = failure_count + 1, last_status = $2, last_attempt_at = $3
WHERE id = $1`, webhookID, status, nextTry.UTC()); err != nil {
		return fmt.Errorf("recording webhook failure: %w", err)
	}

	// Out of attempts: drop the delivery and switch the webhook off. An
	// endpoint that has refused this many times in a row is not coming back
	// without somebody looking at it, and a queue that never drains is how
	// a dead endpoint becomes everybody's problem.
	if attempts >= int64(maxAttempts) {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE id = $1`, deliveryID); err != nil {
			return fmt.Errorf("dropping exhausted webhook delivery: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE webhooks SET active = false WHERE id = $1`, webhookID); err != nil {
			return fmt.Errorf("switching off a failed webhook: %w", err)
		}
	}
	return nil
}

func (s *Store) DeleteWebhooksFor(ctx context.Context, kind store.WebhookOwnerKind, ownerID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM webhooks WHERE owner_kind = $1 AND owner_id = $2`, string(kind), ownerID); err != nil {
		return fmt.Errorf("deleting the webhooks on a %s: %w", kind, err)
	}
	return nil
}
