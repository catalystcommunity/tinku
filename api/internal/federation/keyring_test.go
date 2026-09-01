package federation

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"
)

func TestNewKeyringRefusesFewerThanMinimum(t *testing.T) {
	if _, err := NewKeyring("tinku@example.test", 1, time.Now()); err == nil {
		t.Fatal("a keyring with one key was accepted; MinSigningKeys is 2")
	}
}

func TestNewKeyringRefusesNoAddress(t *testing.T) {
	if _, err := NewKeyring("", RecommendedSigningKeys, time.Now()); err == nil {
		t.Fatal("a keyring with no address was accepted")
	}
}

// TestThreeKeysIsWhatEnablesOrdinarySiblingRevocation is documentation as a
// test: RecommendedSigningKeys is 3 because two keys can never revoke each
// other (the target may not sign its own revocation), so a normal keyring
// generates three. The generator itself does not enforce a maximum, but
// this asserts the constant this repository's whole federation.md guidance
// rests on has not drifted from regularrp's.
func TestThreeKeysIsWhatEnablesOrdinarySiblingRevocation(t *testing.T) {
	kr, err := NewKeyring("tinku@example.test", RecommendedSigningKeys, time.Now())
	if err != nil {
		t.Fatalf("generating a recommended-size keyring: %v", err)
	}
	if got := len(kr.Keys()); got != 3 {
		t.Fatalf("RecommendedSigningKeys produced %d keys, want 3", got)
	}
	// Two OTHER active keys authorize a revocation of the third — exactly
	// AdditionQuorum/RevocationQuorum keys are available with 3 total and
	// one excluded.
	third := kr.Keys()[2].KeyID
	if got := len(kr.ActiveSigners(third)); got < 2 {
		t.Fatalf("only %d other active keys with a keyring of 3; a revocation needs 2", got)
	}
}

// TestSignAndVerifyRoundTrip proves LocalKeyring's own Sign function
// produces something ApplicationKeyVerifier's own Verify function accepts.
// The regularrp SDK's conformance suite proves regularrp agrees with the
// Rust reference; it says nothing about whether THIS package's signing side
// and verifying side agree with each other, which is exactly the class of
// bug (mismatched tag, mismatched encoding) a conformance suite alone won't
// catch — see linkkeys' own sdks/regular-rp/go/keyring_test.go doc comment
// for the same reasoning.
func TestSignAndVerifyRoundTrip(t *testing.T) {
	now := time.Now()
	kr, err := NewKeyring("tinku@example.test", RecommendedSigningKeys, now)
	if err != nil {
		t.Fatalf("generating a keyring: %v", err)
	}
	body := []byte("an event batch, or at least a stand-in for one")

	signature, keyID, err := kr.Sign(context.Background(), body)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	found := false
	for _, k := range kr.Keys() {
		if k.KeyID == keyID {
			found = true
		}
	}
	if !found {
		t.Fatalf("Sign returned key id %q, which is not one of this keyring's own keys", keyID)
	}

	resolver := newFakeResolver()
	instance := testInstance("tinku")
	resolver.set(instance, usableKeySet(kr))
	verifier := &ApplicationKeyVerifier{Resolver: resolver, Context: BatchSignatureTag}

	identity := KeyIdentity{
		SubjectUserID: instance.SubjectUserID, SubjectDomain: instance.SubjectDomain,
		ApplicationID: instance.ApplicationID, InstanceID: instance.InstanceID,
	}
	if err := verifier.Verify(context.Background(), kr.Address(), identity, kr.Algorithm(), keyID,
		body, []byte(signature)); err != nil {
		t.Errorf("a signature this package's own Sign produced did not verify with this package's own Verify: %v", err)
	}
}

// TestSignPicksOnlyActiveKeys proves a retired key is never offered for a
// new signature, by retiring every key but one and checking every
// signature made afterward names that one.
func TestSignPicksOnlyActiveKeys(t *testing.T) {
	now := time.Now()
	kr, err := NewKeyring("tinku@example.test", RecommendedSigningKeys, now)
	if err != nil {
		t.Fatalf("generating a keyring: %v", err)
	}
	keys := kr.Keys()
	survivor := keys[0].KeyID
	for _, k := range keys[1:] {
		if err := kr.RetireKey(k.KeyID, now); err != nil {
			t.Fatalf("retiring %s: %v", k.KeyID, err)
		}
	}
	for i := 0; i < 5; i++ {
		_, keyID, err := kr.Sign(context.Background(), []byte("body"))
		if err != nil {
			t.Fatalf("signing: %v", err)
		}
		if keyID != survivor {
			t.Fatalf("signed with %q, want the only active key %q", keyID, survivor)
		}
	}
}

