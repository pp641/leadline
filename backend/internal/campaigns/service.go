package campaigns

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/internal/email"
	"leadline/internal/leads"
	"leadline/internal/scoring"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/validate"
)

const maxSteps = 10

type Service struct {
	pool  *pgxpool.Pool
	repo  *Repository
	leads *leads.Service
	unsub *email.Unsubscriber
	audit *audit.Log
}

func NewService(pool *pgxpool.Pool, l *leads.Service, u *email.Unsubscriber, a *audit.Log) *Service {
	return &Service{pool: pool, repo: NewRepository(pool), leads: l, unsub: u, audit: a}
}

func (s *Service) Get(ctx context.Context, p auth.Principal, id uuid.UUID) (*Campaign, error) {
	return s.repo.Get(ctx, p.OrgID, id)
}

func (s *Service) List(ctx context.Context, p auth.Principal, status string, limit, offset int) ([]Campaign, int64, error) {
	return s.repo.List(ctx, p.OrgID, status, limit, offset)
}

// normalize fills defaults and validates settings shared by create and update.
func (s *Service) normalize(ctx context.Context, orgID uuid.UUID, in *Input) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Timezone == "" {
		in.Timezone = "UTC"
	}
	if in.SendWindowStart == "" {
		in.SendWindowStart = "09:00"
	}
	if in.SendWindowEnd == "" {
		in.SendWindowEnd = "17:00"
	}
	if in.SendDays == nil {
		in.SendDays = []int{1, 2, 3, 4, 5}
	}
	if in.AllowedEmailStatuses == nil {
		in.AllowedEmailStatuses = []string{"valid"}
	}
	if in.DailyLimit == nil {
		d := 50
		in.DailyLimit = &d
	}
	if in.MaxBounceRate == nil {
		b := 0.05
		in.MaxBounceRate = &b
	}
	if in.StopOnReply == nil {
		t := true
		in.StopOnReply = &t
	}
	if in.AIPersonalization == nil {
		f := false
		in.AIPersonalization = &f
	}
	if in.RequireApproval == nil {
		t := true
		in.RequireApproval = &t
	}
	in.PersonalizationBrief = strings.TrimSpace(in.PersonalizationBrief)
	// Normalize the mailbox pool: MailboxID is the primary; MailboxIDs adds more.
	seen := map[uuid.UUID]bool{}
	var pool []uuid.UUID
	if in.MailboxID != nil {
		pool = append(pool, *in.MailboxID)
		seen[*in.MailboxID] = true
	}
	for _, id := range in.MailboxIDs {
		if !seen[id] {
			seen[id] = true
			pool = append(pool, id)
		}
	}
	in.MailboxIDs = pool
	if in.MailboxID == nil && len(pool) > 0 {
		in.MailboxID = &pool[0]
	}

	v := validate.Errors{}
	v.Required("name", in.Name)
	v.MaxLen("name", in.Name, 200)
	if _, err := NewWindow(in.Timezone, in.SendWindowStart, in.SendWindowEnd, in.SendDays); err != nil {
		v.Add("send_window", err.Error())
	}
	v.Check(len(in.SendDays) > 0, "send_days", "pick at least one day")
	for _, d := range in.SendDays {
		v.Check(d >= 1 && d <= 7, "send_days", "days are 1 (Monday) to 7 (Sunday)")
	}
	v.Check(len(in.AllowedEmailStatuses) > 0, "allowed_email_statuses", "pick at least one status")
	for _, st := range in.AllowedEmailStatuses {
		v.Check(slices.Contains(SendableEmailStatuses, st), "allowed_email_statuses", "invalid, disposable addresses can never be sent to")
	}
	v.Check(*in.DailyLimit >= 1 && *in.DailyLimit <= 5000, "daily_limit", "must be between 1 and 5000")
	v.Check(*in.MaxBounceRate > 0 && *in.MaxBounceRate <= 0.2, "max_bounce_rate", "must be between 0 and 0.2 (20%)")
	v.MaxLen("personalization_brief", in.PersonalizationBrief, 2000)
	v.Check(len(in.MailboxIDs) <= 20, "mailbox_ids", "at most 20 mailboxes per campaign")
	if len(in.MailboxIDs) > 0 {
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM mailboxes WHERE organization_id=$1 AND id = ANY($2)`, orgID, in.MailboxIDs).Scan(&n); err != nil {
			return err
		}
		v.Check(n == len(in.MailboxIDs), "mailbox_ids", "mailbox not found")
	}
	return v.Err()
}

func (s *Service) Create(ctx context.Context, p auth.Principal, in Input) (*Campaign, error) {
	if err := s.normalize(ctx, p.OrgID, &in); err != nil {
		return nil, err
	}
	var id uuid.UUID
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO campaigns (organization_id, name, mailbox_id, daily_limit, start_at, timezone, send_window_start,
				send_window_end, send_days, allowed_email_statuses, max_bounce_rate, stop_on_reply, created_by,
				ai_personalization, personalization_brief, require_personalization_approval)
			VALUES ($1,$2,$3,$4,$5,$6,$7::text::time,$8::text::time,$9::int[],$10,$11,$12,$13,$14,$15,$16) RETURNING id`,
			p.OrgID, in.Name, in.MailboxID, *in.DailyLimit, in.StartAt, in.Timezone, in.SendWindowStart, in.SendWindowEnd,
			in.SendDays, in.AllowedEmailStatuses, *in.MaxBounceRate, *in.StopOnReply, p.UserID,
			*in.AIPersonalization, in.PersonalizationBrief, *in.RequireApproval).Scan(&id); err != nil {
			return err
		}
		return setMailboxes(ctx, tx, p.OrgID, id, in.MailboxIDs)
	})
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "campaign.created", EntityType: "campaign", EntityID: id.String()})
	return s.repo.Get(ctx, p.OrgID, id)
}

