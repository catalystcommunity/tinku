package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/federation"
	"github.com/catalystcommunity/tinku/api/internal/store"
	"github.com/catalystcommunity/tinku/api/internal/transport"
)

// Two instances, both real, talking to each other over HTTP.
//
// Nothing here is a mock: two HTTP servers, two databases, the real signer,
// the real outbox and the real sender. That is the only way to test a
// feature whose whole content is what happens between two processes.

// federatedPair boots a publisher and a directory, each with its own
// database, and gives them each other's addresses.
type federatedPair struct {
	Publisher *testEnv
	Directory *testEnv

	PublisherSigner federation.Signer
	Sender          *federation.Sender
}

func newFederatedPair(t *testing.T) *federatedPair {
	t.Helper()

	publisher := newLocalTestEnv(t)
	directory := newLocalTestEnv(t)

	signer, err := federation.NewDevSigner("tinku@publisher.test", "dev")
	if err != nil {
		t.Fatalf("building the publisher's signer: %v", err)
	}
	verifier, err := federation.NewDevVerifier("dev")
	if err != nil {
		t.Fatalf("building a verifier: %v", err)
	}

	// The publisher publishes; the directory receives. Each gets only the
	// half it needs, which is what makes the test prove the direction.
	publisher.EnableFederation(t, signer, verifier)
	directorySigner, err := federation.NewDevSigner("tinku@directory.test", "dev")
	if err != nil {
		t.Fatalf("building the directory's signer: %v", err)
	}
	directory.EnableFederation(t, directorySigner, verifier)

	return &federatedPair{
		Publisher:       publisher,
		Directory:       directory,
		PublisherSigner: signer,
		Sender: &federation.Sender{
			Store:         publisher.Store,
			Client:        &http.Client{Timeout: 5 * time.Second},
			FailureWindow: time.Hour,
			BaseDelay:     time.Millisecond,
			MaxDelay:      time.Millisecond,
			Now:           publisher.clock,
		},
	}
}

// peerUp records each side's view of the other and approves the directions
// under test: the publisher publishes outbound, the directory accepts
// inbound. Both sides opt in, which is the rule.
func (p *federatedPair) peerUp(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	toDirectory, err := p.Publisher.Store.CreatePeer(ctx, store.PeerInput{
		Address: "tinku@directory.test", Handle: "tinku", Domain: "directory.test",
		BaseURL: p.Directory.Server.URL,
	})
	if err != nil {
		t.Fatalf("recording the directory: %v", err)
	}
	approved := store.PeerStatusApproved
	if _, err := p.Publisher.Store.SetPeerStatus(ctx, toDirectory.ID, nil, &approved, nil); err != nil {
		t.Fatalf("approving outbound: %v", err)
	}

	fromPublisher, err := p.Directory.Store.CreatePeer(ctx, store.PeerInput{
		Address: "tinku@publisher.test", Handle: "tinku", Domain: "publisher.test",
		BaseURL: p.Publisher.Server.URL,
	})
	if err != nil {
		t.Fatalf("recording the publisher: %v", err)
	}
	if _, err := p.Directory.Store.SetPeerStatus(ctx, fromPublisher.ID, &approved, nil, nil); err != nil {
		t.Fatalf("approving inbound: %v", err)
	}
}

// remoteEvents is what the directory currently holds.
func (p *federatedPair) remoteEvents(t *testing.T) []store.RemoteEvent {
	t.Helper()
	events, _, err := p.Directory.Store.ListRemoteEvents(context.Background(),
		store.RemoteEventFilter{Now: p.Directory.clock(), Page: store.Page{Limit: 100}})
	if err != nil {
		t.Fatalf("reading what the directory holds: %v", err)
	}
	return events
}

func TestAnEventReachesAnApprovedDirectory(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)

	ada, _ := pair.Publisher.login(t, "ada")
	gathering := pair.Publisher.createGathering(t, ada, "Thursday Bouldering")
	event := pair.Publisher.createEvent(t, ada, gathering.Id, "Catan Night", 24*time.Hour)

	// Creating the event queued it. Nothing has been sent yet.
	if held := pair.remoteEvents(t); len(held) != 0 {
		t.Fatalf("the directory holds %d events before the sender ran", len(held))
	}

	delivered, failed, err := pair.Sender.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("running the sender: %v", err)
	}
	if delivered != 1 || failed != 0 {
		t.Fatalf("sender delivered %d and failed %d, want 1 and 0", delivered, failed)
	}

	held := pair.remoteEvents(t)
	if len(held) != 1 {
		t.Fatalf("the directory holds %d events, want 1", len(held))
	}
	got := held[0]
	if got.Title != "Catan Night" {
		t.Errorf("title %q, want Catan Night", got.Title)
	}
	if got.RemoteID != string(event.Id) {
		t.Errorf("remote id %q, want %q", got.RemoteID, event.Id)
	}
	if got.OriginDomain != "publisher.test" {
		t.Errorf("origin domain %q, want publisher.test", got.OriginDomain)
	}
	if got.GatheringName != "Thursday Bouldering" {
		t.Errorf("gathering name %q, want Thursday Bouldering", got.GatheringName)
	}
	// A delivery is a summary and a link. There is no description field on
	// a remote event at all, which is what stops the start-time lock from
	// needing to be reasoned about across a domain boundary.
	if got.CanonicalURL == "" {
		t.Error("the delivered event carries no link back to its origin")
	}
	// The instant survives the trip exactly.
	if !got.StartsAt.Equal(event.StartsAt) {
		t.Errorf("starts_at is %s, want %s", got.StartsAt, event.StartsAt)
	}
}

