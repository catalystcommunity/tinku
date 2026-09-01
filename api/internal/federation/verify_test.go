package federation

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	regularrp "github.com/catalystcommunity/linkkeys/sdks/regular-rp/go"
	api "github.com/catalystcommunity/linkkeys/sdks/regular-rp/go/generated"
)

// fakeResolver stands in for a *regularrp.CachedResolver backed by a live
// RP and a live linkkeys home domain. It returns exactly the
// already-verified VerifiedApplicationKeySet a test configures for one
// InstanceRef — never a signed-bytes fixture that would need real domain
// keys to authenticate. That is deliberate: VerifyApplicationKeySet's own
// rules (attestation verification, revocation quorum, temporal
// classification) are regularrp's job and are proved against
// sdks/regular-rp/conformance/ there. What THIS package's tests need to
// prove is that ApplicationKeyVerifier and the federation service wiring
// around it use that result correctly — not re-derive it.
type fakeResolver struct {
	results map[regularrp.InstanceRef]regularrp.ResolveResult
	errs    map[regularrp.InstanceRef]error
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{
		results: map[regularrp.InstanceRef]regularrp.ResolveResult{},
		errs:    map[regularrp.InstanceRef]error{},
	}
}

func (f *fakeResolver) set(instance regularrp.InstanceRef, keys regularrp.VerifiedApplicationKeySet) {
	f.results[instance] = regularrp.ResolveResult{Keys: keys, Freshness: regularrp.FreshnessFresh, FetchedAt: time.Now()}
	delete(f.errs, instance)
}

func (f *fakeResolver) fail(instance regularrp.InstanceRef, err error) {
	f.errs[instance] = err
	delete(f.results, instance)
}

func (f *fakeResolver) Resolve(_ context.Context, instance regularrp.InstanceRef, _ *int64) (regularrp.ResolveResult, error) {
	if err, ok := f.errs[instance]; ok {
		return regularrp.ResolveResult{}, err
	}
	if result, ok := f.results[instance]; ok {
		return result, nil
	}
	return regularrp.ResolveResult{}, errors.New("fake resolver: no result configured for this instance")
}

var _ PeerKeyResolver = (*fakeResolver)(nil)

// testInstance builds a deterministic InstanceRef for one test.
func testInstance(name string) regularrp.InstanceRef {
	return regularrp.InstanceRef{
		SubjectUserID: "user-" + name, SubjectDomain: name + ".test",
		ApplicationID: "tinku", InstanceID: "instance-" + name,
	}
}

// usableKeySet builds a VerifiedApplicationKeySet where every key kr
// currently holds active is KeyStatusUsable — a fake RP's answer for a peer
// whose current keyring is exactly this.
func usableKeySet(kr *LocalKeyring, extra ...regularrp.VerifiedApplicationKey) regularrp.VerifiedApplicationKeySet {
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
	set.Keys = append(set.Keys, extra...)
	return set
}

// revokedKeySet is usableKeySet with keyID additionally marked revoked.
func revokedKeySet(kr *LocalKeyring, keyID string) regularrp.VerifiedApplicationKeySet {
	set := usableKeySet(kr)
	for i := range set.Keys {
		if set.Keys[i].Attestation.KeyId == keyID {
			set.Keys[i].Status = regularrp.KeyStatus{Kind: regularrp.KeyStatusRevoked, RevokedAt: "2020-01-01T00:00:00Z"}
		}
	}
	set.RevokedKeyIDs[keyID] = true
	return set
}

// expiredAttestationKeySet is usableKeySet with keyID marked as having an
// expired attestation — a MISSING PROOF, not a revocation: KeyForUse
// returns a different error, and it is NOT in RevokedKeyIDs.
func expiredAttestationKeySet(kr *LocalKeyring, keyID string) regularrp.VerifiedApplicationKeySet {
	set := usableKeySet(kr)
	for i := range set.Keys {
		if set.Keys[i].Attestation.KeyId == keyID {
			set.Keys[i].Status = regularrp.KeyStatus{Kind: regularrp.KeyStatusAttestationExpired}
		}
	}
	return set
}

// forceSign signs body with EXACTLY keyID, bypassing Sign's own
// among-active-keys choice — for a test that needs a specific key used
// deterministically (e.g. "this key, before and after it is revoked").
func (kr *LocalKeyring) forceSign(t *testing.T, keyID string, body []byte) (signature, usedKeyID string) {
	t.Helper()
	signer, ok := kr.SignerFor(keyID)
	if !ok {
		t.Fatalf("no local key %q", keyID)
	}
	message := signatureInput(BatchSignatureTag, body)
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(signer.PrivateKeyBytes), message)
	return base64.RawURLEncoding.EncodeToString(sig), keyID
}

