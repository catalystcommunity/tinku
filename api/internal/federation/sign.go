// Package federation carries events between tinku instances: who is
// trusted, what is delivered, and how a delivery is authenticated.
//
// # The trust seam
//
// Signing lives behind Signer and Verifier. Every path that produces or
// checks a signature goes through them, so the scheme is one substitution
// rather than a change spread across the delivery code.
//
// Two schemes exist today:
//
//   - ApplicationKeyVerifier (verify.go) and LocalKeyring (keyring.go) are
//     the real ones. This instance holds its own Ed25519 signing keys and
//     signs its own batches; a peer verifies a batch against a short-lived
//     linkkeys attestation of the signing key, fetched through this
//     instance's regular RP. See docs/application-keys.md in the linkkeys
//     repo for the attestation protocol, and BatchSignatureTag below for
//     the signature context this package defines for federation batches.
//
//   - DevSigner authenticates NOTHING. It exists so the queue, the retry
//     rules, the peering flow and the receiving side can be built and
//     tested end to end without real key material — a test env variable, a
//     local sqlite database and two in-process servers, none of a linkkeys
//     deployment. It refuses to construct outside a dev or nonprod
//     environment, exactly as DevAuthService does, so it cannot be the
//     thing running in production by accident.
package federation

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// KeyIdentity is who a signing key belongs to, independent of any specific
// verification scheme: the linkkeys canonical account, application, and
// application instance an attested key is bound to. ApplicationKeyVerifier
// maps this onto a regularrp.InstanceRef; a different Verifier could map it
// onto something else entirely — the point of the interface is that
// delivery code never has to know which.
//
// SubjectUserID is always the account's one canonical UUID. A handle is not
// an identity: it can move to a different account or be reused, so it never
// appears here — see docs/application-keys.md's "Identity" section.
type KeyIdentity struct {
	SubjectUserID string
	SubjectDomain string
	ApplicationID string
	InstanceID    string
}

// Empty reports whether no field of this identity is set — the state of a
// peer this instance has never received a verified signed request from.
func (k KeyIdentity) Empty() bool {
	return k.SubjectUserID == "" && k.SubjectDomain == "" && k.ApplicationID == "" && k.InstanceID == ""
}

// Signer signs a delivery or a peering request as this instance.
type Signer interface {
	// Address is who this instance signs as, `handle@domain`. It is a
	// display name for a peer to address this instance by; it carries no
	// trust of its own under the real scheme — see KeyIdentity.
	Address() string
	// Algorithm names the scheme, so a receiver can refuse one it does not
	// accept by name rather than by failing to parse a signature.
	Algorithm() string
	// Sign returns a detached signature over body, made with any one of
	// this instance's currently valid local signing keys, and that key's
	// id. All valid keys are equal and there is no preferred one, so a
	// second call is free to pick a different key — callers must not
	// assume repeatability, and must carry keyID alongside the signature so
	// a verifier knows which public key to check it against.
	Sign(ctx context.Context, body []byte) (signature, keyID string, err error)
}

// Verifier checks a delivery or a peering request came from who it claims.
type Verifier interface {
	// Verify reports whether signature is a valid signature over body,
	// made under algorithm by the key named keyID, currently valid for
	// identity. address is the handle@domain a caller may log; it is not
	// itself checked — identity is what decides trust.
	Verify(ctx context.Context, address string, identity KeyIdentity, algorithm, keyID string, body, signature []byte) error
}

// Signature contexts this package defines for the two signed structures a
// LocalKeyring signs. Each is Tinku's OWN fixed domain-separation tag —
// neither is a linkkeys tag, and neither is shared with the other:
//
//   - BatchSignatureTag must never be used for anything other than a
//     federation event batch (SignedDelivery.body). It must not be reused
//     as an authentication-request context or as linkkeys'
//     application-key-attestation context (a different protocol, a
//     different signer, a different trust decision entirely) — conflating
//     signature contexts is how a forged federation message becomes
//     replayable as something else, such as a login request.
//   - PeeringSignatureTag is the same idea for a peering request
//     (SignedPeeringRequest.body): distinct from BatchSignatureTag so a
//     batch signature can never be presented as a peering request or the
//     reverse.
//
// A signature under either tag covers `CBOR([tag, body])` — a two-element
// array, not a concatenation — built by signatureInput in cbor.go. This
// mirrors the shape linkkeys' own signed structures use (see
// docs/application-keys.md in the linkkeys repo, "Domain-separation tags"),
// which is where this pattern comes from, but the tag values themselves are
// Tinku's, unrelated to any linkkeys tag.
const (
	BatchSignatureTag   = "tinku-federation-batch-v1"
	PeeringSignatureTag = "tinku-federation-peering-v1"
)

