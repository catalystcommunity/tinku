// Package csilservices implements the generated CSIL service interfaces
// (api/internal/csil). One file per service.
//
// # The error contract
//
// A service method returns one of two things when it fails:
//
//   - *AppError — an EXPECTED failure the caller is meant to see and branch
//     on. The dispatcher encodes it as the declared `ServiceError` arm, at
//     transport status 0. Only ops whose CSIL declaration carries a
//     `/ ServiceError` arm can express one.
//   - any other error — an unexpected failure. The dispatcher logs it and
//     answers with transport status 6 (internal), telling the caller
//     nothing. Detail stays server-side.
//
// Typed failures are opt-in, in other words, and everything else is opaque
// by default. Returning a bare fmt.Errorf from a service method is
// therefore always safe: the worst it can do is hide detail from a caller,
// never leak it.
package csilservices

import (
	"errors"
	"fmt"

	"github.com/catalystcommunity/tinku/api/internal/csil"
)

// Application error codes. These are the values of ServiceError.code and
// the thing a client branches on; the message is for humans. Codes are only
// ever appended to — a client in the wild maps the ones it knows and treats
// the rest as a generic failure.
const (
	// CodeInvalid is a validation failure. Field names the offending input.
	CodeInvalid uint64 = 1
	// CodeUnauthenticated means the caller has no valid session and the op
	// needs one.
	CodeUnauthenticated uint64 = 2
	// CodeForbidden means the caller is known but not allowed.
	CodeForbidden uint64 = 3
	// CodeNotFound means the resource does not exist, or the caller is not
	// permitted to know that it does. ResourceType names what was looked up.
	CodeNotFound uint64 = 4
	// CodeUnavailable means a dependency tinku needs (the linkkeys RP,
	// most often) is unconfigured or unreachable.
	CodeUnavailable uint64 = 5
)

// AppError is an expected failure, safe to show the caller.
type AppError struct {
	Code         uint64
	Message      string
	Field        string
	ResourceType string
}

func (e *AppError) Error() string {
	return fmt.Sprintf("app error %d: %s", e.Code, e.Message)
}

// ServiceError converts to the generated wire type.
func (e *AppError) ServiceError() csil.ServiceError {
	out := csil.ServiceError{Code: e.Code, Message: e.Message}
	if e.Field != "" {
		out.Field = &e.Field
	}
	if e.ResourceType != "" {
		out.ResourceType = &e.ResourceType
	}
	return out
}

// AsAppError reports whether err is (or wraps) an *AppError.
func AsAppError(err error) (*AppError, bool) {
	var appErr *AppError
	ok := errors.As(err, &appErr)
	return appErr, ok
}

// Invalid builds a validation failure against a named field.
func Invalid(field, message string) *AppError {
	return &AppError{Code: CodeInvalid, Message: message, Field: field}
}

// Unauthenticated builds the "you need a session for this" failure.
func Unauthenticated(message string) *AppError {
	return &AppError{Code: CodeUnauthenticated, Message: message}
}

// Forbidden builds the "you have a session but may not do this" failure.
func Forbidden(message string) *AppError {
	return &AppError{Code: CodeForbidden, Message: message}
}

// NotFound builds the missing-resource failure.
func NotFound(resourceType, message string) *AppError {
	return &AppError{Code: CodeNotFound, Message: message, ResourceType: resourceType}
}

// Unavailable builds the "a dependency is not configured or not reachable"
// failure.
func Unavailable(message string) *AppError {
	return &AppError{Code: CodeUnavailable, Message: message}
}
