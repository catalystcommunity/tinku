package federation

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"time"

	regularrp "github.com/catalystcommunity/linkkeys/sdks/regular-rp/go"
	api "github.com/catalystcommunity/linkkeys/sdks/regular-rp/go/generated"
)

// Enrollment and renewal of this instance's own application-key
// attestations at its linkkeys home domain. Initial enrollment itself
// (Account/enroll-application-instance) is NOT here: that operation is
// authenticated by the account owner logging into the home domain
// directly, per docs/application-keys.md's "Initial enrollment" — a
// one-time, human-supervised step outside this service's own
// authentication, not something this process can do unattended. This
// instance's automated job starts once enrollment has already happened: it
// renews its keys' attestations on a schedule (RenewKey), and it can add a
// further key later under quorum (SubmitAddition), both of which the
// application-key quorum itself authorizes — no API key, no session, per
// docs/application-keys.md's Operations table.

// NeedsRenewal reports whether an attestation with the given attested-at
// and expiry times should be renewed at now.
//
// Renewal is idempotent above the half-life of the CURRENT attestation
// (docs/application-keys.md, "To renew an attestation"): the home domain
// returns the stored bytes and makes no new signature. Asking earlier than
// that is wasted work — it pays a round trip for the answer it would have
// gotten anyway — and asking right at or after expiry risks a gap where a
// peer has no current proof for this key. So Tinku renews exactly at the
// boundary the protocol makes free: once less than half the attestation's
// own remaining lifetime is left.
func NeedsRenewal(attestedAt, attestationExpiresAt, now time.Time) bool {
	lifetime := attestationExpiresAt.Sub(attestedAt)
	if lifetime <= 0 {
		return true // malformed or already expired: always renew.
	}
	halfLife := attestedAt.Add(lifetime / 2)
	return !now.Before(halfLife)
}

// Enroller submits application-key operations to this instance's own home
// domain on behalf of its LocalKeyring.
type Enroller struct {
	Client   *api.ApplicationKeysClient
	Keyring  *LocalKeyring
	Instance regularrp.InstanceRef
	// RequestedKeyLifetime is how long a NEWLY enrolled or renewed key
	// itself should be valid for (distinct from the attestation lifetime,
	// which the home domain controls — see
	// APPLICATION_KEY_MAX_LIFETIME_SECONDS in docs/application-keys.md).
	RequestedKeyLifetime time.Duration
	Now                  func() time.Time
}

func (e *Enroller) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

func (e *Enroller) requestedKeyLifetime() time.Duration {
	if e.RequestedKeyLifetime > 0 {
		return e.RequestedKeyLifetime
	}
	return 90 * 24 * time.Hour
}

// RenewKey renews keyID's attestation. An Ed25519 signing key proves
// possession by signing for itself — see docs/application-keys.md, "To
// renew an attestation" — so this needs no sibling signatures, only
// keyID's own key.
//
// The bool return is RenewApplicationKeyAttestationResponse.Signed: false
// means the home domain answered from its stored bytes without making a
// new signature (the idempotent case, expected whenever this is called
// above the half-life — callers driving a renewal LOOP should use
// NeedsRenewal to decide whether to call this at all, rather than relying
// on this return value to avoid the round trip).
func (e *Enroller) RenewKey(ctx context.Context, keyID string) (api.SignedApplicationKeyAttestation, bool, error) {
	signer, ok := e.Keyring.SignerFor(keyID)
	if !ok {
		return api.SignedApplicationKeyAttestation{}, false, fmt.Errorf("federation: no local key %q to renew", keyID)
	}

	challenge, err := e.Client.StartKeyChallenge(ctx, api.StartApplicationKeyChallengeRequest{
		SubjectUserId: e.Instance.SubjectUserID,
		ApplicationId: e.Instance.ApplicationID,
		InstanceId:    e.Instance.InstanceID,
		Purpose:       regularrp.PurposeRenew,
		KeyUsage:      regularrp.KeyUsageSign,
		Algorithm:     "ed25519",
	})
	if err != nil {
		return api.SignedApplicationKeyAttestation{}, false, fmt.Errorf("federation: starting a renewal challenge for %q: %w", keyID, err)
	}
	if challenge.Challenge == nil {
		return api.SignedApplicationKeyAttestation{}, false, fmt.Errorf(
			"federation: renewal challenge for %q carried no plaintext challenge (an ed25519 key cannot open a sealed one)", keyID)
	}

	now := e.now()
	renewal := api.ApplicationKeyRenewal{
		SubjectUserId: e.Instance.SubjectUserID,
		SubjectDomain: e.Instance.SubjectDomain,
		ApplicationId: e.Instance.ApplicationID,
		InstanceId:    e.Instance.InstanceID,
		KeyId:         keyID,
		ChallengeId:   challenge.ChallengeId,
		Challenge:     *challenge.Challenge,
		RequestedAt:   now.Format(time.RFC3339),
		ExpiresAt:     now.Add(e.requestedKeyLifetime()).Format(time.RFC3339),
	}
	signed, err := regularrp.SignRenewal(renewal, nil, &signer)
	if err != nil {
		return api.SignedApplicationKeyAttestation{}, false, fmt.Errorf("federation: signing a renewal for %q: %w", keyID, err)
	}

	resp, err := e.Client.RenewAttestation(ctx, api.RenewApplicationKeyAttestationRequest{Request: signed})
	if err != nil {
		return api.SignedApplicationKeyAttestation{}, false, fmt.Errorf("federation: renewing the attestation for %q: %w", keyID, err)
	}
	return resp.Attestation, resp.Signed, nil
}