// A directory that has not approved a peer refuses its deliveries, and the
// sender records that as a failure rather than treating it as success.
func TestADirectoryRefusesAnUnapprovedPeer(t *testing.T) {
	pair := newFederatedPair(t)
	ctx := context.Background()

	// The publisher is willing to publish; the directory has NOT agreed.
	toDirectory, err := pair.Publisher.Store.CreatePeer(ctx, store.PeerInput{
		Address: "tinku@directory.test", Handle: "tinku", Domain: "directory.test",
		BaseURL: pair.Directory.Server.URL,
	})
	if err != nil {
		t.Fatalf("recording the directory: %v", err)
	}
	approved := store.PeerStatusApproved
	if _, err := pair.Publisher.Store.SetPeerStatus(ctx, toDirectory.ID, nil, &approved, nil); err != nil {
		t.Fatalf("approving outbound: %v", err)
	}

	ada, _ := pair.Publisher.login(t, "ada")
	gathering := pair.Publisher.createGathering(t, ada, "Thursday Bouldering")
	pair.Publisher.createEvent(t, ada, gathering.Id, "Catan Night", 24*time.Hour)

	delivered, failed, err := pair.Sender.RunOnce(ctx)
	if err != nil {
		t.Fatalf("running the sender: %v", err)
	}
	if delivered != 0 || failed != 1 {
		t.Fatalf("sender delivered %d and failed %d, want 0 and 1", delivered, failed)
	}
	if held := pair.remoteEvents(t); len(held) != 0 {
		t.Errorf("an unapproved peer got %d events through", len(held))
	}
}

// A deletion has to travel. Silence would leave the event on the
// directory's site forever.
func TestDeletingAnEventReachesTheDirectory(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)

	ada, adaProfile := pair.Publisher.login(t, "ada")
	gathering := pair.Publisher.createGathering(t, ada, "Thursday Bouldering")
	event := pair.Publisher.createEvent(t, ada, gathering.Id, "Catan Night", 24*time.Hour)

	if _, _, err := pair.Sender.RunOnce(context.Background()); err != nil {
		t.Fatalf("running the sender: %v", err)
	}
	if held := pair.remoteEvents(t); len(held) != 1 {
		t.Fatalf("the directory holds %d events after the first pass, want 1", len(held))
	}

	pair.Publisher.makeAdmin(t, string(adaProfile.Id))
	resp := pair.Publisher.call(t, ada, "event", "delete-event",
		csil.EncodeDeleteEventRequest(csil.DeleteEventRequest{Id: event.Id}))
	requireReply(t, resp, "Empty", "event/delete-event")

	if _, _, err := pair.Sender.RunOnce(context.Background()); err != nil {
		t.Fatalf("running the sender after the deletion: %v", err)
	}
	if held := pair.remoteEvents(t); len(held) != 0 {
		t.Errorf("the directory still holds %d events after a deletion", len(held))
	}
}

// The queue coalesces. An event edited before its first delivery is
// delivered once, in its final state.
func TestEditsBeforeDeliveryCoalesce(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)

	ada, _ := pair.Publisher.login(t, "ada")
	gathering := pair.Publisher.createGathering(t, ada, "Thursday Bouldering")
	event := pair.Publisher.createEvent(t, ada, gathering.Id, "Catan Night", 24*time.Hour)

	for _, title := range []string{"Catan Night (moved)", "Catan Night (final)"} {
		renamed := title
		resp := pair.Publisher.call(t, ada, "event", "update-event",
			csil.EncodeUpdateEventRequest(csil.UpdateEventRequest{Id: event.Id, Title: &renamed}))
		requireReply(t, resp, "Event", "event/update-event")
	}

	delivered, _, err := pair.Sender.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("running the sender: %v", err)
	}
	// One delivery for three writes: the queue holds one row per
	// (peer, event) and the last write replaced the others.
	if delivered != 1 {
		t.Errorf("sender delivered %d times for three writes, want 1", delivered)
	}
	held := pair.remoteEvents(t)
	if len(held) != 1 || held[0].Title != "Catan Night (final)" {
		t.Errorf("the directory holds %+v, want one event titled Catan Night (final)", held)
	}
}

