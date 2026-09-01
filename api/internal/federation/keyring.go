package federation

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"sync"
	"time"

	regularrp "github.com/catalystcommunity/linkkeys/sdks/regular-rp/go"
)

// LocalKeyring is this instance's own Ed25519 signing keys — the
// APPLICATION side of docs/application-keys.md's protocol. Tinku generates
// and holds every private key here; a linkkeys home domain never sees one.
// LocalKeyring implements Signer by picking any one of its currently active
// keys for each call, per "all valid keys are equal, there is no preferred
// key" (docs/application-keys.md, "How many keys").
//
// # Key custody
//
// Every private key this type holds lives only in process memory once
// loaded. Loading and saving go through MarshalSecret/LoadKeyring, which
// read and write exactly the operator-controlled secret this instance's
// config names (TINKU_FEDERATION_SIGNING_KEYS — see docs/OPERATING.md,
// "Federation signing keys"), the same convention this repository already
// uses for its other secrets (TINKU_SESSION_NONCE_SECRET,
// TINKU_LINKKEYS_PKI_API_KEY): one environment variable, sourced from
// whatever secret store the operator's deployment already uses. Nothing in
// this package writes a private key to a log, a metric, or an error
// message — MarshalSecret's output is the only place the seeds appear, and
// its caller is responsible for it reaching secret storage and nowhere
// else.
type LocalKeyring struct {
	address string

	mu   sync.Mutex
	keys []localKey
	rng  func(n int) (int, error) // key selection; overridden in tests for determinism
}

// localKey is one Ed25519 signing key this instance holds.
type localKey struct {
	KeyID     string
	Seed      [ed25519.SeedSize]byte
	PublicKey [ed25519.PublicKeySize]byte
	CreatedAt time.Time
	// RetiredAt is set once this instance has locally stopped offering the
	// key for new signatures — it does not by itself revoke the key at any
	// home domain; use SignRevocation and submit it there for that.
	RetiredAt *time.Time
}

// KeyInfo is the public-facing view of one local key: enough to enroll or
// renew it, never the private material.
type KeyInfo struct {
	KeyID     string
	PublicKey [ed25519.PublicKeySize]byte
	CreatedAt time.Time
	Active    bool
}

// RecommendedSigningKeys mirrors regularrp.RecommendedSigningKeys: the
// instance should hold this many valid signing keys so that an ordinary
// sibling revocation is possible (a key may never sign its own
// revocation, so two keys can never revoke each other).
const RecommendedSigningKeys = regularrp.RecommendedSigningKeys

// MinSigningKeys mirrors regularrp.MinSigningKeys: the fewest valid signing
// keys an instance may hold after initial enrollment.
const MinSigningKeys = regularrp.MinSigningKeys

// NewKeyring generates a fresh LocalKeyring with count freshly generated
// signing keys, all created at now. count must be at least MinSigningKeys;
// RecommendedSigningKeys (3) is what makes ordinary sibling revocation
// possible, and callers that do not have a specific reason for fewer should
// use it.
func NewKeyring(address string, count int, now time.Time) (*LocalKeyring, error) {
	if count < MinSigningKeys {
		return nil, fmt.Errorf("federation: a keyring needs at least %d signing keys, asked for %d", MinSigningKeys, count)
	}
	if address == "" {
		return nil, fmt.Errorf("federation: a keyring needs an address")
	}
	kr := &LocalKeyring{address: address}
	for i := 0; i < count; i++ {
		if _, err := kr.addKeyLocked(now); err != nil {
			return nil, err
		}
	}
	return kr, nil
}

func (kr *LocalKeyring) addKeyLocked(now time.Time) (localKey, error) {
	pub, seed, _, err := regularrp.NewSigningKeyPair()
	if err != nil {
		return localKey{}, fmt.Errorf("federation: generating a signing key: %w", err)
	}
	key := localKey{
		KeyID:     newKeyID(),
		Seed:      seed,
		PublicKey: pub,
		CreatedAt: now.UTC(),
	}
	kr.keys = append(kr.keys, key)
	return key, nil
}

// AddKey generates and adds one new signing key to this keyring, for a
// caller doing key rotation. It does not itself submit
// ApplicationKeys/add-key to any home domain — see Enroller.SubmitAddition
// in enrollment.go for that, which needs quorum signatures from two of this
// keyring's OTHER currently active keys before the home domain will accept
// it.
func (kr *LocalKeyring) AddKey(now time.Time) (KeyInfo, error) {
	kr.mu.Lock()
	defer kr.mu.Unlock()
	key, err := kr.addKeyLocked(now)
	if err != nil {
		return KeyInfo{}, err
	}
	return keyInfoOf(key), nil
}

