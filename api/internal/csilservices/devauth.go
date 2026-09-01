package csilservices

import (
	"context"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/catalystcommunity/tinku/api/internal/config"
	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/store"
)

// DevAuthService implements csil.DevAuthService: it mints a real session for
// a handle with no identity assertion at all, so the whole stack runs with no
// linkkeys relying party configured.
//
// The gates are checked twice, deliberately. `tinku serve` only builds this
// service when config.DevAuthAllowed passes, so in a production deployment
// the ops are not registered and the wire answers "unknown service or op".
// DevLogin re-checks anyway, because a wiring mistake that registered it
// somewhere it does not belong must fail closed rather than mint sessions.
type DevAuthService struct {
	Store store.Store
	Cfg   config.Config
	// Sink is where the minted session is handed back to the HTTP layer,
	// which is the only layer that can set a cookie header. See
	// SessionSink.
	Sink SessionSink
}

var _ csil.DevAuthService = (*DevAuthService)(nil)

// DevLogin mints a session for `handle@domain`, creating the user the first
// time a handle is used.
func (s *DevAuthService) DevLogin(ctx context.Context, req csil.DevLoginRequest) (csil.UserProfile, error) {
	if !s.Cfg.DevAuthAllowed() {
		log.WithFields(log.Fields{"env": s.Cfg.Env, "dev_auth": s.Cfg.DevAuthEnabled}).
			Warn("dev-login reached a server where dev auth is not allowed; refusing")
		return csil.UserProfile{}, Forbidden("dev auth is not enabled on this server")
	}

	handle := strings.ToLower(strings.TrimSpace(req.Handle))
	if handle == "" {
		return csil.UserProfile{}, Invalid("handle", "a handle is required")
	}
	domain := strings.TrimSpace(req.Domain)
	if domain == "" {
		return csil.UserProfile{}, Invalid("domain", "a domain is required")
	}

	user, err := s.Store.UpsertUser(ctx, store.UpsertUserParams{
		LinkkeysDomain: domain,
		// The linkkeys user id a real assertion would carry. Constructing
		// it the same way keeps a dev user and a real one indistinguishable
		// to everything downstream of this function.
		LinkkeysUserID: handle + "@" + domain,
		Handle:         handle,
		DisplayName:    handle,
		Kind:           store.UserKindHuman,
	})
	if err != nil {
		return csil.UserProfile{}, err
	}

	result, err := MintSession(ctx, s.Store, user, s.Cfg.SessionTTL)
	if err != nil {
		return csil.UserProfile{}, err
	}
	if s.Sink != nil {
		s.Sink.SetSession(ctx, result)
	}

	log.WithField("handle", handle).Warn("DEV AUTH: minted a session with no identity assertion")
	return ToUserProfile(user), nil
}