// The suspension rule: a peer that has been failing longer than the window
// stops being retried, and only an administrator restarts it.
func TestAPeerIsSuspendedAfterTheFailureWindow(t *testing.T) {
	pair := newFederatedPair(t)
	ctx := context.Background()

	// A peer pointed at nothing, so every delivery fails.
	peer, err := pair.Publisher.Store.CreatePeer(ctx, store.PeerInput{
		Address: "tinku@gone.test", Handle: "tinku", Domain: "gone.test",
		BaseURL: "http://127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("recording the peer: %v", err)
	}
	approved := store.PeerStatusApproved
	if _, err := pair.Publisher.Store.SetPeerStatus(ctx, peer.ID, nil, &approved, nil); err != nil {
		t.Fatalf("approving outbound: %v", err)
	}

	ada, _ := pair.Publisher.login(t, "ada")
	gathering := pair.Publisher.createGathering(t, ada, "Thursday Bouldering")
	pair.Publisher.createEvent(t, ada, gathering.Id, "Catan Night", 24*time.Hour)

	// First failure opens the run. The peer is not suspended yet: it has
	// only just started failing.
	if _, failed, err := pair.Sender.RunOnce(ctx); err != nil || failed != 1 {
		t.Fatalf("first pass: failed=%d err=%v, want 1 and nil", failed, err)
	}
	after, err := pair.Publisher.Store.PeerByID(ctx, peer.ID)
	if err != nil {
		t.Fatalf("reading the peer: %v", err)
	}
	if after.Suspended() {
		t.Fatal("the peer was suspended on its first failure")
	}
	if after.FirstFailureAt == nil {
		t.Fatal("the failure run was not opened")
	}

	// Move past the window. The next failure suspends.
	pair.Publisher.advance(2 * time.Hour)
	if _, _, err := pair.Sender.RunOnce(ctx); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	after, err = pair.Publisher.Store.PeerByID(ctx, peer.ID)
	if err != nil {
		t.Fatalf("reading the peer: %v", err)
	}
	if !after.Suspended() {
		t.Fatal("the peer was not suspended after failing for longer than the window")
	}
	if after.LastFailureReason == "" {
		t.Error("the peer carries no reason for an administrator to read")
	}

	// A suspended peer is not chosen again, however much time passes.
	pair.Publisher.advance(24 * time.Hour)
	delivered, failed, err := pair.Sender.RunOnce(ctx)
	if err != nil {
		t.Fatalf("third pass: %v", err)
	}
	if delivered != 0 || failed != 0 {
		t.Errorf("a suspended peer was retried: delivered=%d failed=%d", delivered, failed)
	}

	// Only the button restarts it.
	if err := pair.Publisher.Store.ResumePeer(ctx, peer.ID, pair.Publisher.clock()); err != nil {
		t.Fatalf("resuming the peer: %v", err)
	}
	if _, failed, err := pair.Sender.RunOnce(ctx); err != nil || failed != 1 {
		t.Errorf("after a resume the peer should be tried again: failed=%d err=%v", failed, err)
	}
}

// A delivery whose signature does not check out is refused, even from a peer
// that IS approved. Approval says who may speak; the signature says that it
// was them.
func TestATamperedDeliveryIsRefused(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)

	body := csil.EncodeEventBatch(csil.EventBatch{
		OriginDomain: "publisher.test",
		BatchId:      store.NewID(),
		SentAt:       pair.Publisher.clock(),
		Events: []csil.FederatedEvent{{
			RemoteId: "01J0000000000000000000ZZ", CanonicalUrl: "https://publisher.test/events/x",
			Title: "Forged", IsOnline: true, StartsAt: pair.Publisher.clock().Add(time.Hour),
			EndsAt: pair.Publisher.clock().Add(2 * time.Hour), Timezone: "UTC",
		}},
	})

	resp := pair.Directory.call(t, pair.Directory.newClient(t), "federation", "deliver-events",
		csil.EncodeSignedDelivery(csil.SignedDelivery{
			SenderAddress: "tinku@publisher.test",
			Algorithm:     federation.DevAlgorithm,
			Signature:     "not-the-right-signature",
			Body:          body,
		}))
	requireServiceError(t, resp, 3, "federation/deliver-events with a bad signature")

	if held := pair.remoteEvents(t); len(held) != 0 {
		t.Errorf("a forged delivery stored %d events", len(held))
	}
}

// A verified sender cannot speak for a domain that is not theirs.
func TestABatchCannotClaimAnotherOrigin(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)

	body := csil.EncodeEventBatch(csil.EventBatch{
		// Correctly signed by tinku@publisher.test, but claiming to carry
		// somebody else's events.
		OriginDomain: "someone-else.test",
		BatchId:      store.NewID(),
		SentAt:       pair.Publisher.clock(),
		Events: []csil.FederatedEvent{{
			RemoteId: "01J0000000000000000000ZZ", CanonicalUrl: "https://someone-else.test/events/x",
			Title: "Impersonated", IsOnline: true, StartsAt: pair.Publisher.clock().Add(time.Hour),
			EndsAt: pair.Publisher.clock().Add(2 * time.Hour), Timezone: "UTC",
		}},
	})
	signature, keyID, err := pair.PublisherSigner.Sign(context.Background(), body)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	resp := pair.Directory.call(t, pair.Directory.newClient(t), "federation", "deliver-events",
		csil.EncodeSignedDelivery(csil.SignedDelivery{
			SenderAddress: pair.PublisherSigner.Address(),
			Algorithm:     pair.PublisherSigner.Algorithm(),
			KeyId:         keyID,
			Signature:     signature,
			Body:          body,
		}))
	requireServiceError(t, resp, 3, "federation/deliver-events claiming another origin")

	if held := pair.remoteEvents(t); len(held) != 0 {
		t.Errorf("a batch claiming another origin stored %d events", len(held))
	}
}

// setPublishDefault writes the instance setting directly, which is the same
// thing the admin op does.
func setInstanceSettings(t *testing.T, env *testEnv, mutate func(*store.InstanceSettings)) {
	t.Helper()
	settings, err := env.Store.InstanceSettings(context.Background())
	if err != nil {
		t.Fatalf("reading instance settings: %v", err)
	}
	mutate(&settings)
	if err := env.Store.PutInstanceSettings(context.Background(), settings); err != nil {
		t.Fatalf("writing instance settings: %v", err)
	}
}

// The publish rule is actually consulted: an instance default of `out`
// stops an event reaching a directory that would otherwise take it.
func TestAnInstanceDefaultOfOutPublishesNothing(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)
	setInstanceSettings(t, pair.Publisher, func(s *store.InstanceSettings) {
		s.PublishDefault = store.PublishOut
	})

	ada, _ := pair.Publisher.login(t, "ada")
	gathering := pair.Publisher.createGathering(t, ada, "Thursday Bouldering")
	pair.Publisher.createEvent(t, ada, gathering.Id, "Catan Night", 24*time.Hour)

	delivered, failed, err := pair.Sender.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("running the sender: %v", err)
	}
	// Nothing was even queued, so there is nothing to deliver or to fail.
	if delivered != 0 || failed != 0 {
		t.Errorf("sender delivered %d and failed %d, want 0 and 0", delivered, failed)
	}
	if held := pair.remoteEvents(t); len(held) != 0 {
		t.Errorf("an instance set to out published %d events", len(held))
	}
}