// RetireKey stops this instance from offering keyID for new local
// signatures. It is local bookkeeping only — it does not revoke the key at
// any home domain a peer might still trust it under. Submit a
// SignRevocation there (regularrp.SignRevocation, signed by two OTHER
// active siblings) to actually revoke it.
func (kr *LocalKeyring) RetireKey(keyID string, at time.Time) error {
	kr.mu.Lock()
	defer kr.mu.Unlock()
	for i := range kr.keys {
		if kr.keys[i].KeyID == keyID {
			if kr.keys[i].RetiredAt == nil {
				t := at.UTC()
				kr.keys[i].RetiredAt = &t
			}
			return nil
		}
	}
	return fmt.Errorf("federation: no local key %q", keyID)
}

// Keys returns every key this keyring holds, active and retired, in no
// meaningful order. Never includes private material.
func (kr *LocalKeyring) Keys() []KeyInfo {
	kr.mu.Lock()
	defer kr.mu.Unlock()
	out := make([]KeyInfo, len(kr.keys))
	for i, k := range kr.keys {
		out[i] = keyInfoOf(k)
	}
	return out
}

func keyInfoOf(k localKey) KeyInfo {
	return KeyInfo{KeyID: k.KeyID, PublicKey: k.PublicKey, CreatedAt: k.CreatedAt, Active: k.RetiredAt == nil}
}

// ActiveSigners returns an ApplicationSigner for every currently active
// local key, for building a quorum signature (SignAddition, SignRevocation)
// or a self-renewal (SignRenewal) with the regularrp SDK. excludeKeyID, if
// non-empty, leaves that key out — used so a caller building an addition or
// a revocation cannot accidentally hand the target/new key back as one of
// its own quorum signers.
func (kr *LocalKeyring) ActiveSigners(excludeKeyID string) []regularrp.ApplicationSigner {
	kr.mu.Lock()
	defer kr.mu.Unlock()
	out := make([]regularrp.ApplicationSigner, 0, len(kr.keys))
	for _, k := range kr.keys {
		if k.RetiredAt != nil || k.KeyID == excludeKeyID {
			continue
		}
		seed := make([]byte, len(k.Seed))
		copy(seed, k.Seed[:])
		out = append(out, regularrp.ApplicationSigner{KeyID: k.KeyID, Algorithm: "ed25519", PrivateKeyBytes: seed})
	}
	return out
}

// SignerFor returns an ApplicationSigner for one specific local key, active
// or retired (a retired key can still sign its own renewal one last time,
// or a revocation of a sibling, right up until the operator deletes it
// entirely). Used by Enroller when a specific key — not "any active key" —
// must sign, such as a key renewing its own attestation.
func (kr *LocalKeyring) SignerFor(keyID string) (regularrp.ApplicationSigner, bool) {
	kr.mu.Lock()
	defer kr.mu.Unlock()
	for _, k := range kr.keys {
		if k.KeyID == keyID {
			seed := make([]byte, len(k.Seed))
			copy(seed, k.Seed[:])
			return regularrp.ApplicationSigner{KeyID: k.KeyID, Algorithm: "ed25519", PrivateKeyBytes: seed}, true
		}
	}
	return regularrp.ApplicationSigner{}, false
}

// ApplicationKeyAlgorithm is what LocalKeyring.Algorithm returns: the
// federation.Signer/Verifier "algorithm" field, naming the whole signing
// scheme (linkkeys application-key attestations over Ed25519), not merely
// the cryptographic primitive.
const ApplicationKeyAlgorithm = "linkkeys-application-key-ed25519-v1"

func (kr *LocalKeyring) Address() string   { return kr.address }
func (kr *LocalKeyring) Algorithm() string { return ApplicationKeyAlgorithm }

// Sign implements Signer: it signs body under BatchSignatureTag with any
// one currently active local key, chosen with no preference among them, and
// returns that key's id alongside the signature. It is SignWithContext
// pinned to BatchSignatureTag, because federation.Signer today is used only
// for batches — see sign.go's doc comment.
func (kr *LocalKeyring) Sign(ctx context.Context, body []byte) (signature, keyID string, err error) {
	return kr.SignWithContext(ctx, BatchSignatureTag, body)
}

// SignWithContext signs body under the given signature context with any one
// currently active local key, chosen with no preference among them, and
// returns that key's id alongside the signature. Use BatchSignatureTag or
// PeeringSignatureTag — see sign.go's doc comment on why the two must never
// be conflated.
func (kr *LocalKeyring) SignWithContext(_ context.Context, sigContext string, body []byte) (signature, keyID string, err error) {
	kr.mu.Lock()
	defer kr.mu.Unlock()

	var active []int
	for i, k := range kr.keys {
		if k.RetiredAt == nil {
			active = append(active, i)
		}
	}
	if len(active) == 0 {
		return "", "", fmt.Errorf("federation: no active local signing key")
	}
	pick := 0
	if len(active) > 1 {
		n, rerr := kr.rand(len(active))
		if rerr != nil {
			return "", "", fmt.Errorf("federation: choosing a signing key: %w", rerr)
		}
		pick = n
	}
	key := kr.keys[active[pick]]

	message := signatureInput(sigContext, body)
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(key.Seed[:]), message)
	return base64.RawURLEncoding.EncodeToString(sig), key.KeyID, nil
}