func (s *Service) Update(ctx context.Context, p auth.Principal, id uuid.UUID, in Input) (*Campaign, error) {
	if err := s.normalize(ctx, p.OrgID, &in); err != nil {
		return nil, err
	}
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		c, err := GetForUpdate(ctx, tx, p.OrgID, id)
		if err != nil {
			return err
		}
		if c.Status == StatusCompleted || c.Status == StatusCancelled {
			return apperr.Conflict("campaign_closed", "a "+c.Status+" campaign cannot be edited")
		}
		if _, err = tx.Exec(ctx, `
			UPDATE campaigns SET name=$3, mailbox_id=$4, daily_limit=$5, start_at=$6, timezone=$7,
				send_window_start=$8::text::time, send_window_end=$9::text::time, send_days=$10::int[],
				allowed_email_statuses=$11, max_bounce_rate=$12, stop_on_reply=$13,
				ai_personalization=$14, personalization_brief=$15, require_personalization_approval=$16, updated_at=now()
			WHERE organization_id=$1 AND id=$2`,
			p.OrgID, id, in.Name, in.MailboxID, *in.DailyLimit, in.StartAt, in.Timezone, in.SendWindowStart,
			in.SendWindowEnd, in.SendDays, in.AllowedEmailStatuses, *in.MaxBounceRate, *in.StopOnReply,
			*in.AIPersonalization, in.PersonalizationBrief, *in.RequireApproval); err != nil {
			return err
		}
		return setMailboxes(ctx, tx, p.OrgID, id, in.MailboxIDs)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, p.OrgID, id)
}

