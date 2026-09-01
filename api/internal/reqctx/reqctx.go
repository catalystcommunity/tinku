// Package reqctx carries per-request identity on a context.Context.
//
// It exists as its own dependency-free package so both sides of the request
// path can reach it without a cycle: api/internal/server puts the session's
// user on the context, and api/internal/csilservices reads it back — and
// server already imports csilservices to build its dispatch table, so
// csilservices cannot import server.
package reqctx

import (
	"context"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

type contextKey int

const (
	userKey contextKey = iota
	sessionKey
)

// WithUser returns a copy of ctx carrying the authenticated user.
func WithUser(ctx context.Context, user *store.User) context.Context {
	return context.WithValue(ctx, userKey, user)
}

// User returns the authenticated user on ctx. ok is false for an anonymous
// caller, which is a normal state (reads are anonymous), never an error on
// its own — each service method decides whether it needs a user.
func User(ctx context.Context) (*store.User, bool) {
	user, ok := ctx.Value(userKey).(*store.User)
	return user, ok && user != nil
}

// WithSession returns a copy of ctx carrying the session row the request's
// cookie names. Logout needs the row so it can delete exactly the session
// the caller presented, not every session that user holds.
func WithSession(ctx context.Context, session *store.Session) context.Context {
	return context.WithValue(ctx, sessionKey, session)
}

// Session returns the session row on ctx, if the caller presented a valid
// cookie.
func Session(ctx context.Context) (*store.Session, bool) {
	session, ok := ctx.Value(sessionKey).(*store.Session)
	return session, ok && session != nil
}
