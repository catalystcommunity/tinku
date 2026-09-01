package federation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/store"
)

// Publisher turns a local event into a signed delivery and queues it for
// every peer this instance publishes to.
//
// The payload is built and SIGNED ONCE per event, then queued for each
// peer. A retry therefore sends exactly the bytes the first attempt did,
// which is what lets a signature survive a delay — re-signing at send time
// would make every attempt a different message.
type Publisher struct {
	Store  store.Store
	Signer Signer
	// PublicBaseURL is where this instance is reachable by a reader. It is
	// what a canonical link is built from, so a directory can send somebody
	// back to the event's own home.
	PublicBaseURL string
	OriginDomain  string
	Now           func() time.Time
}

// FederatedEventInput is what a caller has to know to publish an event: the
// event itself, plus the two names that give it context at the far end.
type FederatedEventInput struct {
	EventID          string
	Title            string
	IsOnline         bool
	IsInPerson       bool
	Location         store.Location
	StartsAt         time.Time
	EndsAt           time.Time
	Timezone         string
	GatheringName    string
	OrganizationName string
}

func (p *Publisher) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}

// PublishEvent queues an event, or a tombstone for it.
//
// A tombstone has to travel as a message like any other: a directory cannot
// tell an event that was deleted from one that simply stopped being
// mentioned, so silence would leave a deleted event on somebody else's site
// forever.
func (p *Publisher) PublishEvent(ctx context.Context, ev FederatedEventInput, deleted bool) error {
	peers, err := p.Store.OutboundPeers(ctx)
	if err != nil {
		return fmt.Errorf("reading outbound peers: %w", err)
	}
	if len(peers) == 0 {
		return nil
	}

	payload, err := p.envelope(ctx, ev, deleted)
	if err != nil {
		return err
	}
	now := p.now()
	for i := range peers {
		if err := p.Store.EnqueueDelivery(ctx, peers[i].ID, ev.EventID, payload, now); err != nil {
			return fmt.Errorf("queueing for %s: %w", peers[i].Address, err)
		}
	}
	return nil
}

// envelope builds the batch, signs its encoding, and wraps the result.
func (p *Publisher) envelope(ctx context.Context, ev FederatedEventInput, deleted bool) ([]byte, error) {
	federated := csil.FederatedEvent{
		RemoteId:     ev.EventID,
		CanonicalUrl: p.canonicalURL(ev.EventID),
		Title:        ev.Title,
		IsOnline:     ev.IsOnline,
		IsInPerson:   ev.IsInPerson,
		StartsAt:     ev.StartsAt.UTC(),
		EndsAt:       ev.EndsAt.UTC(),
		Timezone:     ev.Timezone,
		// Context, so a directory can say what this belongs to without a
		// second call to a site it may not be able to reach.
		GatheringName:    ev.GatheringName,
		OrganizationName: ev.OrganizationName,
		Deleted:          deleted,
	}
	if !ev.Location.IsZero() {
		federated.Location = &csil.Location{
			Name:       ev.Location.Name,
			Address:    ev.Location.Address,
			Locality:   ev.Location.Locality,
			Region:     ev.Location.Region,
			PostalCode: ev.Location.PostalCode,
			Country:    ev.Location.Country,
			Latitude:   ev.Location.Latitude,
			Longitude:  ev.Location.Longitude,
		}
	}

	body := csil.EncodeEventBatch(csil.EventBatch{
		OriginDomain: p.OriginDomain,
		// A value never repeated, so a receiver can refuse this envelope
		// the second time it arrives. Minted here rather than at send time
		// because a retry must deliver the SAME bytes — a new id per
		// attempt would make every retry look like a fresh batch.
		BatchId: store.NewID(),
		SentAt:  p.now(),
		Events:  []csil.FederatedEvent{federated},
	})
	signature, keyID, err := p.Signer.Sign(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("signing the delivery: %w", err)
	}
	return csil.EncodeSignedDelivery(csil.SignedDelivery{
		SenderAddress: p.Signer.Address(),
		Algorithm:     p.Signer.Algorithm(),
		KeyId:         keyID,
		Signature:     signature,
		Body:          body,
	}), nil
}

// canonicalURL is where a reader goes for everything a delivery leaves out.
func (p *Publisher) canonicalURL(eventID string) string {
	return strings.TrimRight(p.PublicBaseURL, "/") + "/events/" + eventID
}
