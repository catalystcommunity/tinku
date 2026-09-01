package csilservices

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/catalystcommunity/tinku/api/internal/config"
	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/linkkeys"
	"github.com/catalystcommunity/tinku/api/internal/reqctx"
	"github.com/catalystcommunity/tinku/api/internal/store"
)

// PKIClient is the slice of the linkkeys relying-party sidecar the login
// flow uses. It is an interface rather than the concrete client so tests can
// drive the flow without a sidecar, and so a second transport (CSIL-RPC over
// TCP, as firepit and longhouse have) can be dropped in without touching
// this file.
type PKIClient interface {
	SignRequest(callbackURL, nonce string) (string, error)
	DecryptToken(encryptedToken string) (string, error)
	VerifyAssertion(signedAssertion, expectedDomain string) (*linkkeys.Assertion, error)
}

// AuthService implements csil.AuthService.
//
// Only begin-login, logout and whoami are CSIL ops. The middle of the flow —
// the identity provider redirecting the browser back — cannot be: an IDP
// redirects to a plain GET URL and has no way to POST a CSIL-RPC envelope.
// That step is `GET /auth/callback`, and CompleteLogin below is what it
// calls, so the two entry points share one implementation.
type AuthService struct {
	Store store.Store
	Cfg   config.Config
	// PKI is nil when the relying party is unconfigured. begin-login then
	// refuses with CodeUnavailable, while logout and whoami keep working
	// for anyone who already holds a session (from dev-auth, typically).
	PKI PKIClient
	// Sink lets Logout expire the session cookie, which only the HTTP layer
	// can actually do.
	Sink SessionSink
}

var _ csil.AuthService = (*AuthService)(nil)

// BeginLogin mints a self-verifying nonce, has the RP sign an auth request
// bound to it, and returns the IDP URL for the SPA to navigate to.
func (s *AuthService) BeginLogin(ctx context.Context, req csil.BeginLoginRequest) (csil.BeginLoginResponse, error) {
	domain := strings.TrimSpace(req.Domain)
	if domain == "" {
		return csil.BeginLoginResponse{}, Invalid("domain", "a linkkeys domain is required")
	}
	if s.PKI == nil {
		return csil.BeginLoginResponse{}, Unavailable("linkkeys is not configured on this server")
	}

	nonce := linkkeys.MintNonce(s.Cfg.SessionNonceSecret)
	signedRequest, err := s.PKI.SignRequest(s.Cfg.AppCallbackURL, nonce)
	if err != nil {
		log.WithError(err).Error("begin-login: relying party could not sign the auth request")
		return csil.BeginLoginResponse{}, Unavailable("could not reach the identity service")
	}

	query := url.Values{}
	query.Set("signed_request", signedRequest)
	return csil.BeginLoginResponse{
		RedirectUrl: strings.TrimRight(s.Cfg.LinkkeysIDPURL, "/") + "/auth/authorize?" + query.Encode(),
	}, nil
}

// Logout deletes exactly the session the caller's cookie names. It is
// idempotent: a caller with no session, or with a stale one, gets the same
// empty success as one who was logged in. The cookie itself is cleared by
// the HTTP layer, which is the only layer that can set a header.
func (s *AuthService) Logout(ctx context.Context, _ csil.Empty) (csil.Empty, error) {
	// The cookie is expired whether or not there was a live session behind
	// it: a caller holding a stale cookie asked to be logged out, and
	// leaving it in their browser to fail every request is not that.
	if s.Sink != nil {
		s.Sink.ClearSession(ctx)
	}

	session, ok := reqctx.Session(ctx)
	if !ok {
		return csil.Empty{}, nil
	}
	if err := s.Store.DeleteSession(ctx, session.ID); err != nil {
		// logout has no declared error arm, so this becomes a transport
		// internal error. Logging it here is what makes it diagnosable.
		log.WithError(err).Error("logout: deleting session")
		return csil.Empty{}, err
	}
	return csil.Empty{}, nil
}

// Whoami returns the caller's own profile.
func (s *AuthService) Whoami(ctx context.Context, _ csil.Empty) (csil.UserProfile, error) {
	user, ok := reqctx.User(ctx)
	if !ok {
		return csil.UserProfile{}, Unauthenticated("no active session")
	}
	return ToUserProfile(user), nil
}