func (s *Service) Delete(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM campaigns WHERE organization_id=$1 AND id=$2 AND status='draft'`, p.OrgID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.repo.Get(ctx, p.OrgID, id); err != nil {
			return err
		}
		return apperr.Conflict("not_draft", "only draft campaigns can be deleted; cancel it instead to keep its history")
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "campaign.deleted", EntityType: "campaign", EntityID: id.String()})
	return nil
}

// ReplaceSteps saves the sequence. Steps are matched by position so their IDs
// (and the history of messages sent for them) survive edits. A step that has
// already been sent cannot be removed.
func (s *Service) ReplaceSteps(ctx context.Context, p auth.Principal, id uuid.UUID, steps []StepInput) ([]Step, error) {
	v := validate.Errors{}
	v.Check(len(steps) >= 1, "steps", "add at least one email")
	v.Check(len(steps) <= maxSteps, "steps", fmt.Sprintf("at most %d steps", maxSteps))
	for i, st := range steps {
		key := fmt.Sprintf("steps[%d]", i)
		if i == 0 {
			v.Check(strings.TrimSpace(st.SubjectTemplate) != "", key+".subject_template", "the first email needs a subject")
		}
		v.Check(strings.TrimSpace(st.BodyTemplate) != "", key+".body_template", "body is required")
		v.Check(st.DelayDays >= 0 && st.DelayDays <= 90, key+".delay_days", "must be between 0 and 90")
		v.MaxLen(key+".subject_template", st.SubjectTemplate, 300)
		v.MaxLen(key+".body_template", st.BodyTemplate, 20000)
		if err := email.ValidateTemplate(st.SubjectTemplate); err != nil {
			v.Add(key+".subject_template", err.Error())
		}
		if err := email.ValidateTemplate(st.BodyTemplate); err != nil {
			v.Add(key+".body_template", err.Error())
		}
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		c, err := GetForUpdate(ctx, tx, p.OrgID, id)
		if err != nil {
			return err
		}
		if c.Status == StatusRunning {
			return apperr.Conflict("campaign_running", "pause the campaign before editing its sequence")
		}
		if c.Status == StatusCompleted || c.Status == StatusCancelled {
			return apperr.Conflict("campaign_closed", "a "+c.Status+" campaign cannot be edited")
		}
		for i, st := range steps {
			if _, err := tx.Exec(ctx, `
				INSERT INTO campaign_steps (organization_id, campaign_id, step_number, delay_days, subject_template, body_template)
				VALUES ($1,$2,$3,$4,$5,$6)
				ON CONFLICT (campaign_id, step_number) DO UPDATE SET delay_days=EXCLUDED.delay_days,
					subject_template=EXCLUDED.subject_template, body_template=EXCLUDED.body_template, updated_at=now()`,
				p.OrgID, id, i+1, st.DelayDays, strings.TrimSpace(st.SubjectTemplate), st.BodyTemplate); err != nil {
				return err
			}
		}
		var sent int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM email_messages m JOIN campaign_steps s ON s.id = m.step_id
			WHERE s.campaign_id=$1 AND s.step_number > $2 AND m.status <> 'cancelled'`, id, len(steps)).Scan(&sent); err != nil {
			return err
		}
		if sent > 0 {
			return apperr.Conflict("step_in_use", "steps that have already been sent cannot be removed")
		}
		_, err = tx.Exec(ctx, `DELETE FROM campaign_steps WHERE campaign_id=$1 AND step_number > $2`, id, len(steps))
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.repo.Steps(ctx, p.OrgID, id)
}

type AudienceInput struct {
	LeadIDs []uuid.UUID   `json:"lead_ids"`
	Filter  *leads.Filter `json:"filter"`
	// ICPID selects leads whose fit against that ICP is at least MinFit
	// (default 70), optionally narrowed further by Filter.
	ICPID  *uuid.UUID `json:"icp_id"`
	MinFit *int       `json:"min_fit"`
	// Contacts are typed or pasted people. Each email is matched to an
	// existing contact or lead, or becomes a new lead with that contact.
	Contacts []CustomContact `json:"contacts"`
	// DryRun reports what would happen without changing anything.
	DryRun bool `json:"dry_run"`
}

type CustomContact struct {
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Company   string `json:"company"`
}

type AudienceResult struct {
	Requested         int      `json:"requested"`
	Added             int      `json:"added"`
	Skipped           int      `json:"skipped"`
	AlreadyInCampaign int      `json:"already_in_campaign"`
	NoEmail           int      `json:"no_email"`
	Suppressed        int      `json:"suppressed"`
	Created           int      `json:"created"`        // new leads created from custom contacts
	InvalidEmails     []string `json:"invalid_emails"` // custom contacts that were not valid addresses
	DryRun            bool     `json:"dry_run"`
}

// personalDomains are consumer mailboxes: a pasted gmail address names a
// person, not a company.
var personalDomains = map[string]bool{
	"gmail.com": true, "googlemail.com": true, "yahoo.com": true, "yahoo.in": true, "outlook.com": true, "hotmail.com": true,
	"live.com": true, "icloud.com": true, "aol.com": true, "proton.me": true, "protonmail.com": true, "rediffmail.com": true, "zoho.com": true,
}