// A gathering can turn publishing off for itself while the instance
// publishes by default — and the decision travels back on the wire, so a
// client can render it rather than re-deriving the rule.
func TestAGatheringCanOptOut(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)

	ada, _ := pair.Publisher.login(t, "ada")
	gathering := pair.Publisher.createGathering(t, ada, "Thursday Bouldering")
	if !gathering.Publish.Publishing || gathering.Publish.Source != "instance" {
		t.Fatalf("a new gathering resolves to %+v, want publishing from the instance", gathering.Publish)
	}

	out := csil.PublishSetting("out")
	resp := pair.Publisher.call(t, ada, "gathering", "update-gathering",
		csil.EncodeUpdateGatheringRequest(csil.UpdateGatheringRequest{
			Id: gathering.Id, PublishEvents: &out,
		}))
	requireReply(t, resp, "Gathering", "gathering/update-gathering opting out")
	updated, err := csil.DecodeGathering(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Gathering: %v", err)
	}
	if updated.Publish.Publishing {
		t.Error("a gathering set to out still resolves to publishing")
	}
	if updated.Publish.Source != "gathering" {
		t.Errorf("the decision came from %q, want gathering", updated.Publish.Source)
	}

	pair.Publisher.createEvent(t, ada, gathering.Id, "Catan Night", 24*time.Hour)
	if _, _, err := pair.Sender.RunOnce(context.Background()); err != nil {
		t.Fatalf("running the sender: %v", err)
	}
	if held := pair.remoteEvents(t); len(held) != 0 {
		t.Errorf("a gathering that opted out published %d events", len(held))
	}
}

// An instance can withdraw the right to override. The gathering's control
// is then refused rather than accepted and ignored.
func TestAGatheringCannotOverrideWhenTheInstanceForbidsIt(t *testing.T) {
	env := newTestEnv(t)
	setInstanceSettings(t, env, func(s *store.InstanceSettings) {
		s.GatheringOverrideAllowed = false
	})

	ada, _ := env.login(t, "ada")
	gathering := env.createGathering(t, ada, "Thursday Bouldering")
	if gathering.Publish.CanOverride {
		t.Error("the gathering reports it may override when the instance forbids it")
	}

	out := csil.PublishSetting("out")
	resp := env.call(t, ada, "gathering", "update-gathering",
		csil.EncodeUpdateGatheringRequest(csil.UpdateGatheringRequest{
			Id: gathering.Id, PublishEvents: &out,
		}))
	// Refused, not silently dropped. A caller that sets a value the
	// instance will not honour deserves to be told.
	requireServiceError(t, resp, 3, "gathering/update-gathering when overrides are barred")
}

// A deletion travels even when publishing has since been switched off.
// Otherwise an event that was published stays on a peer's site forever.
func TestATombstoneTravelsEvenAfterOptingOut(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)

	ada, adaProfile := pair.Publisher.login(t, "ada")
	gathering := pair.Publisher.createGathering(t, ada, "Thursday Bouldering")
	event := pair.Publisher.createEvent(t, ada, gathering.Id, "Catan Night", 24*time.Hour)

	if _, _, err := pair.Sender.RunOnce(context.Background()); err != nil {
		t.Fatalf("running the sender: %v", err)
	}
	if held := pair.remoteEvents(t); len(held) != 1 {
		t.Fatalf("the directory holds %d events, want 1", len(held))
	}

	// Now switch publishing off entirely, then delete the event.
	setInstanceSettings(t, pair.Publisher, func(s *store.InstanceSettings) {
		s.PublishDefault = store.PublishOut
	})
	pair.Publisher.makeAdmin(t, string(adaProfile.Id))
	resp := pair.Publisher.call(t, ada, "event", "delete-event",
		csil.EncodeDeleteEventRequest(csil.DeleteEventRequest{Id: event.Id}))
	requireReply(t, resp, "Empty", "event/delete-event after opting out")

	if _, _, err := pair.Sender.RunOnce(context.Background()); err != nil {
		t.Fatalf("running the sender: %v", err)
	}
	if held := pair.remoteEvents(t); len(held) != 0 {
		t.Errorf("the directory still holds %d events after a deletion, want 0", len(held))
	}
}

// The rate limit: a peer that sends more than its allowance in a minute has
// the rest refused, and the receipt says how many.
func TestAPeerOverItsAllowanceIsThrottled(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)
	setInstanceSettings(t, pair.Directory, func(s *store.InstanceSettings) {
		s.PeerRateLimitPerMinute = 2
	})

	batch := make([]csil.FederatedEvent, 0, 5)
	for i := 0; i < 5; i++ {
		start := pair.Publisher.clock().Add(time.Duration(i+1) * time.Hour)
		batch = append(batch, csil.FederatedEvent{
			RemoteId:     "remote-" + string(rune('a'+i)),
			CanonicalUrl: "https://publisher.test/events/x",
			Title:        "Event",
			IsOnline:     true,
			StartsAt:     start,
			EndsAt:       start.Add(time.Hour),
			Timezone:     "UTC",
		})
	}
	receipt := pair.deliverBatch(t, batch)

	if receipt.Accepted != 2 {
		t.Errorf("accepted %d, want 2 (the allowance)", receipt.Accepted)
	}
	if receipt.RateLimited != 3 {
		t.Errorf("rate limited %d, want 3", receipt.RateLimited)
	}
	if held := pair.remoteEvents(t); len(held) != 2 {
		t.Errorf("the directory holds %d events, want 2", len(held))
	}

	// The peer carries the count, so an administrator can see it is being
	// throttled rather than merely quiet.
	peer, err := pair.Directory.Store.PeerByAddress(context.Background(), "tinku@publisher.test")
	if err != nil {
		t.Fatalf("reading the peer: %v", err)
	}
	if peer.RateLimitedTotal != 3 {
		t.Errorf("the peer records %d refused, want 3", peer.RateLimitedTotal)
	}
}

