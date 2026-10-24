// Package email holds the provider-agnostic sending interface and its
// implementations, message rendering, unsubscribe tokens and inbound email
// event processing (deliveries, bounces, complaints, replies).
package email

import (
	"context"
	"errors"
	"fmt"
)

// EmailMessage is a fully rendered message ready for a provider.
type EmailMessage struct {
	MessageID      string // RFC 5322 Message-ID, without angle brackets
	FromEmail      string
	FromName       string
	ReplyTo        string
	ToEmail        string
	ToName         string
	Subject        string
	TextBody       string
	HTMLBody       string
	InReplyTo      string // Message-ID of the previous step, for threading
	UnsubscribeURL string // https one-click endpoint (RFC 8058)
	UnsubscribeTo  string // optional mailto: address
	Headers        map[string]string
}

type SendResult struct {
	Provider          string
	ProviderMessageID string
}

// EmailProvider sends one message. Implementations must return a *SendError
// so the worker can tell permanent failures from transient ones.
type EmailProvider interface {
	Name() string
	Send(ctx context.Context, msg EmailMessage) (SendResult, error)
}

// SendError classifies a provider failure.
type SendError struct {
	Permanent bool // do not retry (e.g. SMTP 5xx: mailbox does not exist)
	Bounce    bool // the recipient address was rejected: suppress it
	// ProviderFault means the provider refused us (bad credentials, account
	// suspended), not the recipient. Every send would fail the same way, so
	// the campaign is paused instead of failing leads one by one.
	ProviderFault bool
	Code          string // provider/SMTP code, for diagnostics
	Err           error
}

func (e *SendError) Error() string {
	kind := "transient"
	if e.Permanent {
		kind = "permanent"
	}
	return fmt.Sprintf("%s send error %s: %v", kind, e.Code, e.Err)
}

func (e *SendError) Unwrap() error { return e.Err }

func AsSendError(err error) (*SendError, bool) {
	var se *SendError
	ok := errors.As(err, &se)
	return se, ok
}
