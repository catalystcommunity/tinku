package server_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

// The federation store surface, on whatever backend this run uses.
//
// The two-instance tests in federation_test.go pin themselves to SQLite,
// because two instances need two databases. That leaves ~17 store methods
// exercised on one dialect only, which is precisely the gap that has
// already produced two bugs in this repository. This file closes it: it
// runs against Postgres under `./tools.sh test-pg` and against SQLite
// otherwise.

func newPeer(t *testing.T, env *testEnv, address string) *store.Peer {
	t.Helper()
	handle, domain, _ := cutAddress(address)
	peer, err := env.Store.CreatePeer(context.Background(), store.PeerInput{
		Address: address, Handle: handle, Domain: domain,
		BaseURL: "https://" + domain, Note: "a note",
	})
	if err != nil {
		t.Fatalf("creating peer %s: %v", address, err)
	}
	return peer
}

func cutAddress(address string) (handle, domain string, ok bool) {
	for i := 0; i < len(address); i++ {
		if address[i] == '@' {
			return address[:i], address[i+1:], true
		}
	}
	return address, "", false
}

func TestPeerStatusesMoveIndependently(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	peer := newPeer(t, env, "tinku@example.test")

	if peer.InboundStatus != store.PeerStatusNone || peer.OutboundStatus != store.PeerStatusNone {
		t.Fatalf("a new peer starts at %s/%s, want none/none", peer.InboundStatus, peer.OutboundStatus)
	}

	// Approving inbound must not move outbound. Knowing a peer, accepting
	// what it sends, and publishing to it are three different things.
	approved := store.PeerStatusApproved
	updated, err := env.Store.SetPeerStatus(ctx, peer.ID, &approved, nil, nil)
	if err != nil {
		t.Fatalf("approving inbound: %v", err)
	}
	if updated.InboundStatus != store.PeerStatusApproved {
		t.Errorf("inbound is %s, want approved", updated.InboundStatus)
	}
	if updated.OutboundStatus != store.PeerStatusNone {
		t.Errorf("approving inbound moved outbound to %s", updated.OutboundStatus)
	}

	found, err := env.Store.PeerByAddress(ctx, "tinku@example.test")
	if err != nil {
		t.Fatalf("looking the peer up by address: %v", err)
	}
	if found.ID != peer.ID {
		t.Errorf("address lookup found %s, want %s", found.ID, peer.ID)
	}

	peers, total, err := env.Store.ListPeers(ctx, store.Page{})
	if err != nil {
		t.Fatalf("listing peers: %v", err)
	}
	if total != 1 || len(peers) != 1 {
		t.Errorf("listed %d of %d peers, want 1 of 1", len(peers), total)
	}
}