// ErrUntrusted is returned when a signature does not check out. It is
// deliberately one error for every reason — a wrong signature, an unknown
// algorithm, an unattested or revoked key, an address that does not match
// — because telling a caller which of those failed tells an attacker which
// half to work on.
var ErrUntrusted = errors.New("federation: the delivery is not from who it claims")

// DevAlgorithm names the scheme that authenticates nothing.
const DevAlgorithm = "dev-unsigned-v1"

// devKeyID is the only "key id" DevSigner ever uses. There is exactly one
// key under this scheme — it does not need more, because it proves nothing
// about any key either.
const devKeyID = "dev"

// DevSigner is a signer that proves the plumbing and nothing else. Its
// "signature" is a digest anybody can compute, so it establishes only that
// a body arrived intact — not who sent it.
type DevSigner struct {
	address string
}

// NewDevSigner builds one, refusing outside a development environment.
//
// The refusal is the whole point. A scheme that authenticates nothing is
// useful while the real one is being exercised and catastrophic if it
// reaches a deployment, so the gate is here rather than in a caller that
// might forget it.
func NewDevSigner(address, env string) (*DevSigner, error) {
	if !developmentEnv(env) {
		return nil, fmt.Errorf(
			"federation: the %s scheme is refused in a %q environment; it authenticates nothing",
			DevAlgorithm, env)
	}
	if address == "" {
		return nil, errors.New("federation: a signer needs an address")
	}
	return &DevSigner{address: address}, nil
}

func (d *DevSigner) Address() string   { return d.address }
func (d *DevSigner) Algorithm() string { return DevAlgorithm }

func (d *DevSigner) Sign(_ context.Context, body []byte) (string, string, error) {
	return devSignature(d.address, body), devKeyID, nil
}

// DevVerifier accepts what DevSigner produces. It confirms that the body
// was not altered in transit and that the sender knew its own address —
// which any observer also knows. It is not authentication.
type DevVerifier struct{}

// NewDevVerifier builds one, refusing outside a development environment for
// the same reason NewDevSigner does.
func NewDevVerifier(env string) (*DevVerifier, error) {
	if !developmentEnv(env) {
		return nil, fmt.Errorf(
			"federation: the %s scheme is refused in a %q environment; it authenticates nothing",
			DevAlgorithm, env)
	}
	return &DevVerifier{}, nil
}

func (v *DevVerifier) Verify(_ context.Context, address string, _ KeyIdentity, algorithm, _ string, body, signature []byte) error {
	if algorithm != DevAlgorithm {
		return ErrUntrusted
	}
	if string(signature) != devSignature(address, body) {
		return ErrUntrusted
	}
	return nil
}

func devSignature(address string, body []byte) string {
	sum := sha256.Sum256(append([]byte(address+"\n"), body...))
	return DevAlgorithm + ":" + base64.RawURLEncoding.EncodeToString(sum[:])
}

// developmentEnv mirrors config.DevAuthAllowed's idea of a non-production
// environment. It is repeated rather than imported so this package stays
// free of the config package, which imports it.
func developmentEnv(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "dev", "development", "nonprod", "test":
		return true
	}
	return false
}

// SplitAddress divides `handle@domain`. It is the one place the address
// format is parsed, so a change to it is one change.
func SplitAddress(address string) (handle, domain string, ok bool) {
	handle, domain, found := strings.Cut(strings.ToLower(strings.TrimSpace(address)), "@")
	if !found || handle == "" || domain == "" || strings.Contains(domain, "@") {
		return "", "", false
	}
	return handle, domain, true
}