func decodeSigOrFail(t *testing.T, signature string) []byte {
	t.Helper()
	sig, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		t.Fatalf("decoding signature: %v", err)
	}
	return sig
}

func signBatch(t *testing.T, kr *LocalKeyring, body []byte) (signature, keyID string) {
	t.Helper()
	signature, keyID, err := kr.Sign(context.Background(), body)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return signature, keyID
}

func newTestKeyring(t *testing.T, address string) *LocalKeyring {
	t.Helper()
	kr, err := NewKeyring(address, RecommendedSigningKeys, time.Now())
	if err != nil {
		t.Fatalf("generating a keyring: %v", err)
	}
	return kr
}

func identityOf(instance regularrp.InstanceRef) KeyIdentity {
	return KeyIdentity{
		SubjectUserID: instance.SubjectUserID, SubjectDomain: instance.SubjectDomain,
		ApplicationID: instance.ApplicationID, InstanceID: instance.InstanceID,
	}
}

func TestVerifyAcceptsAUsableKey(t *testing.T) {
	kr := newTestKeyring(t, "a@a.test")
	instance := testInstance("a")
	body := []byte("event batch bytes")
	signature, keyID := signBatch(t, kr, body)

	resolver := newFakeResolver()
	resolver.set(instance, usableKeySet(kr))
	verifier := &ApplicationKeyVerifier{Resolver: resolver, Context: BatchSignatureTag}

	if err := verifier.Verify(context.Background(), kr.Address(), identityOf(instance),
		kr.Algorithm(), keyID, body, []byte(signature)); err != nil {
		t.Errorf("a usable key's signature was refused: %v", err)
	}
}

// TestVerifyRefusesAKeyWithNoAttestation: an unknown key id — the fake RP's
// answer contains no attestation for it at all.
func TestVerifyRefusesAKeyWithNoAttestation(t *testing.T) {
	kr := newTestKeyring(t, "a@a.test")
	instance := testInstance("a")
	body := []byte("event batch bytes")

	resolver := newFakeResolver()
	resolver.set(instance, regularrp.VerifiedApplicationKeySet{RevokedKeyIDs: map[string]bool{}}) // no keys at all
	verifier := &ApplicationKeyVerifier{Resolver: resolver, Context: BatchSignatureTag}

	// A real signature (from A's own key) presented against an RP answer
	// that has never heard of that key — the common case for a key that
	// was generated locally but never enrolled.
	signature, keyID := signBatch(t, kr, body)
	if err := verifier.Verify(context.Background(), kr.Address(), identityOf(instance),
		kr.Algorithm(), keyID, body, []byte(signature)); !errors.Is(err, ErrUntrusted) {
		t.Errorf("a key with no attestation verified (err=%v), want ErrUntrusted", err)
	}
}

// TestVerifyRefusesARevokedKeyAfterItsEffectiveTime, and accepts the same
// key before the fake RP reports it revoked — the RP's report IS "as of
// now" from Tinku's point of view, so this models the temporal behavior
// docs/application-keys.md requires without re-testing regularrp's own
// revocation-quorum/temporal logic (proved there).
func TestVerifyRefusesARevokedKeyAfterItsEffectiveTime(t *testing.T) {
	kr := newTestKeyring(t, "a@a.test")
	instance := testInstance("a")
	resolver := newFakeResolver()
	verifier := &ApplicationKeyVerifier{Resolver: resolver, Context: BatchSignatureTag}

	// Before revocation: accepted.
	resolver.set(instance, usableKeySet(kr))
	body1 := []byte("batch one")
	sig1, key1 := signBatch(t, kr, body1)
	if err := verifier.Verify(context.Background(), kr.Address(), identityOf(instance),
		kr.Algorithm(), key1, body1, []byte(sig1)); err != nil {
		t.Fatalf("the key was refused before revocation: %v", err)
	}

	// After revocation: the SAME key, refused.
	resolver.set(instance, revokedKeySet(kr, key1))
	body2 := []byte("batch two")
	sig2, key2 := kr.forceSign(t, key1, body2)
	_ = key2
	if err := verifier.Verify(context.Background(), kr.Address(), identityOf(instance),
		kr.Algorithm(), key1, body2, []byte(sig2)); !errors.Is(err, ErrUntrusted) {
		t.Errorf("a revoked key verified after its effective time (err=%v), want ErrUntrusted", err)
	}
}