func titleWords(s string) string {
	words := strings.FieldsFunc(s, func(r rune) bool { return r == '.' || r == '-' || r == '_' || r == ' ' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// resolveContacts maps custom contacts to lead ids, creating leads for new
// addresses (unless dry-running).
func (s *Service) resolveContacts(ctx context.Context, p auth.Principal, list []CustomContact, dry bool, res *AudienceResult) ([]uuid.UUID, error) {
	if len(list) > 5000 {
		return nil, apperr.Invalid("validation failed", map[string]string{"contacts": "add at most 5,000 addresses at a time"})
	}
	var ids []uuid.UUID
	seen := map[string]bool{}
	for _, c := range list {
		addr := validate.NormalizeEmail(c.Email)
		if !validate.Email(addr) {
			if strings.TrimSpace(c.Email) != "" {
				res.InvalidEmails = append(res.InvalidEmails, strings.TrimSpace(c.Email))
			}
			continue
		}
		if seen[addr] {
			continue
		}
		seen[addr] = true
		var leadID uuid.UUID
		err := s.pool.QueryRow(ctx, `
			SELECT lead_id FROM lead_contacts WHERE organization_id=$1 AND email=$2
			UNION ALL SELECT id FROM leads WHERE organization_id=$1 AND business_email=$2 LIMIT 1`, p.OrgID, addr).Scan(&leadID)
		if err == nil {
			ids = append(ids, leadID)
			continue
		}
		if !database.IsNoRows(err) {
			return nil, err
		}
		res.Created++
		if dry {
			continue
		}
		local, domain, _ := strings.Cut(addr, "@")
		company, website := strings.TrimSpace(c.Company), ""
		if !personalDomains[domain] {
			website = domain
			if company == "" {
				company = titleWords(strings.Split(domain, ".")[0])
			}
		}
		if company == "" {
			company = strings.TrimSpace(c.FirstName + " " + c.LastName)
		}
		if company == "" {
			company = titleWords(local)
		}
		first := c.FirstName
		if first == "" && c.LastName == "" && !strings.ContainsAny(local, "0123456789") && !slices.Contains([]string{"info", "hello", "contact", "sales", "admin", "support", "office"}, local) {
			first = titleWords(strings.Split(local, ".")[0])
		}
		l, err := s.leads.Create(ctx, p, leads.Input{CompanyName: company, Website: website, Source: "manual", Tags: []string{"added-to-campaign"},
			Contact: &leads.ContactInput{FirstName: first, LastName: c.LastName, Email: addr}})
		if err != nil {
			return nil, err
		}
		ids = append(ids, l.ID)
	}
	return ids, nil
}

// AddLeads adds leads to a campaign from any mix of picked ids, a filter, an
// ICP and custom contacts. Leads without a usable email, suppressed addresses
// and leads already in the campaign are skipped.
func (s *Service) AddLeads(ctx context.Context, p auth.Principal, id uuid.UUID, in AudienceInput) (*AudienceResult, error) {
	res := &AudienceResult{DryRun: in.DryRun, InvalidEmails: []string{}}
	if _, err := s.repo.Get(ctx, p.OrgID, id); err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	if len(in.LeadIDs) > 0 || (in.Filter != nil && in.ICPID == nil) {
		got, err := s.leads.ResolveIDs(ctx, p, in.LeadIDs, in.Filter)
		if err != nil {
			return nil, err
		}
		ids = append(ids, got...)
	}
	if in.ICPID != nil {
		f := leads.Filter{}
		if in.Filter != nil {
			f = *in.Filter
		}
		candidates, err := s.leads.ResolveIDs(ctx, p, nil, &f)
		if err != nil {
			return nil, err
		}
		minFit := 70
		if in.MinFit != nil {
			minFit = max(0, min(100, *in.MinFit))
		}
		matched, err := scoring.FilterByICP(ctx, s.pool, p.OrgID, *in.ICPID, minFit, candidates)
		if err != nil {
			return nil, err
		}
		ids = append(ids, matched...)
	}
	if len(in.Contacts) > 0 {
		got, err := s.resolveContacts(ctx, p, in.Contacts, in.DryRun, res)
		if err != nil {
			return nil, err
		}
		ids = append(ids, got...)
	}
	if len(ids) == 0 && res.Created == 0 && len(in.LeadIDs) == 0 && in.Filter == nil && in.ICPID == nil && len(in.Contacts) == 0 {
		return nil, apperr.Invalid("validation failed", map[string]string{"lead_ids": "pick leads, a filter, an ICP or add email addresses"})
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	ids = slices.Compact(ids)
	res.Requested = len(ids) + map[bool]int{true: res.Created, false: 0}[in.DryRun]

	// Classify every candidate the same way the insert below decides.
	err := s.pool.QueryRow(ctx, `
		WITH cand AS (
			SELECT l.id, COALESCE(pc.email, l.business_email) AS email
			FROM leads l
			LEFT JOIN LATERAL (
				SELECT c.email FROM lead_contacts c
				WHERE c.lead_id = l.id AND c.email IS NOT NULL AND c.email_status NOT IN ('invalid','disposable')
				ORDER BY c.is_primary DESC, c.created_at LIMIT 1
			) pc ON true
			WHERE l.organization_id = $1 AND l.id = ANY($3))
		SELECT
			count(*) FILTER (WHERE EXISTS (SELECT 1 FROM campaign_leads cl WHERE cl.campaign_id=$2 AND cl.lead_id=cand.id)),
			count(*) FILTER (WHERE email IS NULL AND NOT EXISTS (SELECT 1 FROM campaign_leads cl WHERE cl.campaign_id=$2 AND cl.lead_id=cand.id)),
			count(*) FILTER (WHERE email IS NOT NULL AND NOT EXISTS (SELECT 1 FROM campaign_leads cl WHERE cl.campaign_id=$2 AND cl.lead_id=cand.id)
				AND EXISTS (SELECT 1 FROM suppression_list sl WHERE sl.organization_id=$1 AND sl.email=cand.email))
		FROM cand`, p.OrgID, id, ids).Scan(&res.AlreadyInCampaign, &res.NoEmail, &res.Suppressed)
	if err != nil {
		return nil, err
	}
	if in.DryRun {
		// New contacts would all be addable (they have an email and are new).
		res.Added = len(ids) - res.AlreadyInCampaign - res.NoEmail - res.Suppressed + res.Created
		res.Skipped = res.Requested - res.Added
		return res, nil
	}

	err = database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		c, err := GetForUpdate(ctx, tx, p.OrgID, id)
		if err != nil {
			return err
		}
		if c.Status == StatusCompleted || c.Status == StatusCancelled {
			return apperr.Conflict("campaign_closed", "cannot add leads to a "+c.Status+" campaign")
		}
		// Leads joining a launched campaign start immediately; draft ones wait for launch.
		status, nextSend := "pending", (*time.Time)(nil)
		if c.Status == StatusRunning || c.Status == StatusPaused || c.Status == StatusScheduled {
			now := time.Now()
			if c.StartAt != nil && c.StartAt.After(now) {
				now = *c.StartAt
			}
			status, nextSend = "active", &now
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO campaign_leads (organization_id, campaign_id, lead_id, contact_id, email, status, next_send_at)
			SELECT $1, $2, l.id, pc.id, COALESCE(pc.email, l.business_email), $4, $5
			FROM leads l
			LEFT JOIN LATERAL (
				SELECT c.id, c.email FROM lead_contacts c
				WHERE c.lead_id = l.id AND c.email IS NOT NULL AND c.email_status NOT IN ('invalid','disposable')
				ORDER BY CASE c.email_status WHEN 'valid' THEN 0 WHEN 'role_based' THEN 1 WHEN 'risky' THEN 2
				         WHEN 'unknown' THEN 3 ELSE 4 END, c.is_primary DESC, c.created_at
				LIMIT 1
			) pc ON true
			WHERE l.organization_id = $1 AND l.id = ANY($3)
			  AND COALESCE(pc.email, l.business_email) IS NOT NULL
			  AND NOT EXISTS (SELECT 1 FROM suppression_list sl WHERE sl.organization_id = $1
			                  AND sl.email = COALESCE(pc.email, l.business_email))
			ON CONFLICT (campaign_id, lead_id) DO NOTHING`,
			p.OrgID, id, ids, status, nextSend)
		if err != nil {
			return err
		}
		res.Added = int(tag.RowsAffected())
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.Skipped = res.Requested - res.Added
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "campaign.leads_added", EntityType: "campaign", EntityID: id.String(),
		Metadata: map[string]any{"added": res.Added, "created": res.Created}})
	return res, nil
}

// RemoveLeads removes leads that have not been emailed yet and stops the rest.
func (s *Service) RemoveLeads(ctx context.Context, p auth.Principal, id uuid.UUID, leadIDs []uuid.UUID) (int, error) {
	var n int64
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := GetForUpdate(ctx, tx, p.OrgID, id); err != nil {
			return err
		}
		del, err := tx.Exec(ctx, `
			DELETE FROM campaign_leads cl WHERE cl.organization_id=$1 AND cl.campaign_id=$2 AND cl.lead_id = ANY($3)
			AND NOT EXISTS (SELECT 1 FROM email_messages m WHERE m.campaign_lead_id = cl.id)`, p.OrgID, id, leadIDs)
		if err != nil {
			return err
		}
		upd, err := tx.Exec(ctx, `
			UPDATE campaign_leads SET status='stopped', stop_reason='removed', next_send_at=NULL, updated_at=now()
			WHERE organization_id=$1 AND campaign_id=$2 AND lead_id = ANY($3) AND status IN ('pending','active')`, p.OrgID, id, leadIDs)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE email_messages SET status='cancelled', last_error='lead removed from campaign', updated_at=now()
			WHERE organization_id=$1 AND campaign_id=$2 AND lead_id = ANY($3) AND status='queued'`, p.OrgID, id, leadIDs); err != nil {
			return err
		}
		n = del.RowsAffected() + upd.RowsAffected()
		return nil
	})
	return int(n), err
}

func (s *Service) Leads(ctx context.Context, p auth.Principal, id uuid.UUID, status, q string, limit, offset int) ([]CampaignLead, int64, error) {
	if _, err := s.repo.Get(ctx, p.OrgID, id); err != nil {
		return nil, 0, err
	}
	return s.repo.Leads(ctx, p.OrgID, id, status, strings.TrimSpace(q), limit, offset)
}

// Launch validates the campaign and starts it (or schedules it for start_at).
func (s *Service) Launch(ctx context.Context, p auth.Principal, id uuid.UUID) (*Campaign, error) {
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		c, err := GetForUpdate(ctx, tx, p.OrgID, id)
		if err != nil {
			return err
		}
		if c.Status != StatusDraft {
			return apperr.Conflict("not_draft", "only draft campaigns can be launched")
		}
		problems := map[string]string{}
		var steps, audience int
		var mailboxOK bool
		var address string
		if err := tx.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM campaign_steps WHERE campaign_id=$2),
			       (SELECT count(*) FROM campaign_leads WHERE campaign_id=$2),
			       EXISTS (SELECT 1 FROM mailboxes m WHERE m.organization_id=$1 AND m.status='active' AND (m.id=$3 OR
			               m.id IN (SELECT mailbox_id FROM campaign_mailboxes WHERE campaign_id=$2))),
			       (SELECT physical_address FROM organizations WHERE id=$1)`,
			p.OrgID, id, c.MailboxID).Scan(&steps, &audience, &mailboxOK, &address); err != nil {
			return err
		}
		if steps == 0 {
			problems["steps"] = "add at least one email to the sequence"
		}
		if audience == 0 {
			problems["audience"] = "add leads to the campaign"
		}
		if !mailboxOK {
			problems["mailbox_id"] = "select an active sending mailbox"
		}
		if strings.TrimSpace(address) == "" {
			problems["physical_address"] = "set your organization's postal address in Settings (required in every email footer)"
		}
		if len(problems) > 0 {
			return apperr.Invalid("campaign is not ready to launch", problems)
		}

		now := time.Now()
		start := now
		status := StatusRunning
		if c.StartAt != nil && c.StartAt.After(now) {
			start, status = *c.StartAt, StatusScheduled
		}
		if _, err := tx.Exec(ctx, `
			UPDATE campaign_leads cl SET status='active', next_step_number=1,
				next_send_at = $3::timestamptz + make_interval(days => s.delay_days), updated_at=now()
			FROM campaign_steps s
			WHERE cl.organization_id=$1 AND cl.campaign_id=$2 AND cl.status='pending'
			  AND s.campaign_id = cl.campaign_id AND s.step_number = 1`, p.OrgID, id, start); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE campaigns SET status=$3, launched_at=now(), paused_reason=NULL, updated_at=now()
			WHERE organization_id=$1 AND id=$2`, p.OrgID, id, status)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "campaign.launched", EntityType: "campaign", EntityID: id.String()})
	return s.repo.Get(ctx, p.OrgID, id)
}

