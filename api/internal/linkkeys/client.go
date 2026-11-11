// Package linkkeys is the login client for tinku's linkkeys relying-party
// (RP) server. The api never holds a private key: the RP server does. The
// api calls its `Rp` service over CSIL-RPC on TCP+TLS, pinned to the RP's
// certificate fingerprints, through the regular-rp Go SDK. Federation uses
// the same SDK transport for application keys (cmd/serve.go).
//
// Linkkeys v0.16.7 keys standing consent by (user, RP domain, application).
// Every sign-request therefore names tinku's application ID and sends
// tinku's own claim request: the RP's RP_CLAIMS_CONFIG is empty on the
// shared instances, and an app that sends no request gets no claims.
package linkkeys

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	regularrp "github.com/catalystcommunity/linkkeys/sdks/regular-rp/go"
	api "github.com/catalystcommunity/linkkeys/sdks/regular-rp/go/generated"
)

// Claim types tinku asks for.
const (
	ClaimHandle      = "handle"
	ClaimDisplayName = "display_name"
)

// ClaimRequest is what tinku asks a person's identity domain to release.
//
// handle is REQUIRED. Tinku shows every person as `handle@domain`, and an
// address made from the opaque user ID is not one a person can read. Silent
// consent opens the consent screen again only for a new required claim, so
// a claim the app depends on must be required.
//
// display_name is optional. A person without one signs in as usual.
func ClaimRequest() *api.ClaimRequest {
	return &api.ClaimRequest{
		Required: []api.RequestedClaim{{ClaimType: ClaimHandle, Datatype: "text"}},
		Optional: []api.RequestedClaim{{ClaimType: ClaimDisplayName, Datatype: "text"}},
	}
}

// Assertion is the part of linkkeys' IdentityAssertion that tinku reads.
type Assertion struct {
	UserID           string
	Domain           string
	Audience         string
	Nonce            string
	IssuedAt         string
	ExpiresAt        string
	AuthorizedClaims []string
	DisplayName      string
}

// UserInfo is the identity and the claims that the person released to
// tinku, as claim type to text value.
type UserInfo struct {
	UserID      string
	Domain      string
	DisplayName string
	Claims      map[string]string
}

// Config configures a Client.
type Config struct {
	// TCPAddress is the RP server's CSIL-RPC endpoint ("host:port").
	TCPAddress string
	// Fingerprints are the pinned SHA-256 hex fingerprints of the RP
	// server's TLS key. They are the trust anchor: linkkeys certificates
	// are self-signed by design.
	Fingerprints []string
	// APIKey is the RP API key.
	APIKey string
	// ApplicationID names tinku under the RP API-key account. The IDP keys
	// standing consent by it. Keep it stable: a change makes every person
	// consent again. Use the same value as the federation application ID.
	ApplicationID string
	// APIBase is the identity domain's https API base. userinfo-fetch
	// redeems the token there.
	APIBase string
}

// Client calls the RP server's `Rp` service.
type Client struct {
	rp            *api.RpClient
	applicationID string
	apiBase       string
}

