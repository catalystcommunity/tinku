package server_test

import (
	"context"
	"sync"
	"testing"
	"time"

	regularrp "github.com/catalystcommunity/linkkeys/sdks/regular-rp/go"
	api "github.com/catalystcommunity/linkkeys/sdks/regular-rp/go/generated"
	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/federation"
	"github.com/catalystcommunity/tinku/api/internal/store"
	"github.com/catalystcommunity/tinku/api/internal/transport"
)

// The real (linkkeys application-key) federation signing scheme, exercised
// against a real HTTP handler and a real SQLite database — the same
// production code paths federation_test.go uses for the dev scheme, with
// one substitution: federation.LocalKeyring signs, and
// federation.ApplicationKeyVerifier (backed by a fake PeerKeyResolver, so
// no live linkkeys deployment is needed) verifies. What the fake answers is
// exactly what a real regularrp.CachedResolver would have handed back
// after its own verification — see fakePeerResolver's own doc comment.

// fakePeerResolver stands in for a *regularrp.CachedResolver backed by a
// live RP and linkkeys home domain, for exactly the reason
// api/internal/federation/verify_test.go's own fakeResolver does: what
// THIS package's tests need to prove is that the federation SERVICE uses a
// resolved key set correctly (verify before decode, identity stored on the
// peer row rather than trusted from the wire, approval applied after
// verification, replay protection unaffected by the signing scheme) — not
// re-derive attestation/revocation/temporal verification, which is
// regularrp's job and is proved against sdks/regular-rp/conformance/ there.
type fakePeerResolver struct {
	mu      sync.Mutex
	results map[regularrp.InstanceRef]regularrp.ResolveResult
	errs    map[regularrp.InstanceRef]error
}

func newFakePeerResolver() *fakePeerResolver {
	return &fakePeerResolver{
		results: map[regularrp.InstanceRef]regularrp.ResolveResult{},
		errs:    map[regularrp.InstanceRef]error{},
	}
}

func (f *fakePeerResolver) set(instance regularrp.InstanceRef, keys regularrp.VerifiedApplicationKeySet) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[instance] = regularrp.ResolveResult{Keys: keys, Freshness: regularrp.FreshnessFresh, FetchedAt: time.Now()}
	delete(f.errs, instance)
}

func (f *fakePeerResolver) Resolve(_ context.Context, instance regularrp.InstanceRef, _ *int64) (regularrp.ResolveResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.errs[instance]; ok {
		return regularrp.ResolveResult{}, err
	}
	if result, ok := f.results[instance]; ok {
		return result, nil
	}
	return regularrp.ResolveResult{}, errNoFakeResult
}

var errNoFakeResult = fakeResolverError("fake resolver: no result configured for this instance")

type fakeResolverError string

func (e fakeResolverError) Error() string { return string(e) }

var _ federation.PeerKeyResolver = (*fakePeerResolver)(nil)

// usableKeys builds a VerifiedApplicationKeySet where every currently
// active key of kr is KeyStatusUsable — a fake RP's answer for a peer
// whose current keyring is exactly this.
func usableKeys(kr *federation.LocalKeyring) regularrp.VerifiedApplicationKeySet {
	set := regularrp.VerifiedApplicationKeySet{RevokedKeyIDs: map[string]bool{}}
	for _, k := range kr.Keys() {
		if !k.Active {
			continue
		}
		set.Keys = append(set.Keys, regularrp.VerifiedApplicationKey{
			Attestation: api.ApplicationKeyAttestation{
				KeyId: k.KeyID, KeyUsage: regularrp.KeyUsageSign, Algorithm: "ed25519",
				PublicKey: append([]byte{}, k.PublicKey[:]...),
			},
			Status: regularrp.KeyStatus{Kind: regularrp.KeyStatusUsable},
		})
	}
	return set
}

func revokedKeys(kr *federation.LocalKeyring, keyID string) regularrp.VerifiedApplicationKeySet {
	set := usableKeys(kr)
	for i := range set.Keys {
		if set.Keys[i].Attestation.KeyId == keyID {
			set.Keys[i].Status = regularrp.KeyStatus{Kind: regularrp.KeyStatusRevoked, RevokedAt: "2020-01-01T00:00:00Z"}
		}
	}
	set.RevokedKeyIDs[keyID] = true
	return set
}