func (s *Service) Pause(ctx context.Context, p auth.Principal, id uuid.UUID) (*Campaign, error) {
	return s.transition(ctx, p, id, []string{StatusRunning, StatusScheduled}, StatusPaused, "campaign.paused", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE campaigns SET paused_reason='paused by user' WHERE id=$1`, id)
		return err
	})
}

func (s *Service) Resume(ctx context.Context, p auth.Principal, id uuid.UUID) (*Campaign, error) {
	return s.transition(ctx, p, id, []string{StatusPaused}, StatusRunning, "campaign.resumed", func(tx pgx.Tx) error {
		var c Campaign
		if err := tx.QueryRow(ctx, `SELECT max_bounce_rate::float8 FROM campaigns WHERE id=$1`, id).Scan(&c.MaxBounceRate); err != nil {
			return err
		}
		sent, bounced, err := bounceStats(ctx, tx, id)
		if err != nil {
			return err
		}
		if exceedsBounceLimit(sent, bounced, c.MaxBounceRate) {
			return apperr.Conflict("bounce_rate_exceeded", fmt.Sprintf(
				"bounce rate is %.1f%%, above this campaign's %.1f%% limit; clean the list or raise the limit before resuming",
				100*float64(bounced)/float64(sent), 100*c.MaxBounceRate))
		}
		_, err = tx.Exec(ctx, `UPDATE campaigns SET paused_reason=NULL WHERE id=$1`, id)
		return err
	})
}

func (s *Service) Cancel(ctx context.Context, p auth.Principal, id uuid.UUID) (*Campaign, error) {
	return s.transition(ctx, p, id, []string{StatusDraft, StatusScheduled, StatusRunning, StatusPaused}, StatusCancelled, "campaign.cancelled", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE campaign_leads SET status='stopped', stop_reason='campaign cancelled', next_send_at=NULL, updated_at=now()
			WHERE campaign_id=$1 AND status IN ('pending','active')`, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE email_messages SET status='cancelled', last_error='campaign cancelled', updated_at=now()
			WHERE campaign_id=$1 AND status='queued'`, id)
		return err
	})
}

func (s *Service) transition(ctx context.Context, p auth.Principal, id uuid.UUID, from []string, to, action string, extra func(pgx.Tx) error) (*Campaign, error) {
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		c, err := GetForUpdate(ctx, tx, p.OrgID, id)
		if err != nil {
			return err
		}
		if !slices.Contains(from, c.Status) {
			return apperr.Conflict("invalid_transition", fmt.Sprintf("cannot change a %s campaign to %s", c.Status, to))
		}
		if extra != nil {
			if err := extra(tx); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE campaigns SET status=$2, updated_at=now(),
			completed_at = CASE WHEN $2 IN ('completed','cancelled') THEN now() ELSE completed_at END WHERE id=$1`, id, to)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: action, EntityType: "campaign", EntityID: id.String()})
	return s.repo.Get(ctx, p.OrgID, id)
}

