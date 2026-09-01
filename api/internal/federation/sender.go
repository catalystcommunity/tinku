package federation

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/store"
	"github.com/catalystcommunity/tinku/api/internal/transport"
)

// Sender drains the outbox.
//
// # The suspension rule
//
// A failing peer is retried with a growing delay, but not forever. Once the
// CURRENT RUN of failures has lasted longer than FailureWindow, the peer is
// suspended and the sender stops choosing it. Nothing lifts a suspension on
// its own: an administrator has to look at the reason and restart the peer.
//
// The window is measured from when the run began, not from a count of
// attempts. A count means different things at different poll intervals;
// "this peer has been broken for a day" means the same thing however often
// the sender happens to run.
type Sender struct {
	Store  store.Store
	Client *http.Client

	// FailureWindow is how long a peer may keep failing before it is
	// suspended.
	FailureWindow time.Duration
	// BaseDelay is the wait before a first retry; each further attempt
	// doubles it, up to MaxDelay.
	BaseDelay time.Duration
	MaxDelay  time.Duration
	// RateDelay is the wait after a peer refuses an event for RATE. It is
	// separate, and flat rather than exponential, because that refusal is
	// backpressure and not a fault: the peer answered, and its window is a
	// minute long whatever our attempt count says.
	//
	// It is configurable because a peer may be somebody else's
	// implementation entirely. Nothing in the protocol lets us know their
	// window, so this is OUR policy about how patiently to wait — not an
	// agreement with them.
	RateDelay time.Duration
	// BatchSize bounds one pass, so a large backlog cannot hold the loop.
	BatchSize int

	Now func() time.Time
}

// Defaults for a Sender left partly unset.
const (
	DefaultFailureWindow = 24 * time.Hour
	DefaultBaseDelay     = 30 * time.Second
	DefaultMaxDelay      = time.Hour
	// DefaultRateDelay is one window plus a little. A shorter wait only
	// spends the peer's next allowance on the same event.
	DefaultRateDelay    = 70 * time.Second
	DefaultBatchSize    = 50
	DefaultPollInterval = 30 * time.Second
)

func (s *Sender) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Sender) failureWindow() time.Duration {
	if s.FailureWindow > 0 {
		return s.FailureWindow
	}
	return DefaultFailureWindow
}

// Run drains the outbox on a timer until ctx is cancelled.
func (s *Sender) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			delivered, failed, err := s.RunOnce(ctx)
			switch {
			case err != nil:
				log.WithError(err).Warn("federation: a delivery pass failed")
			case delivered > 0 || failed > 0:
				log.WithFields(log.Fields{"delivered": delivered, "failed": failed}).
					Info("federation: delivery pass")
			}
		}
	}
}

// RunOnce makes one pass over what is due.
// deliveryOutcome is what a peer did with an event, as distinct from
// whether we could reach them.
type deliveryOutcome int

const (
	// outcomeAccepted: the peer took it.
	outcomeAccepted deliveryOutcome = iota
	// outcomeRateLimited: the peer answered and refused for rate. Try again.
	outcomeRateLimited
	// outcomeRefused: the peer answered and refused for a reason waiting
	// will not change.
	outcomeRefused
)

func (s *Sender) RunOnce(ctx context.Context) (delivered, failed int, err error) {
	batch := s.BatchSize
	if batch <= 0 {
		batch = DefaultBatchSize
	}
	items, err := s.Store.DueDeliveries(ctx, s.now(), batch)
	if err != nil {
		return 0, 0, err
	}
	var deferred, refused int
	for i := range items {
		if ctx.Err() != nil {
			return delivered, failed, ctx.Err()
		}
		outcome, deliverErr := s.deliver(ctx, &items[i])
		switch {
		case deliverErr != nil:
			failed++
			s.recordFailure(ctx, &items[i], deliverErr)
		case outcome == outcomeRateLimited:
			// The event is NOT delivered and the row is NOT removed. This
			// is the whole point: dropping it here would lose the event
			// permanently, and the peer told us it wants it later.
			deferred++
			s.recordDeferral(ctx, &items[i])
		case outcome == outcomeRefused:
			// A refusal that is not about rate will not become acceptable
			// by waiting. Keeping it would retry a poison pill forever.
			refused++
			s.recordRefusal(ctx, &items[i])
		default:
			delivered++
			s.recordSuccess(ctx, &items[i])
		}
	}
	if deferred > 0 || refused > 0 {
		log.WithFields(log.Fields{"deferred": deferred, "refused": refused}).
			Info("federation: some deliveries were answered but not accepted")
	}
	return delivered, failed, nil
}