func expiredAttestationKeys(kr *federation.LocalKeyring, keyID string) regularrp.VerifiedApplicationKeySet {
	set := usableKeys(kr)
	for i := range set.Keys {
		if set.Keys[i].Attestation.KeyId == keyID {
			set.Keys[i].Status = regularrp.KeyStatus{Kind: regularrp.KeyStatusAttestationExpired}
		}
	}
	return set
}

// applicationKeyEnv boots one real instance (the directory / receiver)
// wired to the real signing scheme, its Verifier backed by resolver.
type applicationKeyEnv struct {
	*testEnv
	resolver *fakePeerResolver
}

func newApplicationKeyEnv(t *testing.T) *applicationKeyEnv {
	t.Helper()
	env := newLocalTestEnv(t)
	resolver := newFakePeerResolver()
	signer, err := federation.NewKeyring("tinku@directory.test", federation.RecommendedSigningKeys, env.clock())
	if err != nil {
		t.Fatalf("building the directory's own keyring: %v", err)
	}
	verifier := &federation.ApplicationKeyVerifier{Resolver: resolver, Context: federation.BatchSignatureTag}
	peeringVerifier := &federation.ApplicationKeyVerifier{Resolver: resolver, Context: federation.PeeringSignatureTag}
	env.EnableFederationFull(t, signer, verifier, peeringVerifier)
	return &applicationKeyEnv{testEnv: env, resolver: resolver}
}

// approvedPeer creates a peer row for address, with inbound approved and
// its canonical identity already stored — the state DeliverEvents requires
// before it will apply anything a peer sends.
func (e *applicationKeyEnv) approvedPeer(t *testing.T, address string, identity regularrp.InstanceRef) *store.Peer {
	t.Helper()
	ctx := context.Background()
	handle, domain, _ := cutAddress(address)
	peer, err := e.Store.CreatePeer(ctx, store.PeerInput{
		Address: address, Handle: handle, Domain: domain,
		Identity: store.PeerIdentity{
			SubjectUserID: identity.SubjectUserID, SubjectDomain: identity.SubjectDomain,
			ApplicationID: identity.ApplicationID, InstanceID: identity.InstanceID,
		},
		BaseURL: "https://" + domain,
	})
	if err != nil {
		t.Fatalf("creating peer %s: %v", address, err)
	}
	approved := store.PeerStatusApproved
	if _, err := e.Store.SetPeerStatus(ctx, peer.ID, &approved, nil, nil); err != nil {
		t.Fatalf("approving inbound for %s: %v", address, err)
	}
	return peer
}

// deliverAs signs body's EventBatch with signer under the batch context and
// posts it, returning the raw response so a test can check success or a
// specific refusal.
func deliverAs(t *testing.T, e *applicationKeyEnv, senderAddress string, signer *federation.LocalKeyring, batch csil.EventBatch) transport.RpcResponse {
	t.Helper()
	body := csil.EncodeEventBatch(batch)
	signature, keyID, err := signer.Sign(context.Background(), body)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return e.call(t, e.newClient(t), "federation", "deliver-events",
		csil.EncodeSignedDelivery(csil.SignedDelivery{
			SenderAddress: senderAddress,
			Algorithm:     federation.ApplicationKeyAlgorithm,
			KeyId:         keyID,
			Signature:     signature,
			Body:          body,
		}))
}

func testBatch(originDomain, batchID string, sentAt time.Time, events ...csil.FederatedEvent) csil.EventBatch {
	return csil.EventBatch{OriginDomain: originDomain, BatchId: batchID, SentAt: sentAt, Events: events}
}

func testEvent(remoteID string, sentAt time.Time) csil.FederatedEvent {
	return csil.FederatedEvent{
		RemoteId: remoteID, CanonicalUrl: "https://publisher.test/events/" + remoteID,
		Title: "An event", IsOnline: true,
		StartsAt: sentAt.Add(time.Hour), EndsAt: sentAt.Add(2 * time.Hour), Timezone: "UTC",
	}
}

