package webhooks

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

const (
	// MaxAttempts before a delivery is dropped and its webhook switched
	// off. An endpoint that has refused six times over roughly an hour is
	// not coming back without somebody looking at it, and a queue that
	// never drains is how one dead endpoint becomes everybody's problem.
	MaxAttempts = 6

	// DeliveryTimeout bounds one POST. A receiver that holds the connection
	// open must not hold up the queue behind it.
	DeliveryTimeout = 10 * time.Second

	// PollInterval is how often the sender looks for work.
	PollInterval = 10 * time.Second

	// BatchSize is how many deliveries one pass claims.
	BatchSize = 50
)

// Sender drains the delivery queue.
//
// It is a poller rather than a queue subscription because the API scales
// horizontally: every replica runs one of these, and the claim is the
// database row itself. Two replicas can pick up the same row and both POST
// it — a duplicate delivery is a receiver's problem to make idempotent, and
// a missed one is not recoverable at all, so the trade goes this way on
// purpose. The delivery id is in the header for exactly that reason.
type Sender struct {
	Store  store.Store
	Client *http.Client
	// UserAgent identifies this instance to a receiver.
	UserAgent string
}

// NewSender builds one with a bounded client. A webhook points at a URL an
// owner chose, which is to say an address this server has no reason to
// trust, so the timeout is not optional.
func NewSender(st store.Store, userAgent string) *Sender {
	return &Sender{
		Store:     st,
		Client:    &http.Client{Timeout: DeliveryTimeout},
		UserAgent: userAgent,
	}
}

// Run polls until the context is cancelled.
func (s *Sender) Run(ctx context.Context) {
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Drain(ctx)
		}
	}
}

// Drain sends everything due now. Exported so a test can run one pass
// instead of waiting for a tick.
func (s *Sender) Drain(ctx context.Context) {
	due, err := s.Store.DueWebhookDeliveries(ctx, time.Now(), BatchSize)
	if err != nil {
		log.WithError(err).Warn("could not read due webhook deliveries")
		return
	}
	for _, delivery := range due {
		if ctx.Err() != nil {
			return
		}
		s.deliver(ctx, delivery)
	}
}

func (s *Sender) deliver(ctx context.Context, d store.WebhookDelivery) {
	status, err := s.post(ctx, d)
	now := time.Now()

	if err == nil {
		if err := s.Store.WebhookDelivered(ctx, d.ID, d.WebhookID, status, now); err != nil {
			log.WithError(err).Warn("could not record a webhook delivery")
		}
		return
	}

	// Exponential, from about a minute: 1, 2, 4, 8, 16, 32 minutes. Six
	// attempts is therefore roughly an hour of trying before the endpoint
	// is switched off.
	backoff := time.Duration(1<<uint(d.Attempts)) * time.Minute
	if backoff > 32*time.Minute {
		backoff = 32 * time.Minute
	}
	if err := s.Store.WebhookFailed(ctx, d.ID, d.WebhookID, status, err.Error(), now.Add(backoff), MaxAttempts); err != nil {
		log.WithError(err).Warn("could not record a webhook failure")
	}
}

// post makes the request. A non-2xx status IS a failure: a receiver that
// answers 500 has not taken the delivery, and treating arrival as
// acceptance is how a notification is lost while looking successful.
func (s *Sender) post(ctx context.Context, d store.WebhookDelivery) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, DeliveryTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, bytes.NewReader(d.Payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", s.UserAgent)
	// The id is what makes a duplicate detectable by a receiver: two
	// replicas can claim the same row, and a receiver that keys on this
	// header sees one delivery either way.
	req.Header.Set("X-Tinku-Delivery", d.ID)
	req.Header.Set("X-Tinku-Signature", Sign(d.Secret, d.Payload))

	resp, err := s.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close() //nolint:errcheck // nothing is read from it

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("the receiver answered %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}
