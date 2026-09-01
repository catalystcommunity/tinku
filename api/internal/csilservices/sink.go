package csilservices

import "context"

// SessionSink is how a CSIL op changes the caller's session cookie.
//
// A CSIL op returns a typed value, not an HTTP response, so it cannot set a
// header itself — and two ops need to: dev-login mints a session, logout
// ends one. This interface is the seam that lets them say so without
// dragging http.ResponseWriter into the service layer. The HTTP carrier
// implements it (api/internal/server) and acts on whatever the op recorded
// once the op has returned.
//
// Both methods take a context because the implementation keys off the
// in-flight request, and are safe to call on a nil sink's behalf: a service
// constructed without one simply skips them, which is what a non-HTTP
// caller wants.
type SessionSink interface {
	// SetSession asks for result's raw token to be sent as a session cookie.
	SetSession(ctx context.Context, result *LoginResult)
	// ClearSession asks for the session cookie to be expired.
	ClearSession(ctx context.Context)
}
