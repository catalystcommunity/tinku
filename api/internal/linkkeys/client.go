// Package linkkeys is the client for the linkkeys relying-party (RP)
// sidecar tinku runs alongside the api. The api never holds a private
// key: the sidecar does, and exposes sign-request, decrypt-token,
// verify-assertion and userinfo-fetch over JSON/HTTP.
//
// Ported from firepit's api/internal/linkkeys, trimmed to the HTTP
// transport. Firepit and longhouse also carry a CSIL-RPC-over-TCP transport
// with certificate-fingerprint pinning; that is the right thing to add here
// when tinku has a reason to talk to a linkkeys instance that publishes
// fingerprints over DNS, and it slots in behind the PKIClient interface in
// api/internal/csilservices without touching a caller.
package linkkeys

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Assertion is the subset of linkkeys' IdentityAssertion tinku reads.
// Unknown fields are ignored on decode, so an IDP that grows a field does
// not break this client.
type Assertion struct {
	UserID      string `json:"user_id"`
	Domain      string `json:"domain"`
	Audience    string `json:"audience"`
	Nonce       string `json:"nonce"`
	IssuedAt    string `json:"issued_at"`
	ExpiresAt   string `json:"expires_at"`
	DisplayName string `json:"display_name,omitempty"`
}

// Client talks to the RP sidecar over JSON/HTTP.
type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// New builds a Client. allowInvalidCerts skips TLS verification, which is
// only ever right for a dev cluster with a self-signed RP certificate.
func New(baseURL, apiKey string, allowInvalidCerts bool) *Client {
	transport := &http.Transport{}
	if allowInvalidCerts {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // dev-only opt-in
	}
	return &Client{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		HTTPClient: &http.Client{Transport: transport, Timeout: 15 * time.Second},
	}
}

// SignRequest asks the RP to sign an auth request bound to callbackURL and
// nonce. The returned value is base64url and goes on the IDP authorize
// redirect as `signed_request`.
func (c *Client) SignRequest(callbackURL, nonce string) (string, error) {
	var out struct {
		SignedRequest string `json:"signed_request"`
	}
	err := c.post("/v1alpha/sign-request.json",
		map[string]string{"callback_url": callbackURL, "nonce": nonce}, &out)
	if err != nil {
		return "", err
	}
	return out.SignedRequest, nil
}

// DecryptToken decrypts the encrypted_token the IDP hands to the callback.
// The result is signed but not yet verified — callers must then call
// VerifyAssertion on it.
func (c *Client) DecryptToken(encryptedToken string) (string, error) {
	var out struct {
		SignedAssertion string `json:"signed_assertion"`
	}
	err := c.post("/v1alpha/decrypt-token.json",
		map[string]string{"encrypted_token": encryptedToken}, &out)
	if err != nil {
		return "", err
	}
	return out.SignedAssertion, nil
}

// VerifyAssertion checks a signed assertion against expectedDomain's
// published linkkeys keys, which the sidecar fetches over DNS. It returns
// the inner assertion only when the signature checks out.
func (c *Client) VerifyAssertion(signedAssertion, expectedDomain string) (*Assertion, error) {
	var out struct {
		Assertion Assertion `json:"assertion"`
		Verified  bool      `json:"verified"`
	}
	err := c.post("/v1alpha/verify-assertion.json",
		map[string]string{"signed_assertion": signedAssertion, "expected_domain": expectedDomain}, &out)
	if err != nil {
		return nil, err
	}
	if !out.Verified {
		return nil, fmt.Errorf("linkkeys: assertion did not verify against %s", expectedDomain)
	}
	return &out.Assertion, nil
}

func (c *Client) post(path string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("linkkeys: encoding request to %s: %w", path, err)
	}

	req, err := http.NewRequest(http.MethodPost, c.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("linkkeys: building request to %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("X-Api-Key", c.APIKey)
	}

	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("linkkeys: calling %s: %w", path, err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// The body is bounded before it is read into an error message so a
		// misconfigured URL pointing at something enormous cannot be
		// turned into an allocation by a caller's failure path.
		detail, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return fmt.Errorf("linkkeys: %s returned %s: %s", path, res.Status, bytes.TrimSpace(detail))
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("linkkeys: decoding response from %s: %w", path, err)
	}
	return nil
}
