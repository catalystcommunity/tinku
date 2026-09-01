package federation

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"

	regularrp "github.com/catalystcommunity/linkkeys/sdks/regular-rp/go"
)

// PeerKeyResolver resolves and verifies another instance's linkkeys
// application keys through this instance's own regular RP (or a DNS-less
// RP cache — either satisfies this interface identically).
// *regularrp.CachedResolver is the production implementation; tests inject
// a fake so they never need a live linkkeys deployment to prove the
// verification RULES are right.
type PeerKeyResolver interface {
	Resolve(ctx context.Context, instance regularrp.InstanceRef, maxCacheAgeSeconds *int64) (regularrp.ResolveResult, error)
}

var _ PeerKeyResolver = (*regularrp.CachedResolver)(nil)

// ApplicationKeyVerifier is the real Verifier: it checks a signature
// against a linkkeys application-key attestation resolved through Resolver,
// under one fixed signature Context (BatchSignatureTag or
// PeeringSignatureTag — see sign.go). Build two instances sharing one
// Resolver, one per context, rather than one instance serving both: see
// sign.go's doc comment on why the two contexts must never be conflated.
//
// This type does not verify anything itself beyond resolving-then-checking
// a signature. Every rule about which keys are usable — attestation
// verified, not expired, key not expired, no accepted revocation, use and
// algorithm matching — lives in regularrp.VerifiedApplicationKeySet.KeyForUse,
// which Resolver's result already carries out. This type does not
// reimplement any of it, per this repository's rule to use the SDK rather
// than reimplement verification.
type ApplicationKeyVerifier struct {
	Resolver PeerKeyResolver
	// Context is the signature context this verifier checks — pass
	// BatchSignatureTag for delivery verification, PeeringSignatureTag for
	// peering-request verification. Required.
	Context string
	// MaxCacheAgeSeconds, if set, is passed through to Resolver.Resolve —
	// a caller that needs fresher material than the RP's own default cache
	// age can ask for it. Nil uses the RP's default.
	MaxCacheAgeSeconds *int64
}

var _ Verifier = (*ApplicationKeyVerifier)(nil)

// applicationKeyCryptoAlgorithm is the cryptographic algorithm every
// federation signature under the real scheme uses. It is unrelated to
// ApplicationKeyAlgorithm (keyring.go), which names the whole SIGNING
// SCHEME on the wire, not the bare primitive — see
// regularrp.KeyForUse's own algorithm parameter, which this must match
// exactly for a lookup to succeed.
const applicationKeyCryptoAlgorithm = "ed25519"

// Verify implements Verifier. It fails closed — returning the single
// ErrUntrusted for every reason, per sign.go's rule not to tell a caller
// which half of the check failed — when: identity is empty (an address
// this instance has never resolved a canonical identity for), the RP
// cannot be resolved, the named key has no current usable attestation
// (unknown, expired, unattested, wrong use/algorithm, or revoked), or the
// signature itself does not verify.
func (v *ApplicationKeyVerifier) Verify(ctx context.Context, _ string, identity KeyIdentity, algorithm, keyID string, body, signature []byte) error {
	if algorithm != ApplicationKeyAlgorithm {
		return ErrUntrusted
	}
	if identity.Empty() {
		return ErrUntrusted
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(string(signature))
	if err != nil {
		return ErrUntrusted
	}

	instance := regularrp.InstanceRef{
		SubjectUserID: identity.SubjectUserID,
		SubjectDomain: identity.SubjectDomain,
		ApplicationID: identity.ApplicationID,
		InstanceID:    identity.InstanceID,
	}
	result, err := v.Resolver.Resolve(ctx, instance, v.MaxCacheAgeSeconds)
	if err != nil {
		return ErrUntrusted
	}
	attestation, err := result.Keys.KeyForUse(keyID, regularrp.KeyUsageSign, applicationKeyCryptoAlgorithm)
	if err != nil {
		return ErrUntrusted
	}

	message := signatureInput(v.Context, body)
	if !ed25519.Verify(ed25519.PublicKey(attestation.PublicKey), message, sigBytes) {
		return ErrUntrusted
	}
	return nil
}