func TestApplicationKeyBatchRoundTrip(t *testing.T) {
	env := newApplicationKeyEnv(t)
	kr, err := federation.NewKeyring("tinku@publisher.test", federation.RecommendedSigningKeys, env.clock())
	if err != nil {
		t.Fatalf("building the publisher's keyring: %v", err)
	}
	instance := regularrp.InstanceRef{SubjectUserID: "user-a", SubjectDomain: "publisher.test", ApplicationID: "tinku", InstanceID: "instance-a"}
	env.approvedPeer(t, "tinku@publisher.test", instance)
	env.resolver.set(instance, usableKeys(kr))

	resp := deliverAs(t, env, "tinku@publisher.test", kr,
		testBatch("publisher.test", store.NewID(), env.clock(), testEvent("event-1", env.clock())))
	requireReply(t, resp, "DeliveryReceipt", "federation/deliver-events")

	events, _, err := env.Store.ListRemoteEvents(context.Background(), store.RemoteEventFilter{Now: env.clock(), Page: store.Page{Limit: 10}})
	if err != nil {
		t.Fatalf("listing remote events: %v", err)
	}
	if len(events) != 1 || events[0].RemoteID != "event-1" {
		t.Fatalf("directory holds %v, want exactly event-1", events)
	}
}

func TestApplicationKeyBatchRefusedWithNoAttestation(t *testing.T) {
	env := newApplicationKeyEnv(t)
	kr, err := federation.NewKeyring("tinku@publisher.test", federation.RecommendedSigningKeys, env.clock())
	if err != nil {
		t.Fatalf("building the publisher's keyring: %v", err)
	}
	instance := regularrp.InstanceRef{SubjectUserID: "user-a", SubjectDomain: "publisher.test", ApplicationID: "tinku", InstanceID: "instance-a"}
	env.approvedPeer(t, "tinku@publisher.test", instance)
	// Deliberately NOT registered with the resolver: the key exists
	// locally but was never enrolled/attested anywhere this instance can see.
	env.resolver.set(instance, regularrp.VerifiedApplicationKeySet{RevokedKeyIDs: map[string]bool{}})

	resp := deliverAs(t, env, "tinku@publisher.test", kr,
		testBatch("publisher.test", store.NewID(), env.clock(), testEvent("event-1", env.clock())))
	requireServiceError(t, resp, 3, "a key with no attestation")

	events, _, _ := env.Store.ListRemoteEvents(context.Background(), store.RemoteEventFilter{Now: env.clock(), Page: store.Page{Limit: 10}})
	if len(events) != 0 {
		t.Errorf("an unattested key's batch stored %d events", len(events))
	}
}

func TestApplicationKeyBatchRevokedKeyRefusedAfterEffectiveTime(t *testing.T) {
	env := newApplicationKeyEnv(t)
	kr, err := federation.NewKeyring("tinku@publisher.test", federation.RecommendedSigningKeys, env.clock())
	if err != nil {
		t.Fatalf("building the publisher's keyring: %v", err)
	}
	// Determinism: retire every key but one, so every signature this
	// keyring makes names the SAME key id.
	keys := kr.Keys()
	for _, k := range keys[1:] {
		if err := kr.RetireKey(k.KeyID, env.clock()); err != nil {
			t.Fatalf("retiring: %v", err)
		}
	}
	activeKeyID := keys[0].KeyID

	instance := regularrp.InstanceRef{SubjectUserID: "user-a", SubjectDomain: "publisher.test", ApplicationID: "tinku", InstanceID: "instance-a"}
	env.approvedPeer(t, "tinku@publisher.test", instance)

	// Before revocation: accepted.
	env.resolver.set(instance, usableKeys(kr))
	resp := deliverAs(t, env, "tinku@publisher.test", kr,
		testBatch("publisher.test", store.NewID(), env.clock(), testEvent("event-1", env.clock())))
	requireReply(t, resp, "DeliveryReceipt", "before revocation")

	// After revocation: the SAME key, refused.
	env.resolver.set(instance, revokedKeys(kr, activeKeyID))
	resp = deliverAs(t, env, "tinku@publisher.test", kr,
		testBatch("publisher.test", store.NewID(), env.clock(), testEvent("event-2", env.clock())))
	requireServiceError(t, resp, 3, "after revocation")

	events, _, _ := env.Store.ListRemoteEvents(context.Background(), store.RemoteEventFilter{Now: env.clock(), Page: store.Page{Limit: 10}})
	if len(events) != 1 {
		t.Errorf("the directory holds %d events, want exactly the one accepted before revocation", len(events))
	}
}

