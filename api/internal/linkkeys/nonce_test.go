package linkkeys

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

func TestNonceRoundTrip(t *testing.T) {
	const secret = "test-secret"
	if err := VerifyNonce(secret, MintNonce(secret)); err != nil {
		t.Errorf("a freshly minted nonce did not verify: %v", err)
	}
}

func TestNonceRejectsAnotherSecret(t *testing.T) {
	nonce := MintNonce("the-real-secret")
	if err := VerifyNonce("a-different-secret", nonce); err == nil {
		t.Error("a nonce minted with a different secret verified")
	}
}

// TestNonceRejectsTampering flips one bit of the random half and confirms
// the MAC catches it. Without this the nonce would be a suggestion.
func TestNonceRejectsTampering(t *testing.T) {
	const secret = "test-secret"
	raw, err := base64.RawURLEncoding.DecodeString(MintNonce(secret))
	if err != nil {
		t.Fatalf("decoding a minted nonce: %v", err)
	}
	raw[nonceTimeLen] ^= 0x01

	if err := VerifyNonce(secret, base64.RawURLEncoding.EncodeToString(raw)); err == nil {
		t.Error("a tampered nonce verified")
	}
}

// TestNonceExpires backdates the issue time and re-signs, so the nonce is
// valid in every respect except age — which is the only thing left for
// VerifyNonce to reject it for.
func TestNonceExpires(t *testing.T) {
	const secret = "test-secret"

	body := make([]byte, nonceBodyLen)
	binary.BigEndian.PutUint64(body[:nonceTimeLen], uint64(time.Now().Add(-NonceTTL-time.Minute).Unix()))
	stale := base64.RawURLEncoding.EncodeToString(append(body, nonceMAC(secret, body)...))

	err := VerifyNonce(secret, stale)
	if err == nil {
		t.Fatal("an expired nonce verified")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("expired nonce rejected as %q, want the error to name expiry", err)
	}
}

func TestNonceRejectsMalformedInput(t *testing.T) {
	const secret = "test-secret"
	for _, bad := range []string{"", "not base64!", base64.RawURLEncoding.EncodeToString([]byte("short"))} {
		if err := VerifyNonce(secret, bad); err == nil {
			t.Errorf("VerifyNonce(%q) succeeded, want an error", bad)
		}
	}
}