type PreviewInput struct {
	StepNumber int        `json:"step_number"`
	LeadID     *uuid.UUID `json:"lead_id"`
	// Optional unsaved templates, so the editor can preview before saving.
	SubjectTemplate *string `json:"subject_template"`
	BodyTemplate    *string `json:"body_template"`
}

type PreviewResult struct {
	Rendered
	To          string    `json:"to"`
	From        string    `json:"from"`
	LeadID      uuid.UUID `json:"lead_id"`
	CompanyName string    `json:"company_name"`
}

// Preview renders a step for one lead (the given one, or the first in the audience).
func (s *Service) Preview(ctx context.Context, p auth.Principal, id uuid.UUID, in PreviewInput) (*PreviewResult, error) {
	c, err := s.repo.Get(ctx, p.OrgID, id)
	if err != nil {
		return nil, err
	}
	if in.StepNumber < 1 {
		in.StepNumber = 1
	}
	step := Step{StepNumber: in.StepNumber}
	prevSubject := ""
	for _, st := range c.Steps {
		if st.StepNumber == in.StepNumber {
			step = st
		}
		if st.StepNumber < in.StepNumber && strings.TrimSpace(st.SubjectTemplate) != "" {
			prevSubject = st.SubjectTemplate
		}
	}
	if in.SubjectTemplate != nil {
		step.SubjectTemplate = *in.SubjectTemplate
	}
	if in.BodyTemplate != nil {
		step.BodyTemplate = *in.BodyTemplate
	}

	q := `SELECT l.id, COALESCE(ct.first_name,''), COALESCE(ct.last_name,''), COALESCE(ct.job_title,''), l.company_name,
			l.website, l.industry, l.city, l.state, l.country, COALESCE(cl.email::text, ct.email::text, l.business_email::text, ''),
			COALESCE((SELECT lp.content FROM lead_personalizations lp WHERE lp.campaign_id=$2 AND lp.lead_id=l.id
			          AND lp.status IN ('approved','generated')), '')
		FROM leads l
		LEFT JOIN campaign_leads cl ON cl.lead_id = l.id AND cl.campaign_id = $2
		LEFT JOIN lead_contacts ct ON ct.id = COALESCE(cl.contact_id,
			(SELECT c2.id FROM lead_contacts c2 WHERE c2.lead_id = l.id ORDER BY c2.is_primary DESC, c2.created_at LIMIT 1))
		WHERE l.organization_id = $1 AND `
	args := []any{p.OrgID, id}
	if in.LeadID != nil {
		q += `l.id = $3`
		args = append(args, *in.LeadID)
	} else {
		q += `cl.id IS NOT NULL ORDER BY cl.created_at LIMIT 1`
	}
	var r Recipient
	var res PreviewResult
	err = s.pool.QueryRow(ctx, q, args...).Scan(&res.LeadID, &r.FirstName, &r.LastName, &r.JobTitle, &r.CompanyName,
		&r.Website, &r.Industry, &r.City, &r.State, &r.Country, &res.To, &r.PersonalizedLine)
	if database.IsNoRows(err) {
		// No audience yet: preview with sample data.
		r = Recipient{FirstName: "Alex", LastName: "Sample", CompanyName: "Sample Co", City: "Delhi", Industry: "restaurant", Website: "https://example.com",
			PersonalizedLine: "(AI opener for each lead appears here)"}
		res.To = "alex@example.com"
	} else if err != nil {
		return nil, err
	}
	res.CompanyName = r.CompanyName

	sender, err := s.sender(ctx, p.OrgID, c.MailboxID)
	if err != nil {
		return nil, err
	}
	res.From = sender.FromName + " <" + sender.FromEmail + ">"
	res.Rendered = RenderStep(step, prevSubject, r, sender, s.unsub.URL(email.UnsubscribeClaims{OrgID: p.OrgID, CampaignID: id, Email: res.To}))
	return &res, nil
}