// TestApplicationKeyRotationOverlap: an old key and a new key of the SAME
// peer are both currently attested at once, and both verify — the overlap
// that makes rotation safe.
func TestApplicationKeyRotationOverlap(t *testing.T) {
	env := newApplicationKeyEnv(t)
	oldKr, err := federation.NewKeyring("tinku@publisher.test", federation.RecommendedSigningKeys, env.clock())
	if err != nil {
		t.Fatalf("building the old keyring: %v", err)
	}
	newKr, err := federation.NewKeyring("tinku@publisher.test", federation.RecommendedSigningKeys, env.clock())
	if err != nil {
		t.Fatalf("building the new keyring: %v", err)
	}
	instance := regularrp.InstanceRef{SubjectUserID: "user-a", SubjectDomain: "publisher.test", ApplicationID: "tinku", InstanceID: "instance-a"}
	env.approvedPeer(t, "tinku@publisher.test", instance)

	// The RP reports BOTH keyrings' keys as currently attested — the
	// overlap window during a rotation.
	both := usableKeys(oldKr)
	both.Keys = append(both.Keys, usableKeys(newKr).Keys...)
	env.resolver.set(instance, both)

	resp := deliverAs(t, env, "tinku@publisher.test", oldKr,
		testBatch("publisher.test", store.NewID(), env.clock(), testEvent("event-old", env.clock())))
	requireReply(t, resp, "DeliveryReceipt", "the old key, during overlap")

	resp = deliverAs(t, env, "tinku@publisher.test", newKr,
		testBatch("publisher.test", store.NewID(), env.clock(), testEvent("event-new", env.clock())))
	requireReply(t, resp, "DeliveryReceipt", "the new key, during overlap")

	events, _, _ := env.Store.ListRemoteEvents(context.Background(), store.RemoteEventFilter{Now: env.clock(), Page: store.Page{Limit: 10}})
	if len(events) != 2 {
		t.Errorf("the directory holds %d events, want 2 (one from each overlapping key)", len(events))
	}
}

// TestApplicationKeyExpiredAttestationIsNotPermanent: refused while the RP
// reports the attestation expired, accepted again once the RP reports the
// SAME key renewed. An expired attestation is a missing proof, not a
// revocation, and this instance must not remember a refusal as though it
// were one.
func TestApplicationKeyExpiredAttestationIsNotPermanent(t *testing.T) {
	env := newApplicationKeyEnv(t)
	kr, err := federation.NewKeyring("tinku@publisher.test", federation.RecommendedSigningKeys, env.clock())
	if err != nil {
		t.Fatalf("building the publisher's keyring: %v", err)
	}
	keys := kr.Keys()
	for _, k := range keys[1:] {
		if err := kr.RetireKey(k.KeyID, env.clock()); err != nil {
			t.Fatalf("retiring: %v", err)
		}
	}
	activeKeyID := keys[0].KeyID
	instance := regularrp.InstanceRef{SubjectUserID: "user-a", SubjectDomain: "publisher.test", ApplicationID: "tinku", InstanceID: "instance-a"}
	env.approvedPeer(t, "tinku@publisher.test", instance)

	env.resolver.set(instance, expiredAttestationKeys(kr, activeKeyID))
	resp := deliverAs(t, env, "tinku@publisher.test", kr,
		testBatch("publisher.test", store.NewID(), env.clock(), testEvent("event-1", env.clock())))
	requireServiceError(t, resp, 3, "while the attestation is expired")

	env.resolver.set(instance, usableKeys(kr))
	resp = deliverAs(t, env, "tinku@publisher.test", kr,
		testBatch("publisher.test", store.NewID(), env.clock(), testEvent("event-2", env.clock())))
	requireReply(t, resp, "DeliveryReceipt", "after the attestation is renewed")
}