// A per-peer allowance overrides the instance-wide one, which is how a
// directory admits a trusted bulk publisher without raising the ceiling for
// everybody.
func TestAPeerCanBeGivenItsOwnAllowance(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)
	setInstanceSettings(t, pair.Directory, func(s *store.InstanceSettings) {
		s.PeerRateLimitPerMinute = 1
	})

	peer, err := pair.Directory.Store.PeerByAddress(context.Background(), "tinku@publisher.test")
	if err != nil {
		t.Fatalf("reading the peer: %v", err)
	}
	generous := int64(100)
	if _, err := pair.Directory.Store.SetPeerRateLimit(context.Background(), peer.ID, &generous); err != nil {
		t.Fatalf("raising the peer's allowance: %v", err)
	}

	batch := make([]csil.FederatedEvent, 0, 4)
	for i := 0; i < 4; i++ {
		start := pair.Publisher.clock().Add(time.Duration(i+1) * time.Hour)
		batch = append(batch, csil.FederatedEvent{
			RemoteId:     "remote-" + string(rune('a'+i)),
			CanonicalUrl: "https://publisher.test/events/x",
			Title:        "Event", IsOnline: true,
			StartsAt: start, EndsAt: start.Add(time.Hour), Timezone: "UTC",
		})
	}
	receipt := pair.deliverBatch(t, batch)
	if receipt.Accepted != 4 || receipt.RateLimited != 0 {
		t.Errorf("accepted %d and limited %d, want 4 and 0", receipt.Accepted, receipt.RateLimited)
	}
}

// deliverBatch signs a batch as the publisher and hands it to the directory
// directly, so a test can send more at once than the queue would.
func (p *federatedPair) deliverBatch(t *testing.T, events []csil.FederatedEvent) csil.DeliveryReceipt {
	t.Helper()
	return p.deliverBatchAs(t, store.NewID(), events)
}

// deliverBatchAs sends with a chosen batch id, so a test can replay one.
func (p *federatedPair) deliverBatchAs(t *testing.T, batchID string, events []csil.FederatedEvent) csil.DeliveryReceipt {
	t.Helper()
	resp := p.rawDeliverBatch(t, batchID, events)
	requireReply(t, resp, "DeliveryReceipt", "federation/deliver-events")
	receipt, err := csil.DecodeDeliveryReceipt(resp.Payload)
	if err != nil {
		t.Fatalf("decoding DeliveryReceipt: %v", err)
	}
	return receipt
}

// rawDeliverBatch returns the envelope without asserting it succeeded, so a
// test can check a refusal.
func (p *federatedPair) rawDeliverBatch(t *testing.T, batchID string, events []csil.FederatedEvent) transport.RpcResponse {
	t.Helper()
	body := csil.EncodeEventBatch(csil.EventBatch{
		OriginDomain: "publisher.test",
		BatchId:      batchID,
		SentAt:       p.Publisher.clock(),
		Events:       events,
	})
	signature, keyID, err := p.PublisherSigner.Sign(context.Background(), body)
	if err != nil {
		t.Fatalf("signing the batch: %v", err)
	}
	return p.Directory.call(t, p.Directory.newClient(t), "federation", "deliver-events",
		csil.EncodeSignedDelivery(csil.SignedDelivery{
			SenderAddress: p.PublisherSigner.Address(),
			Algorithm:     p.PublisherSigner.Algorithm(),
			KeyId:         keyID,
			Signature:     signature,
			Body:          body,
		}))
}

// The per-organization view: which origin inside a peer is responsible for
// the volume. The limit is enforced on the peer, so without this an
// operator can see that a peer is noisy and not which of its organizations
// made it so.
func TestOriginVolumeNamesTheNoisyOrganization(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)

	// One quiet organization and one busy one, on the same peer.
	batch := []csil.FederatedEvent{}
	add := func(organization string, count int) {
		for i := 0; i < count; i++ {
			start := pair.Publisher.clock().Add(time.Duration(len(batch)+1) * time.Hour)
			batch = append(batch, csil.FederatedEvent{
				RemoteId:         organization + "-" + string(rune('a'+i)),
				CanonicalUrl:     "https://publisher.test/events/x",
				Title:            "Event",
				IsOnline:         true,
				StartsAt:         start,
				EndsAt:           start.Add(time.Hour),
				Timezone:         "UTC",
				OrganizationName: organization,
			})
		}
	}
	add("Quiet Climbers", 1)
	add("Loud Chess Club", 4)

	receipt := pair.deliverBatch(t, batch)
	if receipt.Accepted != 5 {
		t.Fatalf("accepted %d, want 5", receipt.Accepted)
	}

	admin := pair.Directory.newClient(t)
	_, adminProfile := pair.Directory.login(t, "root")
	pair.Directory.makeAdmin(t, string(adminProfile.Id))
	admin, _ = pair.Directory.login(t, "root")

	resp := pair.Directory.call(t, admin, "federation", "list-origin-volume",
		csil.EncodeListOriginVolumeRequest(csil.ListOriginVolumeRequest{}))
	requireReply(t, resp, "OriginVolumeList", "federation/list-origin-volume")
	list, err := csil.DecodeOriginVolumeList(resp.Payload)
	if err != nil {
		t.Fatalf("decoding OriginVolumeList: %v", err)
	}

	if list.Total != 2 || len(list.Origins) != 2 {
		t.Fatalf("listed %d of %d origins, want 2 of 2", len(list.Origins), list.Total)
	}
	// Busiest first, so the answer to "who is flooding me" is the top row.
	loudest := list.Origins[0]
	if loudest.OrganizationName != "Loud Chess Club" {
		t.Errorf("the busiest origin is %q, want Loud Chess Club", loudest.OrganizationName)
	}
	if loudest.AcceptedTotal != 4 || loudest.Held != 4 {
		t.Errorf("the busiest origin shows accepted=%d held=%d, want 4 and 4",
			loudest.AcceptedTotal, loudest.Held)
	}
	if loudest.AcceptedThisMinute != 4 {
		t.Errorf("accepted this minute is %d, want 4", loudest.AcceptedThisMinute)
	}
	// The peer's throttle state rides along, so this list stands alone.
	if loudest.PeerAddress != "tinku@publisher.test" {
		t.Errorf("the origin names peer %q, want tinku@publisher.test", loudest.PeerAddress)
	}
	if loudest.PeerSuspended {
		t.Error("the peer is reported as suspended when it is not")
	}
}