func (s *Service) sender(ctx context.Context, orgID uuid.UUID, mailboxID *uuid.UUID) (Sender, error) {
	var snd Sender
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT from_name FROM mailboxes WHERE id=$2 AND organization_id=$1), 'Your Name'),
		       COALESCE((SELECT from_email::text FROM mailboxes WHERE id=$2 AND organization_id=$1), 'you@example.com'),
		       COALESCE(NULLIF(o.legal_name,''), o.name), o.physical_address
		FROM organizations o WHERE o.id=$1`, orgID, mailboxID).Scan(&snd.FromName, &snd.FromEmail, &snd.SenderCompany, &snd.PhysicalAddress)
	return snd, err
}

func bounceStats(ctx context.Context, db database.DBTX, campaignID uuid.UUID) (sent, bounced int64, err error) {
	err = db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM email_messages WHERE campaign_id=$1 AND status='sent'),
		       (SELECT count(DISTINCT email_message_id) FROM email_events WHERE campaign_id=$1 AND event_type='bounced'
		          AND COALESCE(metadata->>'bounce_type','hard') NOT IN ('soft','transient'))`, campaignID).Scan(&sent, &bounced)
	return
}

// minSampleForBounceCheck avoids pausing on tiny samples (1 bounce in 3 sends).
const minSampleForBounceCheck = 20

func exceedsBounceLimit(sent, bounced int64, limit float64) bool {
	return sent >= minSampleForBounceCheck && float64(bounced)/float64(sent) > limit
}

// setMailboxes replaces a campaign's mailbox pool.
func setMailboxes(ctx context.Context, tx pgx.Tx, orgID, campaignID uuid.UUID, ids []uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM campaign_mailboxes WHERE campaign_id=$1 AND NOT (mailbox_id = ANY($2))`, campaignID, nonNilIDs(ids)); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO campaign_mailboxes (organization_id, campaign_id, mailbox_id)
		SELECT $1::uuid, $2::uuid, unnest($3::uuid[]) ON CONFLICT DO NOTHING`, orgID, campaignID, nonNilIDs(ids))
	return err
}

func nonNilIDs(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}