func TestTheOutboxCoalescesPerEvent(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	peer := newPeer(t, env, "tinku@example.test")
	approved := store.PeerStatusApproved
	if _, err := env.Store.SetPeerStatus(ctx, peer.ID, nil, &approved, nil); err != nil {
		t.Fatalf("approving outbound: %v", err)
	}

	now := env.clock()
	for _, payload := range [][]byte{[]byte("first"), []byte("second"), []byte("final")} {
		if err := env.Store.EnqueueDelivery(ctx, peer.ID, "event-1", payload, now); err != nil {
			t.Fatalf("enqueueing: %v", err)
		}
	}
	// A second event is a second row: the key is (peer, event), not peer.
	if err := env.Store.EnqueueDelivery(ctx, peer.ID, "event-2", []byte("other"), now); err != nil {
		t.Fatalf("enqueueing the second event: %v", err)
	}

	due, err := env.Store.DueDeliveries(ctx, now, 50)
	if err != nil {
		t.Fatalf("reading due deliveries: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("queue holds %d rows for three writes to one event plus one to another, want 2", len(due))
	}
	for _, item := range due {
		if item.EventID == "event-1" && string(item.Payload) != "final" {
			t.Errorf("the queued payload is %q, want the last one written", item.Payload)
		}
		if item.PeerAddress != peer.Address || item.PeerBaseURL != peer.BaseURL {
			t.Errorf("the queue row does not carry the peer's address and URL: %+v", item)
		}
	}
}

func TestSuspensionStopsAndResumeRestarts(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	peer := newPeer(t, env, "tinku@example.test")
	approved := store.PeerStatusApproved
	if _, err := env.Store.SetPeerStatus(ctx, peer.ID, nil, &approved, nil); err != nil {
		t.Fatalf("approving outbound: %v", err)
	}
	now := env.clock()
	if err := env.Store.EnqueueDelivery(ctx, peer.ID, "event-1", []byte("payload"), now); err != nil {
		t.Fatalf("enqueueing: %v", err)
	}

	// A failure whose run began just now does not suspend.
	if err := env.Store.RecordDeliveryFailure(ctx, peer.ID, "connection refused", now, now.Add(-time.Hour)); err != nil {
		t.Fatalf("recording a failure: %v", err)
	}
	after, err := env.Store.PeerByID(ctx, peer.ID)
	if err != nil {
		t.Fatalf("reading the peer: %v", err)
	}
	if after.Suspended() {
		t.Fatal("a peer was suspended on the first failure of a run")
	}
	if after.FirstFailureAt == nil {
		t.Fatal("the failure run was not opened")
	}

	// A later failure, with the run now older than the window, suspends.
	later := now.Add(2 * time.Hour)
	if err := env.Store.RecordDeliveryFailure(ctx, peer.ID, "connection refused", later, later.Add(-time.Hour)); err != nil {
		t.Fatalf("recording a later failure: %v", err)
	}
	after, err = env.Store.PeerByID(ctx, peer.ID)
	if err != nil {
		t.Fatalf("reading the peer: %v", err)
	}
	if !after.Suspended() {
		t.Fatal("a peer failing for longer than the window was not suspended")
	}

	// A suspended peer's work is not due, however late it is.
	due, err := env.Store.DueDeliveries(ctx, later.Add(24*time.Hour), 50)
	if err != nil {
		t.Fatalf("reading due deliveries: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("a suspended peer offered %d deliveries", len(due))
	}

	// Only a resume brings it back, and it makes the work due at once.
	if err := env.Store.ResumePeer(ctx, peer.ID, later); err != nil {
		t.Fatalf("resuming: %v", err)
	}
	after, err = env.Store.PeerByID(ctx, peer.ID)
	if err != nil {
		t.Fatalf("reading the peer: %v", err)
	}
	if after.Suspended() || after.FirstFailureAt != nil || after.LastFailureReason != "" {
		t.Errorf("a resumed peer still carries a failure run: %+v", after)
	}
	due, err = env.Store.DueDeliveries(ctx, later, 50)
	if err != nil {
		t.Fatalf("reading due deliveries after a resume: %v", err)
	}
	if len(due) != 1 {
		t.Errorf("a resumed peer offered %d deliveries, want 1", len(due))
	}

	// A success clears the run rather than leaving a stale start behind.
	if err := env.Store.RecordDeliverySuccess(ctx, peer.ID, later); err != nil {
		t.Fatalf("recording success: %v", err)
	}
	after, err = env.Store.PeerByID(ctx, peer.ID)
	if err != nil {
		t.Fatalf("reading the peer: %v", err)
	}
	if after.FirstFailureAt != nil || after.LastSuccessAt == nil {
		t.Errorf("a success did not close the failure run: %+v", after)
	}
}

// OutboundPeers is what the publisher fans out over, so it must exclude a
// peer that is merely known, one that is only inbound, and one that is
// suspended.
func TestOutboundPeersExcludesEverythingButApprovedAndRunning(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	approved := store.PeerStatusApproved

	known := newPeer(t, env, "tinku@known.test")
	_ = known

	inboundOnly := newPeer(t, env, "tinku@inbound.test")
	if _, err := env.Store.SetPeerStatus(ctx, inboundOnly.ID, &approved, nil, nil); err != nil {
		t.Fatalf("approving inbound: %v", err)
	}

	suspended := newPeer(t, env, "tinku@suspended.test")
	if _, err := env.Store.SetPeerStatus(ctx, suspended.ID, nil, &approved, nil); err != nil {
		t.Fatalf("approving outbound: %v", err)
	}
	now := env.clock()
	if err := env.Store.RecordDeliveryFailure(ctx, suspended.ID, "gone", now, now.Add(time.Hour)); err != nil {
		t.Fatalf("suspending: %v", err)
	}

	wanted := newPeer(t, env, "tinku@wanted.test")
	if _, err := env.Store.SetPeerStatus(ctx, wanted.ID, nil, &approved, nil); err != nil {
		t.Fatalf("approving outbound: %v", err)
	}

	peers, err := env.Store.OutboundPeers(ctx)
	if err != nil {
		t.Fatalf("reading outbound peers: %v", err)
	}
	if len(peers) != 1 || peers[0].Address != "tinku@wanted.test" {
		addresses := make([]string, 0, len(peers))
		for i := range peers {
			addresses = append(addresses, peers[i].Address)
		}
		t.Errorf("outbound peers are %v, want just tinku@wanted.test", addresses)
	}
}

func TestRemoteEventsUpsertAndDelete(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	peer := newPeer(t, env, "tinku@example.test")
	now := env.clock()

	lat, lon := 39.7392, -104.9903
	in := store.RemoteEventInput{
		RemoteID:     "remote-1",
		OriginDomain: "example.test",
		CanonicalURL: "https://example.test/events/remote-1",
		Title:        "Denver Session",
		SearchText:   "denver session",
		IsInPerson:   true,
		Location: store.Location{
			Name: "A Gym", Locality: "Denver", Region: "Colorado", Country: "US",
			Latitude: &lat, Longitude: &lon,
		},
		StartsAt:      now.Add(24 * time.Hour),
		EndsAt:        now.Add(26 * time.Hour),
		Timezone:      "America/Denver",
		GatheringName: "Bouldering",
	}
	if err := env.Store.UpsertRemoteEvent(ctx, peer.ID, in); err != nil {
		t.Fatalf("storing a remote event: %v", err)
	}

	// The same remote id again is an update, not a duplicate.
	in.Title = "Denver Session (moved)"
	in.SearchText = "denver session (moved)"
	if err := env.Store.UpsertRemoteEvent(ctx, peer.ID, in); err != nil {
		t.Fatalf("updating a remote event: %v", err)
	}

	events, total, err := env.Store.ListRemoteEvents(ctx, store.RemoteEventFilter{
		Now: now, Page: store.Page{Limit: 50},
	})
	if err != nil {
		t.Fatalf("listing remote events: %v", err)
	}
	if total != 1 || len(events) != 1 {
		t.Fatalf("the directory holds %d of %d, want 1 of 1", len(events), total)
	}
	if events[0].Title != "Denver Session (moved)" {
		t.Errorf("title is %q, want the updated one", events[0].Title)
	}
	if events[0].PeerAddress != peer.Address {
		t.Errorf("the row does not carry its peer's address: %q", events[0].PeerAddress)
	}
	if events[0].Location.Latitude == nil || *events[0].Location.Latitude != lat {
		t.Errorf("coordinates did not survive: %+v", events[0].Location)
	}

	// A place filter reaches remote events the same way it reaches local ones.
	filtered, _, err := env.Store.ListRemoteEvents(ctx, store.RemoteEventFilter{
		Now: now, Place: store.PlaceFilter{Locality: "denver"}, Page: store.Page{Limit: 50},
	})
	if err != nil {
		t.Fatalf("filtering remote events by place: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("a locality filter matched %d remote events, want 1", len(filtered))
	}

	// A tombstone for something held removes it; one for something never
	// held reports that it did nothing.
	ok, err := env.Store.DeleteRemoteEvent(ctx, peer.ID, "remote-1")
	if err != nil {
		t.Fatalf("deleting a remote event: %v", err)
	}
	if !ok {
		t.Error("deleting a held event reported that nothing was held")
	}
	ok, err = env.Store.DeleteRemoteEvent(ctx, peer.ID, "never-seen")
	if err != nil {
		t.Fatalf("deleting an unknown remote event: %v", err)
	}
	if ok {
		t.Error("deleting an unknown event reported that something was held")
	}
}

// Forgetting a peer takes its queue and everything it sent with it.
func TestRemovingAPeerTakesItsDataWithIt(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	peer := newPeer(t, env, "tinku@example.test")
	now := env.clock()

	if err := env.Store.EnqueueDelivery(ctx, peer.ID, "event-1", []byte("payload"), now); err != nil {
		t.Fatalf("enqueueing: %v", err)
	}
	if err := env.Store.UpsertRemoteEvent(ctx, peer.ID, store.RemoteEventInput{
		RemoteID: "remote-1", OriginDomain: "example.test",
		CanonicalURL: "https://example.test/events/remote-1", Title: "Something",
		IsOnline: true, StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour),
		Timezone: "UTC",
	}); err != nil {
		t.Fatalf("storing a remote event: %v", err)
	}

	if err := env.Store.DeletePeer(ctx, peer.ID); err != nil {
		t.Fatalf("deleting the peer: %v", err)
	}
	events, _, err := env.Store.ListRemoteEvents(ctx, store.RemoteEventFilter{Now: now, Page: store.Page{Limit: 50}})
	if err != nil {
		t.Fatalf("listing remote events: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("%d remote events outlived their peer", len(events))
	}
}

// The limiter is atomic.
//
// This is the property the single-statement form exists for. The API has to
// scale horizontally, so the limit cannot be enforced by anything held in
// one process — it has to hold when N callers consume at once, which a
// read-then-write across two statements does not.
//
// On SQLite the driver serializes writers, so this passes trivially there.
// It is the Postgres run (`./tools.sh test-pg`) that actually exercises it.
func TestTheLimiterHoldsUnderConcurrency(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	peer := newPeer(t, env, "tinku@example.test")

	const limit = 10
	const callers = 24
	now := env.clock()

	var wg sync.WaitGroup
	verdicts := make([]store.RateVerdict, callers)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Each asks for one, so the total granted must be exactly the
			// limit — no rounding to hide behind.
			verdicts[i], errs[i] = env.Store.ConsumePeerAllowance(ctx, peer.ID, 1, limit, now)
		}(i)
	}
	wg.Wait()

	var granted, refused int64
	for i := range verdicts {
		if errs[i] != nil {
			t.Fatalf("caller %d failed: %v", i, errs[i])
		}
		granted += verdicts[i].Allowed
		refused += verdicts[i].Refused
	}

	if granted != limit {
		t.Errorf("%d callers against a limit of %d were granted %d; the limiter is not atomic",
			callers, limit, granted)
	}
	if refused != callers-limit {
		t.Errorf("refused %d, want %d", refused, callers-limit)
	}

	// The row agrees with what the callers were told.
	after, err := env.Store.PeerByID(ctx, peer.ID)
	if err != nil {
		t.Fatalf("reading the peer: %v", err)
	}
	if after.RateLimitedTotal != refused {
		t.Errorf("the peer records %d refused, but callers were refused %d",
			after.RateLimitedTotal, refused)
	}
}

// The same, for one organization inside a peer.
func TestTheOriginLimiterHoldsUnderConcurrency(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	peer := newPeer(t, env, "tinku@example.test")

	const limit = 5
	const callers = 20
	now := env.clock()

	var wg sync.WaitGroup
	verdicts := make([]store.RateVerdict, callers)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			verdicts[i], errs[i] = env.Store.ConsumeOriginAllowance(
				ctx, peer.ID, "Loud Chess Club", 1, limit, now)
		}(i)
	}
	wg.Wait()

	var granted int64
	for i := range verdicts {
		if errs[i] != nil {
			t.Fatalf("caller %d failed: %v", i, errs[i])
		}
		granted += verdicts[i].Allowed
	}
	if granted != limit {
		t.Errorf("%d callers against a limit of %d were granted %d; the limiter is not atomic",
			callers, limit, granted)
	}
}