// The volume view is operational detail about somebody else's instance, not
// a public listing.
func TestOriginVolumeNeedsAnAdministrator(t *testing.T) {
	env := newTestEnv(t)
	signer, err := federation.NewDevSigner("tinku@here.test", "dev")
	if err != nil {
		t.Fatalf("building a signer: %v", err)
	}
	verifier, err := federation.NewDevVerifier("dev")
	if err != nil {
		t.Fatalf("building a verifier: %v", err)
	}
	env.EnableFederation(t, signer, verifier)

	ada, _ := env.login(t, "ada")
	resp := env.call(t, ada, "federation", "list-origin-volume",
		csil.EncodeListOriginVolumeRequest(csil.ListOriginVolumeRequest{}))
	requireServiceError(t, resp, 3, "federation/list-origin-volume as a non-admin")

	anon := env.newClient(t)
	resp = env.call(t, anon, "federation", "list-origin-volume",
		csil.EncodeListOriginVolumeRequest(csil.ListOriginVolumeRequest{}))
	requireServiceError(t, resp, 2, "federation/list-origin-volume anonymously")
}

// The per-organization limit. Without it, one organization inside a peer
// spends the whole peer allowance and that peer's OTHER organizations are
// refused for something they did not do.
func TestOneNoisyOrganizationDoesNotStarveTheOthers(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)
	setInstanceSettings(t, pair.Directory, func(s *store.InstanceSettings) {
		s.PeerRateLimitPerMinute = 100 // generous: the peer is not the binding limit
		s.OriginRateLimitPerMinute = 2
	})

	batch := []csil.FederatedEvent{}
	add := func(organization string, count int) {
		for i := 0; i < count; i++ {
			start := pair.Publisher.clock().Add(time.Duration(len(batch)+1) * time.Hour)
			batch = append(batch, csil.FederatedEvent{
				RemoteId:     organization + "-" + string(rune('a'+i)),
				CanonicalUrl: "https://publisher.test/events/x",
				Title:        "Event", IsOnline: true,
				StartsAt: start, EndsAt: start.Add(time.Hour), Timezone: "UTC",
				OrganizationName: organization,
			})
		}
	}
	add("Loud Chess Club", 6)
	add("Quiet Climbers", 2)

	receipt := pair.deliverBatch(t, batch)

	// The loud one gets its two; the quiet one gets both of its two.
	if receipt.Accepted != 4 {
		t.Errorf("accepted %d, want 4 (2 per organization)", receipt.Accepted)
	}
	if receipt.RateLimited != 4 {
		t.Errorf("rate limited %d, want 4 (all from the loud organization)", receipt.RateLimited)
	}

	admin := pair.directoryAdmin(t)
	origins := pair.originVolume(t, admin)
	byName := map[string]csil.OriginVolume{}
	for _, o := range origins {
		byName[o.OrganizationName] = o
	}

	if got := byName["Quiet Climbers"]; got.AcceptedTotal != 2 || got.RateLimitedTotal != 0 {
		t.Errorf("the quiet organization shows accepted=%d limited=%d, want 2 and 0",
			got.AcceptedTotal, got.RateLimitedTotal)
	}
	if got := byName["Loud Chess Club"]; got.AcceptedTotal != 2 || got.RateLimitedTotal != 4 {
		t.Errorf("the loud organization shows accepted=%d limited=%d, want 2 and 4",
			got.AcceptedTotal, got.RateLimitedTotal)
	}
	// The peer itself was never over ITS limit, so its counter stays clean.
	peer, err := pair.Directory.Store.PeerByAddress(context.Background(), "tinku@publisher.test")
	if err != nil {
		t.Fatalf("reading the peer: %v", err)
	}
	if peer.RateLimitedTotal != 0 {
		t.Errorf("the peer records %d refused, want 0 — the organization limit bound, not the peer's",
			peer.RateLimitedTotal)
	}
}

// One origin can be throttled without touching the peer's others.
func TestAnOriginCanBeGivenItsOwnAllowance(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)
	setInstanceSettings(t, pair.Directory, func(s *store.InstanceSettings) {
		s.PeerRateLimitPerMinute = 100
		s.OriginRateLimitPerMinute = 10
	})

	first := pair.Publisher.clock().Add(time.Hour)
	pair.deliverBatch(t, []csil.FederatedEvent{{
		RemoteId: "seed", CanonicalUrl: "https://publisher.test/events/seed",
		Title: "Seed", IsOnline: true, StartsAt: first, EndsAt: first.Add(time.Hour),
		Timezone: "UTC", OrganizationName: "Loud Chess Club",
	}})

	admin := pair.directoryAdmin(t)
	peer, err := pair.Directory.Store.PeerByAddress(context.Background(), "tinku@publisher.test")
	if err != nil {
		t.Fatalf("reading the peer: %v", err)
	}
	one := uint64(1)
	resp := pair.Directory.call(t, admin, "federation", "set-origin-rate-limit",
		csil.EncodeSetOriginRateLimitRequest(csil.SetOriginRateLimitRequest{
			PeerId: peer.ID, OrganizationName: "Loud Chess Club", RateLimitPerMinute: &one,
		}))
	requireReply(t, resp, "OriginVolume", "federation/set-origin-rate-limit")
	origin, err := csil.DecodeOriginVolume(resp.Payload)
	if err != nil {
		t.Fatalf("decoding OriginVolume: %v", err)
	}
	if origin.RateLimitPerMinute == nil || *origin.RateLimitPerMinute != 1 {
		t.Fatalf("the origin's own limit is %v, want 1", origin.RateLimitPerMinute)
	}
	if origin.EffectiveRateLimitPerMinute != 1 {
		t.Errorf("the effective limit is %d, want 1", origin.EffectiveRateLimitPerMinute)
	}

	// A new minute, so the window is clear; the throttled origin now gets
	// one, and the other organization still gets its full instance-wide ten.
	pair.Publisher.advance(2 * time.Minute)
	pair.Directory.advance(2 * time.Minute)

	batch := []csil.FederatedEvent{}
	add := func(organization string, count int) {
		for i := 0; i < count; i++ {
			start := pair.Publisher.clock().Add(time.Duration(len(batch)+2) * time.Hour)
			batch = append(batch, csil.FederatedEvent{
				RemoteId:     organization + "-" + string(rune('a'+i)),
				CanonicalUrl: "https://publisher.test/events/x",
				Title:        "Event", IsOnline: true,
				StartsAt: start, EndsAt: start.Add(time.Hour), Timezone: "UTC",
				OrganizationName: organization,
			})
		}
	}
	add("Loud Chess Club", 3)
	add("Quiet Climbers", 3)

	receipt := pair.deliverBatch(t, batch)
	if receipt.Accepted != 4 {
		t.Errorf("accepted %d, want 4 (1 throttled + 3 unthrottled)", receipt.Accepted)
	}
	if receipt.RateLimited != 2 {
		t.Errorf("rate limited %d, want 2", receipt.RateLimited)
	}
}

