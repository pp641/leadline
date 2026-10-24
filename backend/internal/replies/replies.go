// Package replies defines inbound reply processing (Phase 2):
//
//	Mailbox → Inbound Email → Parser → Lead Matcher → Classifier → CRM
//
// Milestone 1 already records replies (manually or via provider webhooks)
// through email.Events.Ingest, using the same classification labels, so the
// automated pipeline only needs to produce an email.InboundEvent.
package replies

import (
	"context"
	"time"
)

type InboundEmail struct {
	MessageID  string
	InReplyTo  string
	References []string
	From       string
	To         string
	Subject    string
	TextBody   string
	ReceivedAt time.Time
}

type Classification struct {
	Label      string  `json:"classification"` // one of email.ReplyClassifications
	Confidence float64 `json:"confidence"`
	Summary    string  `json:"summary"`
}

// Classifier labels a reply. The LLM-backed implementation lives behind
// integrations.LLMProvider; a rules-based one can handle out-of-office and
// unsubscribe keywords without an LLM.
type Classifier interface {
	Classify(ctx context.Context, email InboundEmail) (Classification, error)
}

// MailboxReader pulls new inbound messages (IMAP, Gmail API, provider inbound webhooks).
type MailboxReader interface {
	Fetch(ctx context.Context, since time.Time) ([]InboundEmail, error)
}
