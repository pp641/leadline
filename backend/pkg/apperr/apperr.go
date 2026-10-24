// Package apperr defines typed application errors that the HTTP layer maps to
// status codes. Services return these; handlers never decide status codes.
package apperr

import (
	"errors"
	"fmt"
)

type Kind int

const (
	KindInternal Kind = iota
	KindInvalid
	KindNotFound
	KindConflict
	KindUnauthorized
	KindForbidden
	KindRateLimited
	KindUnavailable
)

type Error struct {
	Kind    Kind
	Code    string
	Message string
	Fields  map[string]string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

func Invalid(msg string, fields map[string]string) *Error {
	return &Error{Kind: KindInvalid, Code: "invalid_request", Message: msg, Fields: fields}
}

func NotFound(what string) *Error {
	return &Error{Kind: KindNotFound, Code: "not_found", Message: what + " not found"}
}

func Conflict(code, msg string) *Error {
	return &Error{Kind: KindConflict, Code: code, Message: msg}
}

func Unauthorized(msg string) *Error {
	return &Error{Kind: KindUnauthorized, Code: "unauthorized", Message: msg}
}

func Forbidden(msg string) *Error {
	return &Error{Kind: KindForbidden, Code: "forbidden", Message: msg}
}

func RateLimited() *Error {
	return &Error{Kind: KindRateLimited, Code: "rate_limited", Message: "too many requests"}
}

// Unavailable reports a dependency that is down; err is logged, not shown.
func Unavailable(msg string, err error) *Error {
	return &Error{Kind: KindUnavailable, Code: "unavailable", Message: msg, Err: err}
}

// As extracts an *Error from err, if any.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

func IsNotFound(err error) bool {
	e, ok := As(err)
	return ok && e.Kind == KindNotFound
}