func TestSignFailsWithNoActiveKeys(t *testing.T) {
	now := time.Now()
	kr, err := NewKeyring("tinku@example.test", RecommendedSigningKeys, now)
	if err != nil {
		t.Fatalf("generating a keyring: %v", err)
	}
	for _, k := range kr.Keys() {
		if err := kr.RetireKey(k.KeyID, now); err != nil {
			t.Fatalf("retiring %s: %v", k.KeyID, err)
		}
	}
	if _, _, err := kr.Sign(context.Background(), []byte("body")); err == nil {
		t.Fatal("a keyring with every key retired signed anyway")
	}
}

func TestMarshalAndLoadKeyringRoundTrip(t *testing.T) {
	now := time.Now()
	kr, err := NewKeyring("tinku@example.test", RecommendedSigningKeys, now)
	if err != nil {
		t.Fatalf("generating a keyring: %v", err)
	}
	secret, err := kr.MarshalSecret()
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}

	loaded, err := LoadKeyring(secret)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if loaded.Address() != kr.Address() {
		t.Errorf("loaded address %q, want %q", loaded.Address(), kr.Address())
	}
	if len(loaded.Keys()) != len(kr.Keys()) {
		t.Fatalf("loaded %d keys, want %d", len(loaded.Keys()), len(kr.Keys()))
	}

	// A signature made before the round trip must still verify against the
	// public key recovered after it — proves the seed, not just the id,
	// survived intact.
	body := []byte("round trip body")
	signature, keyID, err := kr.Sign(context.Background(), body)
	if err != nil {
		t.Fatalf("signing before the round trip: %v", err)
	}
	var pub ed25519.PublicKey
	for _, k := range loaded.Keys() {
		if k.KeyID == keyID {
			pub = k.PublicKey[:]
		}
	}
	if pub == nil {
		t.Fatalf("loaded keyring has no key %q", keyID)
	}
	sigBytes := decodeSigOrFail(t, signature)
	if !ed25519.Verify(pub, signatureInput(BatchSignatureTag, body), sigBytes) {
		t.Error("a signature made before MarshalSecret/LoadKeyring did not verify against the loaded key")
	}
}

func TestLoadKeyringRejectsMalformedInput(t *testing.T) {
	for name, data := range map[string][]byte{
		"not json":       []byte("not json"),
		"empty object":   []byte(`{}`),
		"wrong version":  []byte(`{"version":2,"address":"a@b","keys":[{"key_id":"k","seed":"AAAA"}]}`),
		"no keys":        []byte(`{"version":1,"address":"a@b","keys":[]}`),
		"bad seed":       []byte(`{"version":1,"address":"a@b","keys":[{"key_id":"k1","seed":"not-base64!!"},{"key_id":"k2","seed":"not-base64!!"}]}`),
		"duplicate ids":  []byte(`{"version":1,"address":"a@b","keys":[{"key_id":"k1","seed":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},{"key_id":"k1","seed":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}]}`),
		"too few active": []byte(`{"version":1,"address":"a@b","keys":[{"key_id":"k1","seed":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}]}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadKeyring(data); err == nil {
				t.Errorf("%s: LoadKeyring accepted malformed input", name)
			}
		})
	}
}

func TestAddKeyAndRetireKey(t *testing.T) {
	now := time.Now()
	kr, err := NewKeyring("tinku@example.test", RecommendedSigningKeys, now)
	if err != nil {
		t.Fatalf("generating a keyring: %v", err)
	}
	before := len(kr.Keys())

	added, err := kr.AddKey(now)
	if err != nil {
		t.Fatalf("adding a key: %v", err)
	}
	if !added.Active {
		t.Error("a freshly added key is not active")
	}
	if len(kr.Keys()) != before+1 {
		t.Fatalf("keyring has %d keys after AddKey, want %d", len(kr.Keys()), before+1)
	}

	if err := kr.RetireKey(added.KeyID, now); err != nil {
		t.Fatalf("retiring: %v", err)
	}
	for _, k := range kr.Keys() {
		if k.KeyID == added.KeyID && k.Active {
			t.Error("a retired key still reports itself active")
		}
	}

	if err := kr.RetireKey("no-such-key", now); err == nil {
		t.Error("retiring an unknown key id succeeded")
	}
}
