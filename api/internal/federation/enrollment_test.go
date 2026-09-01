package federation

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	regularrp "github.com/catalystcommunity/linkkeys/sdks/regular-rp/go"
	api "github.com/catalystcommunity/linkkeys/sdks/regular-rp/go/generated"
)

func TestNeedsRenewalBelowHalfLife(t *testing.T) {
	attestedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	expiresAt := attestedAt.Add(24 * time.Hour)
	now := attestedAt.Add(11 * time.Hour) // well under half of 24h
	if NeedsRenewal(attestedAt, expiresAt, now) {
		t.Error("renewal requested with more than half the attestation's life left")
	}
}

func TestNeedsRenewalAtAndAboveHalfLife(t *testing.T) {
	attestedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	expiresAt := attestedAt.Add(24 * time.Hour)

	atHalf := attestedAt.Add(12 * time.Hour)
	if !NeedsRenewal(attestedAt, expiresAt, atHalf) {
		t.Error("renewal not requested exactly at the half-life boundary")
	}

	pastHalf := attestedAt.Add(20 * time.Hour)
	if !NeedsRenewal(attestedAt, expiresAt, pastHalf) {
		t.Error("renewal not requested with less than half the attestation's life left")
	}
}

func TestNeedsRenewalTreatsMalformedWindowAsNeedingRenewal(t *testing.T) {
	now := time.Now()
	if !NeedsRenewal(now, now, now) {
		t.Error("a zero-length attestation window was not treated as needing renewal")
	}
	if !NeedsRenewal(now, now.Add(-time.Hour), now) {
		t.Error("an already-expired (negative-length) attestation window was not treated as needing renewal")
	}
}

// fakeApplicationKeysTransport answers StartKeyChallenge/AddKey/RenewAttestation
// with canned responses, so Enroller's REQUEST-BUILDING can be tested
// without a live linkkeys deployment. It does not validate anything a real
// home domain would — it exists to prove Enroller assembles the right shape
// of request and reads the right shape of response, not to re-test the
// server's own admission logic.
type fakeApplicationKeysTransport struct {
	challenge api.StartApplicationKeyChallengeResponse
	renewal   api.RenewApplicationKeyAttestationResponse
	addition  api.AddApplicationKeyResponse

	lastOp      string
	lastPayload []byte
}

func (f *fakeApplicationKeysTransport) Call(_ context.Context, _, op string, payload []byte) ([]byte, error) {
	f.lastOp = op
	f.lastPayload = payload
	switch op {
	case "start-key-challenge":
		return api.EncodeStartApplicationKeyChallengeResponse(f.challenge), nil
	case "renew-attestation":
		return api.EncodeRenewApplicationKeyAttestationResponse(f.renewal), nil
	case "add-key":
		return api.EncodeAddApplicationKeyResponse(f.addition), nil
	default:
		panic("fakeApplicationKeysTransport: unexpected op " + op)
	}
}

var _ api.Transport = (*fakeApplicationKeysTransport)(nil)

func TestEnrollerRenewKeyBuildsAndSubmitsASelfSignedRenewal(t *testing.T) {
	kr := newTestKeyring(t, "tinku@example.test")
	targetKeyID := kr.Keys()[0].KeyID
	instance := testInstance("example")

	challengeBytes := []byte("a challenge, opaque to this test")
	attestation := api.ApplicationKeyAttestation{
		SubjectUserId: instance.SubjectUserID, SubjectDomain: instance.SubjectDomain,
		ApplicationId: instance.ApplicationID, InstanceId: instance.InstanceID,
		KeyId: targetKeyID, KeyUsage: "sign", Algorithm: "ed25519",
		AttestedAt: "2026-01-01T00:00:00Z", AttestationExpiresAt: "2026-01-02T00:00:00Z",
		KeyCreatedAt: "2025-01-01T00:00:00Z", KeyExpiresAt: "2027-01-01T00:00:00Z",
	}
	fake := &fakeApplicationKeysTransport{
		challenge: api.StartApplicationKeyChallengeResponse{
			ChallengeId: "challenge-1", Challenge: &challengeBytes, ExpiresAt: "2026-01-01T00:05:00Z",
		},
		renewal: api.RenewApplicationKeyAttestationResponse{
			Attestation: api.SignedApplicationKeyAttestation{Attestation: api.EncodeApplicationKeyAttestation(attestation)},
			Signed:      true,
		},
	}
	enroller := &Enroller{
		Client:   api.NewApplicationKeysClient(fake),
		Keyring:  kr,
		Instance: instance,
		Now:      func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}

	signed, wasSigned, err := enroller.RenewKey(context.Background(), targetKeyID)
	if err != nil {
		t.Fatalf("renewing: %v", err)
	}
	if !wasSigned {
		t.Error("Signed came back false for a response that set it true")
	}
	got, err := api.DecodeApplicationKeyAttestation(signed.Attestation)
	if err != nil {
		t.Fatalf("decoding the returned attestation: %v", err)
	}
	if got.KeyId != targetKeyID {
		t.Errorf("returned attestation names key %q, want %q", got.KeyId, targetKeyID)
	}

	// The submitted renewal must actually be signed by the target key
	// itself (an Ed25519 key proves possession by signing for itself) and
	// must carry the challenge the fake home domain issued.
	req, err := api.DecodeRenewApplicationKeyAttestationRequest(fake.lastPayload)
	if err != nil {
		t.Fatalf("decoding the submitted request: %v", err)
	}
	renewal, err := api.DecodeApplicationKeyRenewal(req.Request.Renewal)
	if err != nil {
		t.Fatalf("decoding the renewal payload: %v", err)
	}
	if renewal.KeyId != targetKeyID {
		t.Errorf("submitted renewal names key %q, want %q", renewal.KeyId, targetKeyID)
	}
	if renewal.ChallengeId != "challenge-1" || string(renewal.Challenge) != string(challengeBytes) {
		t.Error("submitted renewal does not carry the issued challenge")
	}
	if req.Request.PossessionProof == nil {
		t.Fatal("an ed25519 renewal was submitted with no possession proof")
	}
	// regularrp.PossessionSignatureInput is the exported form of the same
	// tag-wrapping construction sign.go's signatureInput uses under
	// Tinku's own tags — reused here (rather than reimplemented) to build
	// exactly the bytes the possession proof was made over.
	proofMessage := regularrp.PossessionSignatureInput(req.Request.Renewal)
	pub := findPublicKey(kr, targetKeyID)
	if !ed25519.Verify(pub, proofMessage, *req.Request.PossessionProof) {
		t.Error("the possession proof does not verify against the target key's own public key")
	}
}

func TestEnrollerRenewKeyRejectsAnUnknownKey(t *testing.T) {
	kr := newTestKeyring(t, "tinku@example.test")
	enroller := &Enroller{
		Client:   api.NewApplicationKeysClient(&fakeApplicationKeysTransport{}),
		Keyring:  kr,
		Instance: testInstance("example"),
	}
	if _, _, err := enroller.RenewKey(context.Background(), "no-such-key"); err == nil {
		t.Error("renewing an unknown local key succeeded")
	}
}

func findPublicKey(kr *LocalKeyring, keyID string) []byte {
	for _, k := range kr.Keys() {
		if k.KeyID == keyID {
			return k.PublicKey[:]
		}
	}
	return nil
}