// TestVerifyExpiredAttestationIsRefusedButNotPermanent proves an expired
// attestation is a MISSING PROOF, not a revocation: the key is refused
// while the fake RP reports it attestation-expired, and the identical key
// is accepted again once the RP reports a renewed (usable) attestation —
// nothing in this package's own code path permanently blacklists a key
// after one refusal.
func TestVerifyExpiredAttestationIsRefusedButNotPermanent(t *testing.T) {
	kr := newTestKeyring(t, "a@a.test")
	instance := testInstance("a")
	resolver := newFakeResolver()
	verifier := &ApplicationKeyVerifier{Resolver: resolver, Context: BatchSignatureTag}

	resolver.set(instance, expiredAttestationKeySet(kr, kr.Keys()[0].KeyID))
	targetKey := kr.Keys()[0].KeyID
	body1 := []byte("batch while expired")
	sig1, _ := kr.forceSign(t, targetKey, body1)
	if err := verifier.Verify(context.Background(), kr.Address(), identityOf(instance),
		kr.Algorithm(), targetKey, body1, []byte(sig1)); !errors.Is(err, ErrUntrusted) {
		t.Errorf("an expired attestation verified (err=%v), want ErrUntrusted", err)
	}

	// The attestation is renewed: the RP now reports the SAME key usable.
	resolver.set(instance, usableKeySet(kr))
	body2 := []byte("batch after renewal")
	sig2, _ := kr.forceSign(t, targetKey, body2)
	if err := verifier.Verify(context.Background(), kr.Address(), identityOf(instance),
		kr.Algorithm(), targetKey, body2, []byte(sig2)); err != nil {
		t.Errorf("the same key was still refused after its attestation was renewed: %v", err)
	}
}

// TestVerifyRejectsTheWrongSignatureContext is the batch-vs-peering context
// distinctness the batch signature context exists for: a signature made
// for one context must not verify under the other.
func TestVerifyRejectsTheWrongSignatureContext(t *testing.T) {
	kr := newTestKeyring(t, "a@a.test")
	instance := testInstance("a")
	resolver := newFakeResolver()
	resolver.set(instance, usableKeySet(kr))

	body := []byte("some bytes")
	signature, keyID := signBatch(t, kr, body) // signed under BatchSignatureTag

	peeringVerifier := &ApplicationKeyVerifier{Resolver: resolver, Context: PeeringSignatureTag}
	if err := peeringVerifier.Verify(context.Background(), kr.Address(), identityOf(instance),
		kr.Algorithm(), keyID, body, []byte(signature)); !errors.Is(err, ErrUntrusted) {
		t.Errorf("a batch signature verified under the peering context (err=%v), want ErrUntrusted", err)
	}

	batchVerifier := &ApplicationKeyVerifier{Resolver: resolver, Context: BatchSignatureTag}
	if err := batchVerifier.Verify(context.Background(), kr.Address(), identityOf(instance),
		kr.Algorithm(), keyID, body, []byte(signature)); err != nil {
		t.Errorf("the same signature did not verify under its own context: %v", err)
	}
}

func TestVerifyRejectsAnEmptyIdentity(t *testing.T) {
	kr := newTestKeyring(t, "a@a.test")
	resolver := newFakeResolver()
	verifier := &ApplicationKeyVerifier{Resolver: resolver, Context: BatchSignatureTag}
	body := []byte("body")
	signature, keyID := signBatch(t, kr, body)

	if err := verifier.Verify(context.Background(), kr.Address(), KeyIdentity{},
		kr.Algorithm(), keyID, body, []byte(signature)); !errors.Is(err, ErrUntrusted) {
		t.Errorf("an empty identity verified (err=%v), want ErrUntrusted", err)
	}
}

func TestVerifyRejectsWhenTheResolverFails(t *testing.T) {
	kr := newTestKeyring(t, "a@a.test")
	instance := testInstance("a")
	resolver := newFakeResolver()
	resolver.fail(instance, errors.New("the RP is unreachable"))
	verifier := &ApplicationKeyVerifier{Resolver: resolver, Context: BatchSignatureTag}
	body := []byte("body")
	signature, keyID := signBatch(t, kr, body)

	if err := verifier.Verify(context.Background(), kr.Address(), identityOf(instance),
		kr.Algorithm(), keyID, body, []byte(signature)); !errors.Is(err, ErrUntrusted) {
		t.Errorf("verification succeeded despite an unreachable resolver (err=%v), want ErrUntrusted", err)
	}
}