// deliver posts one already-signed envelope to a peer, over the same
// CSIL-RPC carrier everything else uses. A peer is a tinku instance, so
// there is no second protocol to maintain.
func (s *Sender) deliver(ctx context.Context, item *store.OutboxItem) (deliveryOutcome, error) {
	if item.PeerBaseURL == "" {
		return outcomeAccepted, fmt.Errorf("peer %s has no base URL", item.PeerAddress)
	}
	envelope, err := transport.NewRpcRequest("federation", "deliver-events", item.Payload).Encode()
	if err != nil {
		return outcomeAccepted, fmt.Errorf("encoding the request envelope: %w", err)
	}

	endpoint := trimSlash(item.PeerBaseURL) + "/csil/v1/rpc"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(envelope))
	if err != nil {
		return outcomeAccepted, fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("Content-Type", "application/cbor")

	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return outcomeAccepted, fmt.Errorf("reaching %s: %w", endpoint, err)
	}
	defer res.Body.Close() //nolint:errcheck // response body

	if res.StatusCode != http.StatusOK {
		return outcomeAccepted, fmt.Errorf("%s answered HTTP %d", endpoint, res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return outcomeAccepted, fmt.Errorf("reading the response: %w", err)
	}
	resp, err := transport.DecodeRpcResponse(raw)
	if err != nil {
		return outcomeAccepted, fmt.Errorf("decoding the response: %w", err)
	}
	if !resp.Status.IsOk() {
		return outcomeAccepted, fmt.Errorf("%s refused the delivery: transport status %s", item.PeerAddress, resp.Status.Name())
	}
	// A declared error arm is a refusal the peer meant, not a transport
	// fault — most often "you are not an approved peer here". It is still a
	// failure for this item, and still worth an operator's eyes.
	if resp.Variant != nil && *resp.Variant == "ServiceError" {
		return outcomeAccepted, fmt.Errorf("%s refused the delivery", item.PeerAddress)
	}

	// READ THE RECEIPT. A transport-level OK says the message arrived, not
	// that the peer kept it. Treating arrival as acceptance is how a
	// rate-limited event gets deleted from the outbox and lost for good.
	receipt, err := csil.DecodeDeliveryReceipt(resp.Payload)
	if err != nil {
		return outcomeAccepted, fmt.Errorf("decoding the receipt from %s: %w", item.PeerAddress, err)
	}
	switch {
	case receipt.Accepted > 0:
		return outcomeAccepted, nil
	case receipt.RateLimited > 0:
		return outcomeRateLimited, nil
	default:
		// Accepted nothing and not for rate: malformed, or a tombstone for
		// something the peer never held. Waiting will not change either.
		return outcomeRefused, nil
	}
}

// recordDeferral reschedules without counting an attempt, and treats the
// peer as healthy — it answered. Suspension is for a peer that cannot be
// reached, not one that is busy.
func (s *Sender) recordDeferral(ctx context.Context, item *store.OutboxItem) {
	next := s.now().Add(s.rateDelay())
	if err := s.Store.DeferDelivery(ctx, item.ID, "refused for rate; will retry", next); err != nil {
		log.WithError(err).WithField("peer", item.PeerAddress).
			Warn("federation: could not defer a rate-limited delivery")
	}
	if err := s.Store.RecordDeliverySuccess(ctx, item.PeerID, s.now()); err != nil {
		log.WithError(err).WithField("peer", item.PeerAddress).
			Warn("federation: could not record a reachable peer")
	}
	log.WithFields(log.Fields{"peer": item.PeerAddress, "event": item.EventID, "retry_at": next}).
		Info("federation: a peer refused an event for rate; it stays queued")
}

// recordRefusal drops an event the peer will never take. It is logged at
// error level because it is data this instance wanted delivered and never
// will be — an operator has to know.
func (s *Sender) recordRefusal(ctx context.Context, item *store.OutboxItem) {
	if err := s.Store.MarkDelivered(ctx, item.ID); err != nil {
		log.WithError(err).WithField("peer", item.PeerAddress).
			Warn("federation: could not clear a refused item")
	}
	if err := s.Store.RecordDeliverySuccess(ctx, item.PeerID, s.now()); err != nil {
		log.WithError(err).WithField("peer", item.PeerAddress).
			Warn("federation: could not record a reachable peer")
	}
	log.WithFields(log.Fields{"peer": item.PeerAddress, "event": item.EventID}).
		Error("federation: a peer refused an event outright; it will not be sent again")
}

func (s *Sender) rateDelay() time.Duration {
	if s.RateDelay > 0 {
		return s.RateDelay
	}
	return DefaultRateDelay
}

func (s *Sender) recordSuccess(ctx context.Context, item *store.OutboxItem) {
	if err := s.Store.MarkDelivered(ctx, item.ID); err != nil {
		log.WithError(err).WithField("peer", item.PeerAddress).
			Warn("federation: could not clear a delivered item")
	}
	if err := s.Store.RecordDeliverySuccess(ctx, item.PeerID, s.now()); err != nil {
		log.WithError(err).WithField("peer", item.PeerAddress).
			Warn("federation: could not record a success")
	}
}

func (s *Sender) recordFailure(ctx context.Context, item *store.OutboxItem, cause error) {
	now := s.now()
	if err := s.Store.MarkDeliveryFailed(ctx, item.ID, cause.Error(), s.nextAttempt(now, item.Attempts)); err != nil {
		log.WithError(err).WithField("peer", item.PeerAddress).
			Warn("federation: could not record a failed item")
	}
	// The peer-level record is what decides suspension. Passing the cutoff
	// rather than the window keeps the comparison in one place: the store
	// asks only whether the run began at or before this instant.
	if err := s.Store.RecordDeliveryFailure(
		ctx, item.PeerID, cause.Error(), now, now.Add(-s.failureWindow()),
	); err != nil {
		log.WithError(err).WithField("peer", item.PeerAddress).
			Warn("federation: could not record a failure")
	}
	log.WithError(cause).WithFields(log.Fields{
		"peer": item.PeerAddress, "attempts": item.Attempts + 1,
	}).Warn("federation: delivery failed")
}

// nextAttempt backs off exponentially and stops growing at MaxDelay. The
// exponent is capped before the shift, so a long-broken peer cannot
// overflow the duration.
func (s *Sender) nextAttempt(now time.Time, attempts int64) time.Time {
	base := s.BaseDelay
	if base <= 0 {
		base = DefaultBaseDelay
	}
	ceiling := s.MaxDelay
	if ceiling <= 0 {
		ceiling = DefaultMaxDelay
	}
	exponent := math.Min(float64(attempts), 16)
	delay := time.Duration(float64(base) * math.Pow(2, exponent))
	if delay > ceiling || delay <= 0 {
		delay = ceiling
	}
	return now.Add(delay)
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
