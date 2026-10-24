// Package validate provides a tiny field-error collector used by services to
// validate input before touching the database.
package validate

import (
	"net/mail"
	"regexp"
	"strings"

	"leadline/pkg/apperr"
)

type Errors map[string]string

func (v Errors) Add(field, msg string) {
	if _, exists := v[field]; !exists {
		v[field] = msg
	}
}

func (v Errors) Check(ok bool, field, msg string) {
	if !ok {
		v.Add(field, msg)
	}
}

func (v Errors) Required(field, value string) {
	v.Check(strings.TrimSpace(value) != "", field, "is required")
}

func (v Errors) MaxLen(field, value string, n int) {
	v.Check(len(value) <= n, field, "is too long")
}

// Err returns an apperr validation error, or nil when there are no errors.
func (v Errors) Err() error {
	if len(v) == 0 {
		return nil
	}
	return apperr.Invalid("validation failed", v)
}

var emailRe = regexp.MustCompile(`^[A-Za-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$`)

// Email reports whether s is a syntactically valid bare address (no display name).
func Email(s string) bool {
	if len(s) > 254 || !emailRe.MatchString(s) {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}

// NormalizeEmail lowercases and trims an address.
func NormalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// OneOf reports whether s is in allowed.
func OneOf(s string, allowed ...string) bool {
	for _, a := range allowed {
		if s == a {
			return true
		}
	}
	return false
}