// SubmitAddition asks the home domain to attest newKeyID, a signing key
// already present (but not yet attested) in Keyring. Two of Keyring's
// OTHER currently active keys authorize it — the new key never counts
// toward that quorum, and separately proves possession of its own private
// key, per docs/application-keys.md's "To add a key".
func (e *Enroller) SubmitAddition(ctx context.Context, newKeyID string) (api.SignedApplicationKeyAttestation, error) {
	quorum := e.Keyring.ActiveSigners(newKeyID)
	if len(quorum) < regularrp.AdditionQuorum {
		return api.SignedApplicationKeyAttestation{}, fmt.Errorf(
			"federation: %d other active signing key(s), need %d to authorize adding %q",
			len(quorum), regularrp.AdditionQuorum, newKeyID)
	}
	quorum = quorum[:regularrp.AdditionQuorum]

	newSigner, ok := e.Keyring.SignerFor(newKeyID)
	if !ok {
		return api.SignedApplicationKeyAttestation{}, fmt.Errorf("federation: no local key %q to add", newKeyID)
	}
	publicKey := []byte(ed25519.NewKeyFromSeed(newSigner.PrivateKeyBytes).Public().(ed25519.PublicKey))

	challenge, err := e.Client.StartKeyChallenge(ctx, api.StartApplicationKeyChallengeRequest{
		SubjectUserId: e.Instance.SubjectUserID,
		ApplicationId: e.Instance.ApplicationID,
		InstanceId:    e.Instance.InstanceID,
		Purpose:       regularrp.PurposeAdd,
		KeyUsage:      regularrp.KeyUsageSign,
		Algorithm:     "ed25519",
	})
	if err != nil {
		return api.SignedApplicationKeyAttestation{}, fmt.Errorf("federation: starting an addition challenge for %q: %w", newKeyID, err)
	}
	if challenge.Challenge == nil {
		return api.SignedApplicationKeyAttestation{}, fmt.Errorf(
			"federation: addition challenge for %q carried no plaintext challenge", newKeyID)
	}

	now := e.now()
	addition := api.ApplicationKeyAddition{
		SubjectUserId:               e.Instance.SubjectUserID,
		SubjectDomain:               e.Instance.SubjectDomain,
		ApplicationId:               e.Instance.ApplicationID,
		InstanceId:                  e.Instance.InstanceID,
		KeyId:                       newKeyID,
		KeyUsage:                    regularrp.KeyUsageSign,
		Algorithm:                   "ed25519",
		PublicKey:                   publicKey,
		Fingerprint:                 regularrp.Fingerprint(publicKey),
		RequestedKeyLifetimeSeconds: int64(e.requestedKeyLifetime().Seconds()),
		ChallengeId:                 challenge.ChallengeId,
		Challenge:                   *challenge.Challenge,
		RequestedAt:                 now.Format(time.RFC3339),
		ExpiresAt:                   now.Add(e.requestedKeyLifetime()).Format(time.RFC3339),
	}
	signed, err := regularrp.SignAddition(addition, quorum, &newSigner)
	if err != nil {
		return api.SignedApplicationKeyAttestation{}, fmt.Errorf("federation: signing an addition for %q: %w", newKeyID, err)
	}

	resp, err := e.Client.AddKey(ctx, api.AddApplicationKeyRequest{Request: signed})
	if err != nil {
		return api.SignedApplicationKeyAttestation{}, fmt.Errorf("federation: adding key %q: %w", newKeyID, err)
	}
	return resp.Attestation, nil
}

// RunRenewalLoop renews every active local key's attestation on a timer,
// until ctx is done. It tracks each key's last known attestation expiry in
// memory (seeded by fetching current keys once at the first tick) and
// calls RenewKey only when NeedsRenewal says the current attestation has
// used up more than half its life — never on a fixed schedule regardless of
// need, which is exactly the wasted-round-trip case
// docs/application-keys.md's idempotent-renewal rule exists to avoid.
//
// A key with no known attestation yet (freshly generated, never enrolled)
// is treated as always needing renewal, so a newly added key gets its
// first attestation on the next tick rather than waiting a full interval.
func (e *Enroller) RunRenewalLoop(ctx context.Context, checkInterval time.Duration) {
	if checkInterval <= 0 {
		checkInterval = 15 * time.Minute
	}
	expiry := map[string]attestationWindow{}
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for {
		e.renewalPass(ctx, expiry)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type attestationWindow struct {
	attestedAt, expiresAt time.Time
}

func (e *Enroller) renewalPass(ctx context.Context, expiry map[string]attestationWindow) {
	now := e.now()
	for _, key := range e.Keyring.Keys() {
		if !key.Active {
			continue
		}
		window, known := expiry[key.KeyID]
		if known && !NeedsRenewal(window.attestedAt, window.expiresAt, now) {
			continue
		}
		signed, _, err := e.RenewKey(ctx, key.KeyID)
		if err != nil {
			// Left in expiry as it was (or absent): the next pass tries
			// again. A renewal failure must not crash the process — a peer
			// still holding this instance's PREVIOUS attestation is
			// unaffected until that one itself expires.
			continue
		}
		attestation, err := api.DecodeApplicationKeyAttestation(signed.Attestation)
		if err != nil {
			continue
		}
		attestedAt, aerr1 := time.Parse(time.RFC3339, attestation.AttestedAt)
		expiresAt, aerr2 := time.Parse(time.RFC3339, attestation.AttestationExpiresAt)
		if aerr1 == nil && aerr2 == nil {
			expiry[key.KeyID] = attestationWindow{attestedAt: attestedAt, expiresAt: expiresAt}
		}
	}
}