// ToUserProfile converts a stored user to the wire type. Exported because
// DevAuthService returns the same shape.
func ToUserProfile(user *store.User) csil.UserProfile {
	return csil.UserProfile{
		Id:             csil.UserID(user.ID),
		LinkkeysDomain: user.LinkkeysDomain,
		Handle:         user.Handle,
		DisplayName:    user.DisplayName,
		Kind:           csil.UserKind(user.Kind),
		CreatedAt:      user.CreatedAt,
	}
}

// LoginResult is what a completed login produces: the stored session and the
// raw cookie value, which exists only in this return value — the database
// holds nothing but its hash.
type LoginResult struct {
	User      *store.User
	Session   *store.Session
	RawToken  string
	ExpiresAt time.Time
}

// CompleteLogin is the second half of the login flow, called by
// `GET /auth/callback`: decrypt the token the IDP returned, verify the
// assertion against the IDP's published keys, check the audience and the
// nonce we minted in BeginLogin, upsert the user, and mint a session.
//
// Every failure here is an *AppError with a code the callback maps to an
// HTTP status, because these are all failures a person hitting the URL can
// cause and should see named.
func CompleteLogin(ctx context.Context, st store.Store, pki PKIClient, cfg config.Config, encryptedToken string) (*LoginResult, error) {
	if pki == nil {
		return nil, Unavailable("linkkeys is not configured on this server")
	}

	signedAssertion, err := pki.DecryptToken(encryptedToken)
	if err != nil {
		log.WithError(err).Info("auth callback: token would not decrypt")
		return nil, Invalid("encrypted_token", "the login token could not be decrypted")
	}

	assertion, err := pki.VerifyAssertion(signedAssertion, cfg.LinkkeysIDPDomain)
	if err != nil {
		log.WithError(err).Info("auth callback: assertion would not verify")
		return nil, Invalid("encrypted_token", "the identity assertion did not verify")
	}

	// The audience check is what stops an assertion minted for a different
	// relying party from being replayed at tinku.
	if expected := cfg.LinkkeysRPDomain(); expected != "" && assertion.Audience != expected {
		log.WithFields(log.Fields{"got": assertion.Audience, "want": expected}).
			Info("auth callback: assertion audience mismatch")
		return nil, Forbidden("this identity assertion was issued for a different service")
	}

	if err := linkkeys.VerifyNonce(cfg.SessionNonceSecret, assertion.Nonce); err != nil {
		log.WithError(err).Info("auth callback: nonce rejected")
		return nil, Invalid("nonce", "this login has expired or was not started here; try again")
	}

	handle := assertion.UserID
	if at := strings.Index(handle, "@"); at > 0 {
		handle = handle[:at]
	}
	// Lowercased on the way in, as dev-auth already does. An address is not
	// case-sensitive, and every lookup lowercases what it is given
	// (AdminService.FindUser), so storing `Alice` verbatim would make that
	// person unfindable by the only op that resolves an address to an id.
	handle = strings.ToLower(handle)
	user, err := st.UpsertUser(ctx, store.UpsertUserParams{
		LinkkeysDomain: strings.ToLower(assertion.Domain),
		LinkkeysUserID: assertion.UserID,
		Handle:         handle,
		DisplayName:    assertion.DisplayName,
		Kind:           store.UserKindHuman,
	})
	if err != nil {
		return nil, fmt.Errorf("upserting user from assertion: %w", err)
	}

	return MintSession(ctx, st, user, cfg.SessionTTL)
}

// MintSession creates a session for user and returns the raw cookie value
// alongside it. Exported because DevAuthService mints sessions the same way
// — the two login paths differ in how identity is established, never in what
// a session is.
func MintSession(ctx context.Context, st store.Store, user *store.User, ttl time.Duration) (*LoginResult, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generating session token: %w", err)
	}
	token := hex.EncodeToString(raw)

	session := &store.Session{
		ID:        store.NewID(),
		UserID:    user.ID,
		TokenHash: HashToken(token),
		ExpiresAt: time.Now().UTC().Add(ttl),
	}
	if err := st.CreateSession(ctx, session); err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}
	return &LoginResult{User: user, Session: session, RawToken: token, ExpiresAt: session.ExpiresAt}, nil
}

// HashToken is the one-way function between the cookie value and what the
// sessions table stores. A read of the database therefore cannot produce a
// usable cookie. SHA-256 with no salt or stretching is right here and would
// be wrong for a password: the input is 32 bytes of randomness this server
// generated, so there is no dictionary to attack.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
