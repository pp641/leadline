// Package verification checks contact email addresses before they can be
// used in campaigns. Verification providers sit behind EmailVerifier.
package verification

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"leadline/pkg/validate"
)

type Status string

const (
	StatusValid      Status = "valid"
	StatusInvalid    Status = "invalid"
	StatusRisky      Status = "risky"
	StatusUnknown    Status = "unknown"
	StatusDisposable Status = "disposable"
	StatusRoleBased  Status = "role_based"
)

type VerificationResult struct {
	Status   Status `json:"status"`
	Reason   string `json:"reason"`
	Provider string `json:"provider"`
	MXFound  *bool  `json:"mx_found,omitempty"`
}

type EmailVerifier interface {
	Verify(ctx context.Context, email string) (VerificationResult, error)
}

// Basic is the built-in verifier: syntax, disposable-domain and role-address
// checks plus a DNS MX lookup. It does not open SMTP connections to probe
// mailboxes, so "valid" here means "deliverable domain, well-formed,
// non-role address". Companies from the company database arrive already checked
// by company-scraper.
type Basic struct {
	resolver *net.Resolver
	mxLookup bool
	// AllowTestDomains treats RFC 2606 reserved domains (*.test, *.example,
	// example.com/net/org) as valid. Development only: it lets seeded and
	// generated data flow through campaigns into Mailpit.
	AllowTestDomains bool

	mu    sync.Mutex
	cache map[string]mxEntry
}

type mxEntry struct {
	found bool
	err   bool
	at    time.Time
}

func NewBasic(mxLookup bool) *Basic {
	return &Basic{resolver: net.DefaultResolver, mxLookup: mxLookup, cache: map[string]mxEntry{}}
}

func (b *Basic) Verify(ctx context.Context, email string) (VerificationResult, error) {
	email = validate.NormalizeEmail(email)
	res := VerificationResult{Provider: "basic"}
	if !validate.Email(email) {
		res.Status, res.Reason = StatusInvalid, "invalid syntax"
		return res, nil
	}
	local, domain, _ := strings.Cut(email, "@")
	if b.AllowTestDomains && isTestDomain(domain) {
		res.Status, res.Reason = StatusValid, "reserved test domain (development mode)"
		if IsRoleAddress(local) {
			res.Status = StatusRoleBased
		}
		return res, nil
	}
	if disposableDomains[domain] {
		res.Status, res.Reason = StatusDisposable, "disposable email provider"
		return res, nil
	}
	if b.mxLookup {
		found, temporary := b.lookupMX(ctx, domain)
		res.MXFound = &found
		if temporary {
			res.Status, res.Reason = StatusUnknown, "DNS lookup failed; try again later"
			return res, nil
		}
		if !found {
			res.Status, res.Reason = StatusInvalid, "domain does not accept email"
			return res, nil
		}
	}
	if IsRoleAddress(local) {
		res.Status, res.Reason = StatusRoleBased, "role-based address ("+local+"@)"
		return res, nil
	}
	if !b.mxLookup {
		res.Status, res.Reason = StatusUnknown, "syntax ok; DNS checks disabled"
		return res, nil
	}
	res.Status, res.Reason = StatusValid, "syntax and MX records ok"
	return res, nil
}

func (b *Basic) lookupMX(ctx context.Context, domain string) (found, temporary bool) {
	b.mu.Lock()
	if e, ok := b.cache[domain]; ok && time.Since(e.at) < 6*time.Hour && !e.err {
		b.mu.Unlock()
		return e.found, false
	}
	b.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	mx, err := b.resolver.LookupMX(ctx, domain)
	if err == nil && len(mx) > 0 && !(len(mx) == 1 && mx[0].Host == ".") { // "." = null MX (RFC 7505)
		found = true
	} else if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			// No MX: RFC 5321 falls back to the A/AAAA record.
			if addrs, aerr := b.resolver.LookupHost(ctx, domain); aerr == nil && len(addrs) > 0 {
				found = true
			}
		} else {
			temporary = true
		}
	}
	b.mu.Lock()
	b.cache[domain] = mxEntry{found: found, err: temporary, at: time.Now()}
	b.mu.Unlock()
	return found, temporary
}

var roleLocals = map[string]bool{
	"admin": true, "administrator": true, "billing": true, "booking": true, "bookings": true,
	"careers": true, "contact": true, "enquiries": true, "enquiry": true, "feedback": true,
	"hello": true, "help": true, "hi": true, "hr": true, "info": true, "inquiries": true,
	"jobs": true, "marketing": true, "media": true, "noreply": true, "no-reply": true,
	"office": true, "orders": true, "postmaster": true, "press": true, "reception": true,
	"reservations": true, "sales": true, "support": true, "team": true, "webmaster": true,
	"abuse": true, "accounts": true, "mail": true, "service": true,
}

func isTestDomain(d string) bool {
	switch d {
	case "example.com", "example.net", "example.org":
		return true
	}
	return strings.HasSuffix(d, ".test") || strings.HasSuffix(d, ".example")
}

// IsRoleAddress reports whether the local part is a shared/role mailbox.
func IsRoleAddress(local string) bool {
	return roleLocals[strings.ToLower(local)]
}

// A short built-in list; production deployments should use a maintained list
// or a verification provider.
var disposableDomains = map[string]bool{
	"mailinator.com": true, "10minutemail.com": true, "guerrillamail.com": true, "tempmail.com": true,
	"temp-mail.org": true, "yopmail.com": true, "trashmail.com": true, "getnada.com": true,
	"dispostable.com": true, "sharklasers.com": true, "maildrop.cc": true, "throwawaymail.com": true,
	"fakeinbox.com": true, "mintemail.com": true, "mohmal.com": true, "emailondeck.com": true,
	"tempail.com": true, "burnermail.io": true, "spamgourmet.com": true, "mailnesia.com": true,
}
