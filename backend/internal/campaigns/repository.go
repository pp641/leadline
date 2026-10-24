package campaigns

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"leadline/pkg/apperr"
	"leadline/pkg/database"
)

const campaignCols = `c.id, c.name, c.status, c.mailbox_id, c.daily_limit, c.start_at, c.timezone,
	to_char(c.send_window_start, 'HH24:MI'), to_char(c.send_window_end, 'HH24:MI'), c.send_days,
	c.allowed_email_statuses, c.max_bounce_rate::float8, c.stop_on_reply, c.paused_reason, c.launched_at,
	c.completed_at, c.created_at, c.updated_at,
	COALESCE((SELECT array_agg(cm.mailbox_id ORDER BY cm.mailbox_id) FROM campaign_mailboxes cm WHERE cm.campaign_id = c.id), '{}'),
	c.ai_personalization, c.personalization_brief, c.require_personalization_approval`

const summaryCols = `,
	(SELECT count(*) FROM campaign_leads cl WHERE cl.campaign_id = c.id),
	(SELECT count(*) FROM campaign_leads cl WHERE cl.campaign_id = c.id AND cl.status IN ('pending','active','sending')),
	(SELECT count(*) FROM email_messages m WHERE m.campaign_id = c.id AND m.status = 'sent'),
	(SELECT count(DISTINCT e.lead_id) FROM email_events e WHERE e.campaign_id = c.id AND e.event_type = 'replied'),
	(SELECT count(DISTINCT e.email_message_id) FROM email_events e WHERE e.campaign_id = c.id AND e.event_type = 'bounced')`

func scanCampaign(row pgx.Row, withSummary bool) (*Campaign, error) {
	var c Campaign
	var days []int32
	dest := []any{&c.ID, &c.Name, &c.Status, &c.MailboxID, &c.DailyLimit, &c.StartAt, &c.Timezone,
		&c.SendWindowStart, &c.SendWindowEnd, &days, &c.AllowedEmailStatuses, &c.MaxBounceRate, &c.StopOnReply,
		&c.PausedReason, &c.LaunchedAt, &c.CompletedAt, &c.CreatedAt, &c.UpdatedAt,
		&c.MailboxIDs, &c.AIPersonalization, &c.PersonalizationBrief, &c.RequireApproval}
	var s Summary
	if withSummary {
		dest = append(dest, &s.Leads, &s.Active, &s.Sent, &s.Replied, &s.Bounced)
	}
	if err := row.Scan(dest...); err != nil {
		if database.IsNoRows(err) {
			return nil, apperr.NotFound("campaign")
		}
		return nil, err
	}
	c.SendDays = make([]int, len(days))
	for i, d := range days {
		c.SendDays[i] = int(d)
	}
	if withSummary {
		c.Summary = &s
	}
	return &c, nil
}

type Repository struct{ db database.DBTX }

func NewRepository(db database.DBTX) *Repository { return &Repository{db: db} }

func (r *Repository) Get(ctx context.Context, orgID, id uuid.UUID) (*Campaign, error) {
	c, err := scanCampaign(r.db.QueryRow(ctx, `SELECT `+campaignCols+summaryCols+` FROM campaigns c WHERE c.organization_id=$1 AND c.id=$2`, orgID, id), true)
	if err != nil {
		return nil, err
	}
	c.Steps, err = r.Steps(ctx, orgID, id)
	return c, err
}

// GetForUpdate locks the campaign row inside a transaction.
func GetForUpdate(ctx context.Context, tx pgx.Tx, orgID, id uuid.UUID) (*Campaign, error) {
	return scanCampaign(tx.QueryRow(ctx, `SELECT `+campaignCols+` FROM campaigns c WHERE c.organization_id=$1 AND c.id=$2 FOR UPDATE`, orgID, id), false)
}

func (r *Repository) List(ctx context.Context, orgID uuid.UUID, status string, limit, offset int) ([]Campaign, int64, error) {
	var total int64
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM campaigns c WHERE c.organization_id=$1 AND ($2 = '' OR c.status = $2)`, orgID, status).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.Query(ctx, `SELECT `+campaignCols+summaryCols+` FROM campaigns c
		WHERE c.organization_id=$1 AND ($2 = '' OR c.status = $2) ORDER BY c.created_at DESC LIMIT $3 OFFSET $4`, orgID, status, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Campaign{}
	for rows.Next() {
		c, err := scanCampaign(rows, true)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *c)
	}
	return out, total, rows.Err()
}

func (r *Repository) Steps(ctx context.Context, orgID, campaignID uuid.UUID) ([]Step, error) {
	rows, err := r.db.Query(ctx, `SELECT id, step_number, delay_days, subject_template, body_template
		FROM campaign_steps WHERE organization_id=$1 AND campaign_id=$2 ORDER BY step_number`, orgID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Step{}
	for rows.Next() {
		var s Step
		if err := rows.Scan(&s.ID, &s.StepNumber, &s.DelayDays, &s.SubjectTemplate, &s.BodyTemplate); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repository) Leads(ctx context.Context, orgID, campaignID uuid.UUID, status, q string, limit, offset int) ([]CampaignLead, int64, error) {
	where := `cl.organization_id=$1 AND cl.campaign_id=$2 AND ($3 = '' OR cl.status = $3)
		AND ($4 = '' OR l.company_name ILIKE '%' || $4 || '%' OR cl.email::text ILIKE '%' || $4 || '%')`
	var total int64
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM campaign_leads cl JOIN leads l ON l.id = cl.lead_id WHERE `+where,
		orgID, campaignID, status, q).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.Query(ctx, `
		SELECT cl.id, cl.lead_id, cl.contact_id, l.company_name,
		       trim(COALESCE(ct.first_name, '') || ' ' || COALESCE(ct.last_name, '')),
		       cl.email::text, COALESCE(ct.email_status, 'unverified'), cl.status, cl.next_step_number,
		       cl.next_send_at, cl.last_sent_at, cl.stop_reason, cl.created_at
		FROM campaign_leads cl
		JOIN leads l ON l.id = cl.lead_id
		LEFT JOIN lead_contacts ct ON ct.id = cl.contact_id
		WHERE `+where+` ORDER BY cl.created_at, cl.id LIMIT $5 OFFSET $6`,
		orgID, campaignID, status, q, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []CampaignLead{}
	for rows.Next() {
		var c CampaignLead
		if err := rows.Scan(&c.ID, &c.LeadID, &c.ContactID, &c.CompanyName, &c.ContactName, &c.Email, &c.EmailStatus,
			&c.Status, &c.NextStepNumber, &c.NextSendAt, &c.LastSentAt, &c.StopReason, &c.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}