// TestApplicationKeyApprovalAppliedAfterVerification: a cryptographically
// VALID batch from a peer whose inbound status is not approved is still
// refused. Verification succeeding must never stand in for approval.
func TestApplicationKeyApprovalAppliedAfterVerification(t *testing.T) {
	env := newApplicationKeyEnv(t)
	kr, err := federation.NewKeyring("tinku@publisher.test", federation.RecommendedSigningKeys, env.clock())
	if err != nil {
		t.Fatalf("building the publisher's keyring: %v", err)
	}
	instance := regularrp.InstanceRef{SubjectUserID: "user-a", SubjectDomain: "publisher.test", ApplicationID: "tinku", InstanceID: "instance-a"}

	// Recorded, but never approved.
	ctx := context.Background()
	peer, err := env.Store.CreatePeer(ctx, store.PeerInput{
		Address: "tinku@publisher.test", Handle: "tinku", Domain: "publisher.test",
		Identity: store.PeerIdentity{
			SubjectUserID: instance.SubjectUserID, SubjectDomain: instance.SubjectDomain,
			ApplicationID: instance.ApplicationID, InstanceID: instance.InstanceID,
		},
		BaseURL: "https://publisher.test",
	})
	if err != nil {
		t.Fatalf("creating the peer: %v", err)
	}
	if peer.InboundStatus != store.PeerStatusNone {
		t.Fatalf("a freshly created peer has inbound status %s, want none", peer.InboundStatus)
	}
	env.resolver.set(instance, usableKeys(kr)) // the key is perfectly valid

	resp := deliverAs(t, env, "tinku@publisher.test", kr,
		testBatch("publisher.test", store.NewID(), env.clock(), testEvent("event-1", env.clock())))
	requireServiceError(t, resp, 3, "a cryptographically valid batch from an unapproved peer")

	events, _, _ := env.Store.ListRemoteEvents(ctx, store.RemoteEventFilter{Now: env.clock(), Page: store.Page{Limit: 10}})
	if len(events) != 0 {
		t.Errorf("an unapproved peer's valid batch stored %d events", len(events))
	}
}

// TestApplicationKeyHandleChangeDoesNotTransferApproval: this instance
// approved a peer under one canonical identity. A batch signed under a
// DIFFERENT identity, claiming the SAME address, is refused — because
// verification is checked against the STORED identity, never re-derived
// from the address.
func TestApplicationKeyHandleChangeDoesNotTransferApproval(t *testing.T) {
	env := newApplicationKeyEnv(t)
	original := regularrp.InstanceRef{SubjectUserID: "original-user", SubjectDomain: "publisher.test", ApplicationID: "tinku", InstanceID: "original-instance"}
	env.approvedPeer(t, "tinku@publisher.test", original)

	// A different account's keyring, attested under a DIFFERENT identity —
	// as if the address `tinku@publisher.test` were reassigned.
	impostor, err := federation.NewKeyring("tinku@publisher.test", federation.RecommendedSigningKeys, env.clock())
	if err != nil {
		t.Fatalf("building the impostor's keyring: %v", err)
	}
	newIdentity := regularrp.InstanceRef{SubjectUserID: "new-user", SubjectDomain: "publisher.test", ApplicationID: "tinku", InstanceID: "new-instance"}
	env.resolver.set(newIdentity, usableKeys(impostor))
	// The ORIGINAL identity's resolver entry has none of the impostor's
	// keys — exactly what a real RP would answer, since the impostor was
	// never attested under the original identity.
	env.resolver.set(original, regularrp.VerifiedApplicationKeySet{RevokedKeyIDs: map[string]bool{}})

	resp := deliverAs(t, env, "tinku@publisher.test", impostor,
		testBatch("publisher.test", store.NewID(), env.clock(), testEvent("event-1", env.clock())))
	requireServiceError(t, resp, 3, "a batch from a different identity at the same address")

	events, _, _ := env.Store.ListRemoteEvents(context.Background(), store.RemoteEventFilter{Now: env.clock(), Page: store.Page{Limit: 10}})
	if len(events) != 0 {
		t.Errorf("a handle-change impostor's batch stored %d events", len(events))
	}
}