// directoryAdmin signs in as an administrator on the directory.
func (p *federatedPair) directoryAdmin(t *testing.T) *http.Client {
	t.Helper()
	_, profile := p.Directory.login(t, "root")
	p.Directory.makeAdmin(t, string(profile.Id))
	client, _ := p.Directory.login(t, "root")
	return client
}

func (p *federatedPair) originVolume(t *testing.T, admin *http.Client) []csil.OriginVolume {
	t.Helper()
	resp := p.Directory.call(t, admin, "federation", "list-origin-volume",
		csil.EncodeListOriginVolumeRequest(csil.ListOriginVolumeRequest{}))
	requireReply(t, resp, "OriginVolumeList", "federation/list-origin-volume")
	list, err := csil.DecodeOriginVolumeList(resp.Payload)
	if err != nil {
		t.Fatalf("decoding OriginVolumeList: %v", err)
	}
	return list.Origins
}

// Name collisions across domains. A directory can hold an organization
// called the same thing as a local one; the DOMAIN is what tells them
// apart, so every record carries it and says whether it is foreign.
func TestOriginTellsLocalFromForeign(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)

	// A local gathering, and a remote event whose organization has the very
	// same name. These must not be mistakable for each other.
	ada, _ := pair.Directory.login(t, "ada")
	local := pair.Directory.createGathering(t, ada, "Loud Chess Club")
	if local.Origin.IsExternal {
		t.Error("a locally created gathering is reported as external")
	}
	if local.Origin.Domain != "tinku.test" {
		t.Errorf("the local gathering's domain is %q, want tinku.test", local.Origin.Domain)
	}
	if local.Origin.PeerAddress != nil {
		t.Error("a local record names a peer it did not come from")
	}

	start := pair.Publisher.clock().Add(time.Hour)
	pair.deliverBatch(t, []csil.FederatedEvent{{
		RemoteId: "remote-1", CanonicalUrl: "https://publisher.test/events/remote-1",
		Title: "Chess Night", IsOnline: true,
		StartsAt: start, EndsAt: start.Add(time.Hour), Timezone: "UTC",
		OrganizationName: "Loud Chess Club",
	}})

	resp := pair.Directory.call(t, pair.Directory.newClient(t), "federation", "list-remote-events",
		csil.EncodeListRemoteEventsRequest(csil.ListRemoteEventsRequest{}))
	requireReply(t, resp, "RemoteEventList", "federation/list-remote-events")
	list, err := csil.DecodeRemoteEventList(resp.Payload)
	if err != nil {
		t.Fatalf("decoding RemoteEventList: %v", err)
	}
	if len(list.Events) != 1 {
		t.Fatalf("the directory holds %d remote events, want 1", len(list.Events))
	}

	remote := list.Events[0]
	if !remote.Origin.IsExternal {
		t.Error("an event from a peer is not reported as external")
	}
	if remote.Origin.Domain != "publisher.test" {
		t.Errorf("the remote event's domain is %q, want publisher.test", remote.Origin.Domain)
	}
	// Not merely "not yours" but "theirs": the peer it arrived from.
	if remote.Origin.PeerAddress == nil || *remote.Origin.PeerAddress != "tinku@publisher.test" {
		t.Errorf("the remote event names peer %v, want tinku@publisher.test", remote.Origin.PeerAddress)
	}

	// The same name, two domains. That is the whole point.
	if remote.OrganizationName != local.Name {
		t.Fatalf("the fixture no longer tests a name collision: %q vs %q",
			remote.OrganizationName, local.Name)
	}
	if remote.Origin.Domain == local.Origin.Domain {
		t.Error("the collision is not distinguishable: both records claim the same domain")
	}
}

