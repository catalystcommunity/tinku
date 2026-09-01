package server

import (
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/catalystcommunity/tinku/api/internal/csilservices"
)

// handleAuthCallback implements `GET /auth/callback` — the one step of the
// linkkeys login flow that cannot be a CSIL op, because an identity provider
// redirects a browser to a plain GET URL and has no way to POST a CSIL-RPC
// envelope.
//
// It is deliberately thin. Everything that decides anything — decrypt,
// verify, audience check, nonce check, upsert, mint session — is
// csilservices.CompleteLogin, so the HTTP entry point and any other caller
// share one implementation. This handler only pulls the query parameter out,
// calls it, and turns the result into a cookie and a redirect, or an error.
func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	encryptedToken := r.URL.Query().Get("encrypted_token")
	if encryptedToken == "" {
		http.Error(w, "missing encrypted_token", http.StatusBadRequest)
		return
	}
	if s.pki == nil {
		log.Error("auth callback: the linkkeys relying party is not configured")
		http.Error(w, "linkkeys is not configured on this server", http.StatusInternalServerError)
		return
	}

	result, err := csilservices.CompleteLogin(r.Context(), s.store, s.pki, s.cfg, encryptedToken)
	if err != nil {
		status, message := authCallbackError(err)
		log.WithError(err).Info("auth callback failed")
		http.Error(w, message, status)
		return
	}

	SetSessionCookie(w, s.cfg, result.RawToken, result.ExpiresAt)

	redirectTo := s.cfg.PostLoginRedirectURL
	if redirectTo == "" {
		redirectTo = "/"
	}
	http.Redirect(w, r, redirectTo, http.StatusFound)
}

// authCallbackError maps a CompleteLogin failure onto an HTTP status and a
// message that is safe to show.
//
// An *AppError names an EXPECTED failure — a bad token, a stale nonce, an
// assertion for someone else's audience — and those are fine to surface
// verbatim, because the person seeing the page needs to know which one
// happened. Anything else is unexpected: it was already logged by the
// caller, and the browser gets nothing but "internal error". This is the
// same contract routeFallible enforces for CSIL ops.
func authCallbackError(err error) (int, string) {
	appErr, ok := csilservices.AsAppError(err)
	if !ok {
		return http.StatusInternalServerError, "internal error"
	}
	switch appErr.Code {
	case csilservices.CodeInvalid:
		return http.StatusBadRequest, appErr.Message
	case csilservices.CodeUnauthenticated:
		return http.StatusUnauthorized, appErr.Message
	case csilservices.CodeForbidden:
		return http.StatusForbidden, appErr.Message
	case csilservices.CodeUnavailable:
		return http.StatusBadGateway, appErr.Message
	default:
		return http.StatusInternalServerError, appErr.Message
	}
}
