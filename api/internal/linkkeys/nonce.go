package linkkeys

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"time"
)

// NonceTTL is how long a minted nonce stays acceptable — long enough for a
// person to finish logging in, short enough that a captured authorize URL
// stops being useful quickly.
const NonceTTL = 10 * time.Minute

// nonce layout: 8 bytes big-endian issue time (Unix seconds) || 16 random
// bytes || 32-byte HMAC-SHA256 over the first 24. Base64url, unpadded.
const (
	nonceTimeLen   = 8
	nonceRandomLen = 16
	nonceBodyLen   = nonceTimeLen + nonceRandomLen
	nonceMACLen    = sha256.Size
)

// MintNonce returns a self-verifying nonce. "Self-verifying" means tinku
// stores nothing: the nonce carries its own issue time and its own MAC, so
// VerifyNonce can accept or reject it with no server-side state and nothing
// to clean up. That matters because the nonce leaves the api entirely — it
// travels to the RP, to the IDP, through the user's browser, and comes back
// inside the assertion.
func MintNonce(secret string) string {
	body := make([]byte, nonceBodyLen)
	binary.BigEndian.PutUint64(body[:nonceTimeLen], uint64(time.Now().Unix()))
	if _, err := rand.Read(body[nonceTimeLen:]); err != nil {
		// Without randomness the nonce is guessable, which defeats its
		// only purpose, so there is nothing useful to return here.
		panic("linkkeys: could not generate nonce randomness: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(append(body, nonceMAC(secret, body)...))
}

// VerifyNonce checks a nonce's MAC and age against secret. It returns an
// error naming which check failed, so a caller can log the difference
// between a tampered nonce and a login somebody left open over lunch.
func VerifyNonce(secret, nonce string) error {
	raw, err := base64.RawURLEncoding.DecodeString(nonce)
	if err != nil {
		return fmt.Errorf("nonce is not valid base64url: %w", err)
	}
	if len(raw) != nonceBodyLen+nonceMACLen {
		return fmt.Errorf("nonce is %d bytes, expected %d", len(raw), nonceBodyLen+nonceMACLen)
	}

	body, mac := raw[:nonceBodyLen], raw[nonceBodyLen:]
	// Constant-time comparison: a variable-time one leaks how much of a
	// forged MAC was correct, which is enough to forge the rest.
	if !hmac.Equal(mac, nonceMAC(secret, body)) {
		return fmt.Errorf("nonce signature does not verify")
	}

	issued := time.Unix(int64(binary.BigEndian.Uint64(body[:nonceTimeLen])), 0)
	if age := time.Since(issued); age > NonceTTL {
		return fmt.Errorf("nonce expired %s ago", (age - NonceTTL).Truncate(time.Second))
	}
	return nil
}

func nonceMAC(secret string, body []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return mac.Sum(nil)
}