// A rate-limited event stays queued and arrives later.
//
// The sender used to treat any transport-OK answer as delivery and remove
// the outbox row, so a peer that refused for rate silently lost the event
// for good. Rate limiting has to be backpressure, not data loss.
func TestARateLimitedEventIsKeptAndRetried(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)
	// The directory will take nothing this minute.
	setInstanceSettings(t, pair.Directory, func(s *store.InstanceSettings) {
		s.PeerRateLimitPerMinute = 1
		s.OriginRateLimitPerMinute = 1
	})

	ada, _ := pair.Publisher.login(t, "ada")
	gathering := pair.Publisher.createGathering(t, ada, "Thursday Bouldering")
	pair.Publisher.createEvent(t, ada, gathering.Id, "First", 24*time.Hour)
	pair.Publisher.createEvent(t, ada, gathering.Id, "Second", 48*time.Hour)

	// Two events, an allowance of one. The first lands; the second is
	// refused for rate.
	delivered, failed, err := pair.Sender.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("running the sender: %v", err)
	}
	if failed != 0 {
		t.Errorf("the sender reported %d failures; a rate refusal is not a failure", failed)
	}
	if delivered != 1 {
		t.Errorf("delivered %d, want 1", delivered)
	}
	if held := pair.remoteEvents(t); len(held) != 1 {
		t.Fatalf("the directory holds %d events, want 1", len(held))
	}

	// The refused one is STILL QUEUED. This is the property that matters:
	// without it the event is gone and nothing will ever send it.
	queued, err := pair.Publisher.Store.DueDeliveries(
		context.Background(), pair.Publisher.clock().Add(24*time.Hour), 50)
	if err != nil {
		t.Fatalf("reading the queue: %v", err)
	}
	if len(queued) != 1 {
		t.Fatalf("%d deliveries are still queued, want 1 — the refused event was dropped", len(queued))
	}
	// Deferral is not failure, so it must not have counted an attempt
	// toward the exponential backoff.
	if queued[0].Attempts != 0 {
		t.Errorf("a deferred delivery counted %d attempts, want 0", queued[0].Attempts)
	}
	// Nor may it march the peer toward suspension: the peer answered.
	peer, err := pair.Publisher.Store.PeerByAddress(context.Background(), "tinku@directory.test")
	if err != nil {
		t.Fatalf("reading the peer: %v", err)
	}
	if peer.FirstFailureAt != nil || peer.Suspended() {
		t.Errorf("a rate refusal opened a failure run on the peer: %+v", peer.FirstFailureAt)
	}

	// A minute later the window has rolled and the retry lands.
	pair.Publisher.advance(2 * time.Minute)
	pair.Directory.advance(2 * time.Minute)
	delivered, failed, err = pair.Sender.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("running the sender again: %v", err)
	}
	if delivered != 1 || failed != 0 {
		t.Errorf("the retry delivered %d and failed %d, want 1 and 0", delivered, failed)
	}
	if held := pair.remoteEvents(t); len(held) != 2 {
		t.Errorf("the directory holds %d events after the retry, want 2", len(held))
	}
}

// A captured delivery replayed later is refused.
//
// A signature never expires, so replay protection cannot come from the
// signature. Without it, resending an old envelope would revert whatever
// the peer sent since — and an old tombstone would delete an event they had
// republished.
func TestAReplayedDeliveryIsRefused(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)

	start := pair.Publisher.clock().Add(time.Hour)
	events := []csil.FederatedEvent{{
		RemoteId: "remote-1", CanonicalUrl: "https://publisher.test/events/remote-1",
		Title: "Chess Night", IsOnline: true,
		StartsAt: start, EndsAt: start.Add(time.Hour), Timezone: "UTC",
	}}

	batchID := store.NewID()
	receipt := pair.deliverBatchAs(t, batchID, events)
	if receipt.Accepted != 1 {
		t.Fatalf("the first delivery accepted %d, want 1", receipt.Accepted)
	}

	// The very same envelope again — correctly signed, still verifying.
	resp := pair.rawDeliverBatch(t, batchID, events)
	requireServiceError(t, resp, 1, "federation/deliver-events replayed")

	if held := pair.remoteEvents(t); len(held) != 1 {
		t.Errorf("the directory holds %d events after a replay, want 1", len(held))
	}
}

// A replayed TOMBSTONE is the case that would do real damage: it would
// delete an event the peer had since republished.
func TestAReplayedTombstoneCannotDeleteARepublishedEvent(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)

	start := pair.Publisher.clock().Add(time.Hour)
	live := csil.FederatedEvent{
		RemoteId: "remote-1", CanonicalUrl: "https://publisher.test/events/remote-1",
		Title: "Chess Night", IsOnline: true,
		StartsAt: start, EndsAt: start.Add(time.Hour), Timezone: "UTC",
	}
	tombstone := live
	tombstone.Deleted = true

	pair.deliverBatch(t, []csil.FederatedEvent{live})
	tombstoneID := store.NewID()
	pair.deliverBatchAs(t, tombstoneID, []csil.FederatedEvent{tombstone})
	if held := pair.remoteEvents(t); len(held) != 0 {
		t.Fatalf("the tombstone did not remove the event")
	}

	// The peer republishes it.
	pair.deliverBatch(t, []csil.FederatedEvent{live})
	if held := pair.remoteEvents(t); len(held) != 1 {
		t.Fatalf("the republished event did not land")
	}

	// Somebody replays the old tombstone.
	resp := pair.rawDeliverBatch(t, tombstoneID, []csil.FederatedEvent{tombstone})
	requireServiceError(t, resp, 1, "federation/deliver-events replaying a tombstone")

	if held := pair.remoteEvents(t); len(held) != 1 {
		t.Errorf("a replayed tombstone deleted a republished event")
	}
}

// A delivery whose own timestamp is far from this instance's clock is
// refused. The window is what bounds how long batch ids must be remembered.
func TestAStaleDeliveryIsRefused(t *testing.T) {
	pair := newFederatedPair(t)
	pair.peerUp(t)

	start := pair.Publisher.clock().Add(time.Hour)
	events := []csil.FederatedEvent{{
		RemoteId: "remote-1", CanonicalUrl: "https://publisher.test/events/remote-1",
		Title: "Chess Night", IsOnline: true,
		StartsAt: start, EndsAt: start.Add(time.Hour), Timezone: "UTC",
	}}

	// The sender's clock is a day behind the directory's.
	pair.Directory.advance(24 * time.Hour)
	resp := pair.rawDeliverBatch(t, store.NewID(), events)
	requireServiceError(t, resp, 1, "federation/deliver-events with a stale timestamp")

	if held := pair.remoteEvents(t); len(held) != 0 {
		t.Errorf("a stale delivery stored %d events", len(held))
	}
}
