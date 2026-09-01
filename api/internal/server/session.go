package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/catalystcommunity/tinku/api/internal/config"
	"github.com/catalystcommunity/tinku/api/internal/csilservices"
	"github.com/catalystcommunity/tinku/api/internal/reqctx"
	"github.com/catalystcommunity/tinku/api/internal/store"
)

// SessionCookieName is the cookie tinku's own session lives in. Sessions
// are tinku's; linkkeys verifies identity and issues nothing.
const SessionCookieName = "tinku_session"

// sessionMiddleware reads the session cookie, looks it up, and attaches the
// user AND the session row to the request context. Logout needs the row so
// it deletes exactly the session the caller presented rather than every
// session that user holds.
//
// A missing cookie, an unknown token and an expired session all resolve to
// "no user attached". None of them is an error: anonymous reads are a
// feature, so this middleware never rejects a request, it only enriches the
// context for the ops that care.
func sessionMiddleware(st store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
				session, user, lookupErr := st.SessionByTokenHash(ctx, csilservices.HashToken(cookie.Value))
				switch {
				case lookupErr == nil:
					ctx = reqctx.WithUser(ctx, user)
					ctx = reqctx.WithSession(ctx, session)
				case errors.Is(lookupErr, store.ErrNotFound):
					// Expired or unknown. Anonymous from here on.
				default:
					// A database failure must not silently downgrade a
					// logged-in caller to anonymous without a trace.
					log.WithError(lookupErr).Error("session lookup failed; treating caller as anonymous")
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// sessionSink holds whatever a CSIL op decided about the caller's cookie
// while the request was being handled. It is per-request state, reached
// through the context, because the op that writes it and the middleware that
// acts on it never see each other.
type sessionSink struct {
	mu      sync.Mutex
	result  *csilservices.LoginResult
	cleared bool
}

type sinkKey struct{}

// sessionSinkBridge implements csilservices.SessionSink. It is stateless —
// every request's state lives on that request's context — so one value
// serves the whole process and is handed to the services at construction.
type sessionSinkBridge struct{}

// NewSessionSink returns the SessionSink to give services that change the
// session cookie. For a request that did not come through
// sessionCookieMiddleware (a direct service-level test, say) both methods
// are no-ops.
func NewSessionSink() csilservices.SessionSink { return &sessionSinkBridge{} }

func (*sessionSinkBridge) SetSession(ctx context.Context, result *csilservices.LoginResult) {
	if sink, ok := ctx.Value(sinkKey{}).(*sessionSink); ok && sink != nil {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		sink.result, sink.cleared = result, false
	}
}

func (*sessionSinkBridge) ClearSession(ctx context.Context) {
	if sink, ok := ctx.Value(sinkKey{}).(*sessionSink); ok && sink != nil {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		sink.result, sink.cleared = nil, true
	}
}

// sessionCookieMiddleware attaches a sink to each request and writes the
// Set-Cookie header for whatever an op recorded while handling it.
func sessionCookieMiddleware(cfg config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sink := &sessionSink{}
			ctx := context.WithValue(r.Context(), sinkKey{}, sink)

			// A cookie has to be set before the handler writes its status,
			// because the headers are flushed by then. Wrapping the
			// ResponseWriter is what makes that ordering possible.
			wrapped := &cookieWriter{ResponseWriter: w, sink: sink, cfg: cfg}
			next.ServeHTTP(wrapped, r.WithContext(ctx))
			// A handler that returned without writing anything still needs
			// its cookie emitted.
			wrapped.emitCookie()
		})
	}
}

// cookieWriter emits the pending cookie change exactly once, at whichever
// comes first: the handler writing, or the middleware finishing.
type cookieWriter struct {
	http.ResponseWriter
	sink    *sessionSink
	cfg     config.Config
	emitted bool
}

func (w *cookieWriter) WriteHeader(status int) {
	w.emitCookie()
	w.ResponseWriter.WriteHeader(status)
}

func (w *cookieWriter) Write(b []byte) (int, error) {
	w.emitCookie()
	return w.ResponseWriter.Write(b)
}

func (w *cookieWriter) emitCookie() {
	if w.emitted {
		return
	}
	w.emitted = true

	w.sink.mu.Lock()
	result, cleared := w.sink.result, w.sink.cleared
	w.sink.mu.Unlock()

	switch {
	case result != nil:
		SetSessionCookie(w.ResponseWriter, w.cfg, result.RawToken, result.ExpiresAt)
	case cleared:
		ClearSessionCookie(w.ResponseWriter, w.cfg)
	}
}

// SetSessionCookie writes the session cookie. HttpOnly keeps the value away
// from page scripts, so an XSS bug cannot read a session out of the
// document. SameSite=Lax lets the identity provider's redirect arrive with
// the cookie attached, which Strict would block.
func SetSessionCookie(w http.ResponseWriter, cfg config.Config, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   cfg.SessionCookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie expires the cookie in the browser. The session row is
// deleted separately by AuthService.Logout; this is the half only the HTTP
// layer can do.
func ClearSessionCookie(w http.ResponseWriter, cfg config.Config) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cfg.SessionCookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}