func TestApplicationKeyReplayedBatchIsRefused(t *testing.T) {
	env := newApplicationKeyEnv(t)
	kr, err := federation.NewKeyring("tinku@publisher.test", federation.RecommendedSigningKeys, env.clock())
	if err != nil {
		t.Fatalf("building the publisher's keyring: %v", err)
	}
	instance := regularrp.InstanceRef{SubjectUserID: "user-a", SubjectDomain: "publisher.test", ApplicationID: "tinku", InstanceID: "instance-a"}
	env.approvedPeer(t, "tinku@publisher.test", instance)
	env.resolver.set(instance, usableKeys(kr))

	body := csil.EncodeEventBatch(testBatch("publisher.test", store.NewID(), env.clock(), testEvent("event-1", env.clock())))
	signature, keyID, err := kr.Sign(context.Background(), body)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	envelope := csil.EncodeSignedDelivery(csil.SignedDelivery{
		SenderAddress: "tinku@publisher.test", Algorithm: federation.ApplicationKeyAlgorithm,
		KeyId: keyID, Signature: signature, Body: body,
	})

	first := env.call(t, env.newClient(t), "federation", "deliver-events", envelope)
	requireReply(t, first, "DeliveryReceipt", "the first delivery")

	// The EXACT same envelope, byte for byte, sent again.
	second := env.call(t, env.newClient(t), "federation", "deliver-events", envelope)
	requireServiceError(t, second, 1, "a replayed envelope")

	events, _, _ := env.Store.ListRemoteEvents(context.Background(), store.RemoteEventFilter{Now: env.clock(), Page: store.Page{Limit: 10}})
	if len(events) != 1 {
		t.Errorf("a replayed batch changed the directory's holdings to %d events, want 1", len(events))
	}
}

// peeringRequestAs signs a PeeringBody under PeeringSignatureTag and posts
// it as a SignedPeeringRequest, claiming identity for the sender.
func peeringRequestAs(t *testing.T, e *applicationKeyEnv, senderAddress string, signer *federation.LocalKeyring, identity regularrp.InstanceRef, baseURL string) transport.RpcResponse {
	t.Helper()
	body := csil.EncodePeeringBody(csil.PeeringBody{BaseUrl: baseURL, RequestedAt: e.clock()})
	signature, keyID, err := signer.SignWithContext(context.Background(), federation.PeeringSignatureTag, body)
	if err != nil {
		t.Fatalf("signing the peering request: %v", err)
	}
	return e.call(t, e.newClient(t), "federation", "request-peering",
		csil.EncodeSignedPeeringRequest(csil.SignedPeeringRequest{
			SenderAddress: senderAddress, Algorithm: federation.ApplicationKeyAlgorithm, KeyId: keyID,
			SubjectUserId: identity.SubjectUserID, SubjectDomain: identity.SubjectDomain,
			ApplicationId: identity.ApplicationID, InstanceId: identity.InstanceID,
			Signature: signature, Body: body,
		}))
}

// TestPeeringRequestCapturesIdentityOnFirstContact: an unknown sender's
// FIRST signed peering request is accepted as pending, and its claimed
// canonical identity is stored on the new peer row — with no prior peer
// row to have trusted anything in advance.
func TestPeeringRequestCapturesIdentityOnFirstContact(t *testing.T) {
	env := newApplicationKeyEnv(t)
	kr, err := federation.NewKeyring("tinku@newcomer.test", federation.RecommendedSigningKeys, env.clock())
	if err != nil {
		t.Fatalf("building the newcomer's keyring: %v", err)
	}
	identity := regularrp.InstanceRef{SubjectUserID: "user-new", SubjectDomain: "newcomer.test", ApplicationID: "tinku", InstanceID: "instance-new"}
	env.resolver.set(identity, usableKeys(kr))

	resp := peeringRequestAs(t, env, "tinku@newcomer.test", kr, identity, "https://newcomer.test")
	requireReply(t, resp, "PeeringReceipt", "a first-contact peering request")

	peer, err := env.Store.PeerByAddress(context.Background(), "tinku@newcomer.test")
	if err != nil {
		t.Fatalf("reading the new peer: %v", err)
	}
	if peer.InboundStatus != store.PeerStatusPending {
		t.Errorf("inbound status is %s, want pending", peer.InboundStatus)
	}
	if peer.Identity.SubjectUserID != identity.SubjectUserID || peer.Identity.InstanceID != identity.InstanceID {
		t.Errorf("stored identity is %+v, want it to match the verified claim %+v", peer.Identity, identity)
	}
}