// New builds a Client over a pinned TLS transport.
func New(cfg Config) (*Client, error) {
	if err := ValidApplicationID(cfg.ApplicationID); err != nil {
		return nil, err
	}
	transport, err := regularrp.NewPinnedRpcTransport(regularrp.PinnedRpcTransportOptions{
		TCPAddress:     cfg.TCPAddress,
		Fingerprints:   cfg.Fingerprints,
		APIKey:         cfg.APIKey,
		RequestTimeout: 15 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("linkkeys: configuring the RP transport: %w", err)
	}
	return NewWithTransport(transport, cfg.ApplicationID, cfg.APIBase), nil
}

// NewWithTransport builds a Client over any generated-client transport.
// Tests use it with a fake transport that records the requests.
func NewWithTransport(transport api.Transport, applicationID, apiBase string) *Client {
	return &Client{
		rp:            api.NewRpClient(transport),
		applicationID: applicationID,
		apiBase:       strings.TrimRight(apiBase, "/"),
	}
}

// ValidApplicationID applies the linkkeys identifier rule: 1 to 128 of
// A-Z a-z 0-9 - _ . The RP refuses any other value, so tinku refuses it at
// startup rather than at the first sign-in.
func ValidApplicationID(id string) error {
	if id == "" || len(id) > 128 {
		return errors.New("linkkeys: the application ID must be 1 to 128 characters")
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.'
		if !ok {
			return fmt.Errorf("linkkeys: the application ID %q has a character other than A-Z a-z 0-9 - _ .", id)
		}
	}
	return nil
}

// SignRequest asks the RP to sign an auth request bound to callbackURL and
// nonce. It names tinku's application and sends tinku's claim request. The
// result is base64url and goes on the IDP authorize URL as `signed_request`.
func (c *Client) SignRequest(ctx context.Context, callbackURL, nonce string) (string, error) {
	appID := c.applicationID
	out, err := c.rp.SignRequest(ctx, api.RpSignRequest{
		CallbackUrl:     callbackURL,
		Nonce:           nonce,
		ApplicationId:   &appID,
		RequestedClaims: ClaimRequest(),
	})
	if err != nil {
		return "", fmt.Errorf("linkkeys: sign-request: %w", err)
	}
	return out.SignedRequest, nil
}

// DecryptToken decrypts the encrypted_token that the IDP gives the
// callback. The result is signed but not verified: call VerifyAssertion
// next.
func (c *Client) DecryptToken(ctx context.Context, encryptedToken string) (string, error) {
	out, err := c.rp.DecryptToken(ctx, api.RpDecryptRequest{EncryptedToken: encryptedToken})
	if err != nil {
		return "", fmt.Errorf("linkkeys: decrypt-token: %w", err)
	}
	return out.SignedAssertion, nil
}

// VerifyAssertion checks a signed assertion against expectedDomain's
// published keys. It returns the assertion only when the signature is
// correct. The RP checks the signature only: the caller must still check
// the domain, the audience, the nonce and the times.
func (c *Client) VerifyAssertion(ctx context.Context, signedAssertion, expectedDomain string) (*Assertion, error) {
	out, err := c.rp.VerifyAssertion(ctx, api.RpVerifyRequest{
		SignedAssertion: signedAssertion,
		ExpectedDomain:  expectedDomain,
	})
	if err != nil {
		return nil, fmt.Errorf("linkkeys: verify-assertion: %w", err)
	}
	if !out.Verified {
		return nil, fmt.Errorf("linkkeys: assertion did not verify against %s", expectedDomain)
	}
	a := out.Assertion
	result := &Assertion{
		UserID:           a.UserId,
		Domain:           a.Domain,
		Audience:         a.Audience,
		Nonce:            a.Nonce,
		IssuedAt:         a.IssuedAt,
		ExpiresAt:        a.ExpiresAt,
		AuthorizedClaims: a.AuthorizedClaims,
	}
	if a.DisplayName != nil {
		result.DisplayName = *a.DisplayName
	}
	return result, nil
}

// FetchUserInfo redeems the verified signed assertion for the claims that
// the person released to tinku. domain is the assertion's domain.
func (c *Client) FetchUserInfo(ctx context.Context, signedAssertion, domain string) (*UserInfo, error) {
	out, err := c.rp.UserinfoFetch(ctx, api.RpUserInfoRequest{
		Token:   signedAssertion,
		ApiBase: c.apiBase,
		Domain:  domain,
	})
	if err != nil {
		return nil, fmt.Errorf("linkkeys: userinfo-fetch: %w", err)
	}
	info := &UserInfo{
		UserID:      out.UserId,
		Domain:      out.Domain,
		DisplayName: out.DisplayName,
		Claims:      make(map[string]string, len(out.Claims)),
	}
	for _, claim := range out.Claims {
		if claim.ClaimType == "" || claim.RevokedAt != nil {
			continue
		}
		// The well-known identity claims are UTF-8 text.
		info.Claims[claim.ClaimType] = string(claim.ClaimValue)
	}
	return info, nil
}
