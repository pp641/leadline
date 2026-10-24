// Package jobs is the contract between job producers (API services) and
// consumers (workers): queue names, job types and payloads. Keeping it
// separate avoids import cycles between domain packages.
package jobs

import (
	"context"
	"time"

	"github.com/google/uuid"

	"leadline/pkg/queue"
)

const (
	QueueDefault = "default"
	QueueEmail   = "email"
)

const (
	TypeProcessImport  = "import.process"
	TypeVerifyContacts = "verification.contacts"
	TypeScoreLeads     = "scoring.leads"
	TypeRescoreOrg     = "scoring.rescore_org"
	TypeSendEmail      = "email.send"
	TypePersonalize    = "ai.personalize"
	TypeInboxSync      = "inbox.sync"
	TypeCRMSync        = "crm.sync"
	TypeWorkflowEvent  = "workflow.event"
)

type Personalize struct {
	OrgID      uuid.UUID   `json:"org_id"`
	CampaignID uuid.UUID   `json:"campaign_id"`
	LeadIDs    []uuid.UUID `json:"lead_ids"`
}

type InboxSync struct {
	MailboxID uuid.UUID `json:"mailbox_id"`
}

type CRMSync struct {
	OrgID   uuid.UUID   `json:"org_id"`
	LeadIDs []uuid.UUID `json:"lead_ids"`
	// Provider limits the sync to one CRM integration kind; empty syncs to
	// every connected CRM.
	Provider string `json:"provider,omitempty"`
}

// WorkflowEvent runs the organization's enabled workflows for one event.
// Chain holds the workflows that led to this event, so a workflow can't
// re-trigger itself; Depth caps how far automations may cascade.
type WorkflowEvent struct {
	OrgID    uuid.UUID         `json:"org_id"`
	Trigger  string            `json:"trigger"`
	EventKey string            `json:"event_key"`
	LeadID   *uuid.UUID        `json:"lead_id,omitempty"`
	DealID   *uuid.UUID        `json:"deal_id,omitempty"`
	Extra    map[string]string `json:"extra,omitempty"`
	Chain    []uuid.UUID       `json:"chain,omitempty"`
	Depth    int               `json:"depth,omitempty"`
}

type ProcessImport struct {
	OrgID    uuid.UUID `json:"org_id"`
	ImportID uuid.UUID `json:"import_id"`
}

type VerifyContacts struct {
	OrgID      uuid.UUID   `json:"org_id"`
	ContactIDs []uuid.UUID `json:"contact_ids"`
}

type ScoreLeads struct {
	OrgID   uuid.UUID   `json:"org_id"`
	LeadIDs []uuid.UUID `json:"lead_ids"`
}

type RescoreOrg struct {
	OrgID uuid.UUID `json:"org_id"`
}

type SendEmail struct {
	MessageID uuid.UUID `json:"message_id"`
}

// Enqueuer is what services depend on, so tests can substitute a fake.
type Enqueuer interface {
	Enqueue(ctx context.Context, queueName, jobType string, payload any, opts ...queue.EnqueueOpts) error
}

// Chunk splits ids into slices of at most n, for fan-out jobs.
func Chunk(ids []uuid.UUID, n int) [][]uuid.UUID {
	var out [][]uuid.UUID
	for len(ids) > 0 {
		k := min(n, len(ids))
		out = append(out, ids[:k])
		ids = ids[k:]
	}
	return out
}

// EnqueueVerify fans contact verification out in chunks.
func EnqueueVerify(ctx context.Context, q Enqueuer, orgID uuid.UUID, ids []uuid.UUID) error {
	for _, c := range Chunk(ids, 200) {
		if err := q.Enqueue(ctx, QueueDefault, TypeVerifyContacts, VerifyContacts{OrgID: orgID, ContactIDs: c}); err != nil {
			return err
		}
	}
	return nil
}

// EnqueueScore fans lead scoring out in chunks.
func EnqueueScore(ctx context.Context, q Enqueuer, orgID uuid.UUID, ids []uuid.UUID) error {
	for _, c := range Chunk(ids, 500) {
		if err := q.Enqueue(ctx, QueueDefault, TypeScoreLeads, ScoreLeads{OrgID: orgID, LeadIDs: c}); err != nil {
			return err
		}
	}
	return nil
}

// SendEmailOpts: retries are governed by the email worker itself.
var SendEmailOpts = queue.EnqueueOpts{MaxAttempts: 6}

// SweepAge is how long a queued message may sit before the recovery sweep
// re-enqueues it (covers jobs lost in Redis).
const SweepAge = 10 * time.Minute