func (kr *LocalKeyring) rand(n int) (int, error) {
	if kr.rng != nil {
		return kr.rng(n)
	}
	max := big.NewInt(int64(n))
	v, err := rand.Int(rand.Reader, max)
	if err != nil {
		return 0, err
	}
	return int(v.Int64()), nil
}

var _ Signer = (*LocalKeyring)(nil)

// newKeyID makes a short random key id. It is public (it appears on the
// wire, in a SignedDelivery, and in a linkkeys attestation), so it carries
// no structure worth hiding — just enough entropy that two keys, including
// ones from two different instances, never collide.
func newKeyID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// Only crypto/rand's own failure reaches here, which this process
		// cannot recover from in any case that matters (every other use of
		// randomness in this package — key generation itself — would also
		// be broken).
		panic("federation: could not generate a key id: " + err.Error())
	}
	return "k_" + base64.RawURLEncoding.EncodeToString(raw[:])
}

// ---------------------------------------------------------------------------
// Secret storage: JSON, meant for one operator-controlled secret value —
// see docs/OPERATING.md, "Federation signing keys".
// ---------------------------------------------------------------------------

// keyringSecretV1 is the on-disk/on-wire shape of MarshalSecret's output.
// It is deliberately NOT the CSIL-generated codec: this is a local secret
// this instance reads from its own configuration, never sent to, or
// received from, another instance or a home domain.
type keyringSecretV1 struct {
	Version int                `json:"version"`
	Address string             `json:"address"`
	Keys    []keyringSecretKey `json:"keys"`
}

type keyringSecretKey struct {
	KeyID     string     `json:"key_id"`
	Seed      string     `json:"seed"` // base64url(32-byte Ed25519 seed) — see the package doc: never logged
	CreatedAt time.Time  `json:"created_at"`
	RetiredAt *time.Time `json:"retired_at,omitempty"`
}

// MarshalSecret encodes this keyring's full private material as JSON, for
// the operator to place in secret storage (TINKU_FEDERATION_SIGNING_KEYS).
// The caller must not log this output.
func (kr *LocalKeyring) MarshalSecret() ([]byte, error) {
	kr.mu.Lock()
	defer kr.mu.Unlock()
	out := keyringSecretV1{Version: 1, Address: kr.address}
	for _, k := range kr.keys {
		out.Keys = append(out.Keys, keyringSecretKey{
			KeyID:     k.KeyID,
			Seed:      base64.RawURLEncoding.EncodeToString(k.Seed[:]),
			CreatedAt: k.CreatedAt,
			RetiredAt: k.RetiredAt,
		})
	}
	return json.Marshal(out)
}

// LoadKeyring decodes a keyring previously produced by MarshalSecret. It
// fails closed on anything that does not parse, on zero keys, and on fewer
// than MinSigningKeys keys that are still active — a keyring this small
// cannot sign, or cannot ever be revoked by its own siblings, so it is
// better to refuse it at boot than to run with it silently.
func LoadKeyring(data []byte) (*LocalKeyring, error) {
	var in keyringSecretV1
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("federation: decoding the signing keyring: %w", err)
	}
	if in.Version != 1 {
		return nil, fmt.Errorf("federation: signing keyring has unknown version %d", in.Version)
	}
	if in.Address == "" {
		return nil, fmt.Errorf("federation: signing keyring has no address")
	}
	if len(in.Keys) == 0 {
		return nil, fmt.Errorf("federation: signing keyring has no keys")
	}
	kr := &LocalKeyring{address: in.Address}
	active := 0
	seen := map[string]bool{}
	for _, k := range in.Keys {
		if k.KeyID == "" {
			return nil, fmt.Errorf("federation: signing keyring has a key with no id")
		}
		if seen[k.KeyID] {
			return nil, fmt.Errorf("federation: signing keyring has a duplicate key id %q", k.KeyID)
		}
		seen[k.KeyID] = true
		seedBytes, err := base64.RawURLEncoding.DecodeString(k.Seed)
		if err != nil || len(seedBytes) != ed25519.SeedSize {
			return nil, fmt.Errorf("federation: signing keyring has a malformed seed for key %q", k.KeyID)
		}
		var seed [ed25519.SeedSize]byte
		copy(seed[:], seedBytes)
		pub := ed25519.NewKeyFromSeed(seedBytes).Public().(ed25519.PublicKey)
		var pubArr [ed25519.PublicKeySize]byte
		copy(pubArr[:], pub)
		kr.keys = append(kr.keys, localKey{
			KeyID: k.KeyID, Seed: seed, PublicKey: pubArr,
			CreatedAt: k.CreatedAt.UTC(), RetiredAt: k.RetiredAt,
		})
		if k.RetiredAt == nil {
			active++
		}
	}
	if active < MinSigningKeys {
		return nil, fmt.Errorf("federation: signing keyring has %d active key(s), needs at least %d", active, MinSigningKeys)
	}
	return kr, nil
}
