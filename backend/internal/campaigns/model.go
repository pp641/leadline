// Package campaigns implements campaign management, email sequences,
// audiences, the scheduler that turns due sequence steps into queued
// messages, and the worker that sends them.
package campaigns

import (
	"time"

	"github.com/google/uuid"
)

const (
	StatusDraft     = "draft"
	StatusScheduled = "scheduled"
	StatusRunning   = "running"
	StatusPaused    = "paused"
	StatusCompleted = "completed"
	StatusCancelled = "cancelled"
)

// Statuses a contact's email may have to be eligible for sending.
var SendableEmailStatuses = []string{"valid", "role_based", "risky", "unknown", "unverified"}

type Campaign struct {
	ID                   uuid.UUID   `json:"id"`
	Name                 string      `json:"name"`
	Status               string      `json:"status"`
	MailboxID            *uuid.UUID  `json:"mailbox_id"`
	DailyLimit           int         `json:"daily_limit"`
	StartAt              *time.Time  `json:"start_at"`
	Timezone             string      `json:"timezone"`
	SendWindowStart      string      `json:"send_window_start"`
	SendWindowEnd        string      `json:"send_window_end"`
	SendDays             []int       `json:"send_days"`
	AllowedEmailStatuses []string    `json:"allowed_email_statuses"`
	MaxBounceRate        float64     `json:"max_bounce_rate"`
	StopOnReply          bool        `json:"stop_on_reply"`
	MailboxIDs           []uuid.UUID `json:"mailbox_ids"`
	AIPersonalization    bool        `json:"ai_personalization"`
	PersonalizationBrief string      `json:"personalization_brief"`
	RequireApproval      bool        `json:"require_personalization_approval"`
	PausedReason         *string     `json:"paused_reason"`
	LaunchedAt           *time.Time  `json:"launched_at"`
	CompletedAt          *time.Time  `json:"completed_at"`
	CreatedAt            time.Time   `json:"created_at"`
	UpdatedAt            time.Time   `json:"updated_at"`
	Summary              *Summary    `json:"summary,omitempty"`
	Steps                []Step      `json:"steps,omitempty"`
}

// Summary is a light per-campaign rollup for list views.
type Summary struct {
	Leads   int64 `json:"leads"`
	Active  int64 `json:"active"`
	Sent    int64 `json:"sent"`
	Replied int64 `json:"replied"`
	Bounced int64 `json:"bounced"`
}

type Step struct {
	ID              uuid.UUID `json:"id"`
	StepNumber      int       `json:"step_number"`
	DelayDays       int       `json:"delay_days"`
	SubjectTemplate string    `json:"subject_template"`
	BodyTemplate    string    `json:"body_template"`
}

type Input struct {
	Name                 string     `json:"name"`
	MailboxID            *uuid.UUID `json:"mailbox_id"`
	DailyLimit           *int       `json:"daily_limit"`
	StartAt              *time.Time `json:"start_at"`
	Timezone             string     `json:"timezone"`
	SendWindowStart      string     `json:"send_window_start"`
	SendWindowEnd        string     `json:"send_window_end"`
	SendDays             []int      `json:"send_days"`
	AllowedEmailStatuses []string   `json:"allowed_email_statuses"`
	MaxBounceRate        *float64   `json:"max_bounce_rate"`
	StopOnReply          *bool      `json:"stop_on_reply"`
	// MailboxIDs rotates sending across several mailboxes (MailboxID is
	// treated as one more member of the pool, for backwards compatibility).
	MailboxIDs           []uuid.UUID `json:"mailbox_ids"`
	AIPersonalization    *bool       `json:"ai_personalization"`
	PersonalizationBrief string      `json:"personalization_brief"`
	RequireApproval      *bool       `json:"require_personalization_approval"`
}

type StepInput struct {
	DelayDays       int    `json:"delay_days"`
	SubjectTemplate string `json:"subject_template"`
	BodyTemplate    string `json:"body_template"`
}

type CampaignLead struct {
	ID             uuid.UUID  `json:"id"`
	LeadID         uuid.UUID  `json:"lead_id"`
	ContactID      *uuid.UUID `json:"contact_id"`
	CompanyName    string     `json:"company_name"`
	ContactName    string     `json:"contact_name"`
	Email          string     `json:"email"`
	EmailStatus    string     `json:"email_status"`
	Status         string     `json:"status"`
	NextStepNumber int        `json:"next_step_number"`
	NextSendAt     *time.Time `json:"next_send_at"`
	LastSentAt     *time.Time `json:"last_sent_at"`
	StopReason     *string    `json:"stop_reason"`
	CreatedAt      time.Time  `json:"created_at"`
}