// TestPeeringRequestDoesNotOverwriteAnExistingPeersIdentity: a peering
// request from an address that already has a recorded peer must not
// replace that peer's stored identity, even though ITS OWN signature
// verifies fine for the new identity it claims. Otherwise a second signed
// request would be how a handle change silently inherited whatever the
// first identity had already been granted.
func TestPeeringRequestDoesNotOverwriteAnExistingPeersIdentity(t *testing.T) {
	env := newApplicationKeyEnv(t)
	original := regularrp.InstanceRef{SubjectUserID: "original-user", SubjectDomain: "publisher.test", ApplicationID: "tinku", InstanceID: "original-instance"}
	env.approvedPeer(t, "tinku@publisher.test", original)

	impostor, err := federation.NewKeyring("tinku@publisher.test", federation.RecommendedSigningKeys, env.clock())
	if err != nil {
		t.Fatalf("building the impostor's keyring: %v", err)
	}
	newIdentity := regularrp.InstanceRef{SubjectUserID: "new-user", SubjectDomain: "publisher.test", ApplicationID: "tinku", InstanceID: "new-instance"}
	env.resolver.set(newIdentity, usableKeys(impostor))

	resp := peeringRequestAs(t, env, "tinku@publisher.test", impostor, newIdentity, "https://publisher.test")
	// The request itself verifies (the impostor really does hold a
	// currently attested key for newIdentity), so the answer is still
	// "pending" — RequestPeering never refuses a well-formed request from
	// an unknown claimant. What matters is what got STORED.
	requireReply(t, resp, "PeeringReceipt", "a peering request claiming a new identity for a known address")

	peer, err := env.Store.PeerByAddress(context.Background(), "tinku@publisher.test")
	if err != nil {
		t.Fatalf("reading the peer: %v", err)
	}
	if peer.Identity.SubjectUserID != original.SubjectUserID || peer.Identity.InstanceID != original.InstanceID {
		t.Errorf("the peer's identity changed to %+v; it must stay %+v", peer.Identity, original)
	}
	if peer.InboundStatus != store.PeerStatusApproved {
		t.Errorf("inbound status is %s, want it to stay approved", peer.InboundStatus)
	}
}

// TestApprovingInboundRequiresAnIdentity exercises SetPeerStatus itself
// (the admin RPC, not the store): approving inbound for a peer with no
// canonical identity is refused, and supplying the identity in the SAME
// request succeeds.
func TestApprovingInboundRequiresAnIdentity(t *testing.T) {
	env := newApplicationKeyEnv(t)
	admin, profile := env.login(t, "ada")
	env.makeAdmin(t, string(profile.Id))

	resp := env.call(t, admin, "federation", "add-peer", csil.EncodeAddPeerRequest(csil.AddPeerRequest{
		Address: "tinku@nopeer.test", BaseUrl: "https://nopeer.test",
	}))
	requireReply(t, resp, "Peer", "add-peer")
	peer, err := csil.DecodePeer(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Peer: %v", err)
	}

	approved := csil.PeerStatus(store.PeerStatusApproved)
	resp = env.call(t, admin, "federation", "set-peer-status", csil.EncodeSetPeerStatusRequest(csil.SetPeerStatusRequest{
		PeerId: peer.Id, InboundStatus: &approved,
	}))
	requireServiceError(t, resp, 1, "approving inbound with no identity")

	userID, domain, appID, instanceID := "u1", "nopeer.test", "tinku", "i1"
	resp = env.call(t, admin, "federation", "set-peer-status", csil.EncodeSetPeerStatusRequest(csil.SetPeerStatusRequest{
		PeerId: peer.Id, InboundStatus: &approved,
		SubjectUserId: &userID, SubjectDomain: &domain, ApplicationId: &appID, InstanceId: &instanceID,
	}))
	requireReply(t, resp, "Peer", "approving inbound with an identity supplied")
	updated, err := csil.DecodePeer(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Peer: %v", err)
	}
	if updated.InboundStatus != approved {
		t.Errorf("inbound status is %s, want approved", updated.InboundStatus)
	}
	if updated.SubjectUserId == nil || *updated.SubjectUserId != userID {
		t.Errorf("subject_user_id is %v, want %q", updated.SubjectUserId, userID)
	}
}
