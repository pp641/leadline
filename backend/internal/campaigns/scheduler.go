package campaigns

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"leadline/internal/audit"
	"leadline/internal/email"
	"leadline/internal/jobs"
	"leadline/internal/webhooks"
	"leadline/pkg/database"
	"leadline/pkg/redisx"
)

// Scheduler periodically turns due sequence steps into queued email_messages
// and enqueues send jobs. It enforces sending windows, pacing, and the
// campaign / mailbox / organization daily limits, and auto-pauses campaigns
// whose bounce rate is too high.
//
// Only one scheduler runs at a time across all worker replicas (Redis lock);
// the per-tick work is small, and serializing it keeps limit accounting exact.
type Scheduler struct {
	pool     *pgxpool.Pool
	rdb      *redis.Client
	q        jobs.Enqueuer
	unsub    *email.Unsubscriber
	audit    *audit.Log
	log      *slog.Logger
	interval time.Duration
	now      func() time.Time
}

func NewScheduler(pool *pgxpool.Pool, rdb *redis.Client, q jobs.Enqueuer, u *email.Unsubscriber, a *audit.Log, log *slog.Logger, interval time.Duration) *Scheduler {
	return &Scheduler{pool: pool, rdb: rdb, q: q, unsub: u, audit: a, log: log, interval: interval, now: time.Now}
}

// WithClock overrides the clock used for sending-window checks (tests).
func (s *Scheduler) WithClock(now func() time.Time) *Scheduler {
	s.now = now
	return s
}

func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("scheduler tick failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick runs one scheduling pass. It is exported for tests.
func (s *Scheduler) Tick(ctx context.Context) error {
	release, ok, err := redisx.TryLock(ctx, s.rdb, "campaign-scheduler", 5*time.Minute)
	if err != nil || !ok {
		return err
	}
	defer release()

	if _, err := s.pool.Exec(ctx, `UPDATE campaigns SET status='running', updated_at=now()
		WHERE status='scheduled' AND start_at <= now()`); err != nil {
		return err
	}
	if err := s.recover(ctx); err != nil {
		s.log.Error("scheduler recovery failed", "error", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.organization_id, c.daily_limit, c.timezone, to_char(c.send_window_start,'HH24:MI'),
		       to_char(c.send_window_end,'HH24:MI'), c.send_days, c.allowed_email_statuses, c.max_bounce_rate::float8,
		       c.ai_personalization, c.require_personalization_approval,
		       o.daily_send_limit, COALESCE(NULLIF(o.legal_name,''), o.name), o.physical_address
		FROM campaigns c
		JOIN organizations o ON o.id = c.organization_id
		WHERE c.status = 'running'
		ORDER BY c.launched_at`)
	if err != nil {
		return err
	}
	var runs []runCtx
	for rows.Next() {
		var r runCtx
		var days []int32
		if err := rows.Scan(&r.ID, &r.OrgID, &r.DailyLimit, &r.TZ, &r.WinStart, &r.WinEnd, &days,
			&r.Allowed, &r.MaxBounce, &r.Personalize, &r.RequireApproval,
			&r.OrgLimit, &r.SenderCompany, &r.PhysicalAddress); err != nil {
			rows.Close()
			return err
		}
		for _, d := range days {
			r.Days = append(r.Days, int(d))
		}
		runs = append(runs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, r := range runs {
		if err := s.runCampaign(ctx, r); err != nil {
			s.log.Error("scheduling campaign failed", "campaign_id", r.ID, "error", err)
		}
	}
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			UPDATE campaigns c SET status='completed', completed_at=now(), updated_at=now()
			WHERE c.status='running' AND NOT EXISTS (
				SELECT 1 FROM campaign_leads cl WHERE cl.campaign_id=c.id AND cl.status IN ('pending','active','sending'))
			RETURNING c.id, c.organization_id`)
		if err != nil {
			return err
		}
		type done struct{ id, org uuid.UUID }
		var finished []done
		for rows.Next() {
			var d done
			if err := rows.Scan(&d.id, &d.org); err != nil {
				rows.Close()
				return err
			}
			finished = append(finished, d)
		}
		rows.Close()
		for _, d := range finished {
			if err := webhooks.Publish(ctx, tx, d.org, webhooks.CampaignCompleted, map[string]any{"campaign_id": d.id}); err != nil {
				return err
			}
		}
		return nil
	})
}

type runCtx struct {
	ID, OrgID                      uuid.UUID
	DailyLimit, OrgLimit           int
	TZ, WinStart, WinEnd           string
	Days                           []int
	Allowed                        []string
	MaxBounce                      float64
	Personalize, RequireApproval   bool
	SenderCompany, PhysicalAddress string
}

// poolMailbox is one sending identity available to a campaign this tick.
type poolMailbox struct {
	ID                           uuid.UUID
	FromEmail, FromName, ReplyTo string
	Remaining                    int
}

func (s *Scheduler) pause(ctx context.Context, r runCtx, reason string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE campaigns SET status='paused', paused_reason=$2, updated_at=now() WHERE id=$1 AND status='running'`, r.ID, reason)
	if err == nil && tag.RowsAffected() > 0 {
		err = webhooks.Publish(ctx, s.pool, r.OrgID, webhooks.CampaignPaused, map[string]any{"campaign_id": r.ID, "reason": reason})
	}
	if err == nil {
		s.log.Warn("campaign auto-paused", "campaign_id", r.ID, "reason", reason)
		s.audit.Record(ctx, audit.Entry{OrgID: r.OrgID, Action: "campaign.auto_paused", EntityType: "campaign", EntityID: r.ID.String(), Metadata: map[string]any{"reason": reason}})
	}
	return err
}

// mailboxPool loads the campaign's active mailboxes with their remaining
// rolling-24h capacity (mailbox limits are shared across campaigns).
func (s *Scheduler) mailboxPool(ctx context.Context, campaignID uuid.UUID) (map[uuid.UUID]*poolMailbox, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.id, m.from_email::text, m.from_name, COALESCE(m.reply_to::text, ''),
		       m.daily_limit - (SELECT count(*) FROM email_messages e WHERE e.mailbox_id = m.id AND e.status <> 'cancelled'
		                        AND e.created_at > now() - interval '24 hours')
		FROM mailboxes m
		WHERE m.status = 'active' AND m.id IN (
			SELECT mailbox_id FROM campaign_mailboxes WHERE campaign_id = $1
			UNION SELECT mailbox_id FROM campaigns WHERE id = $1 AND mailbox_id IS NOT NULL)`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]*poolMailbox{}
	for rows.Next() {
		var m poolMailbox
		if err := rows.Scan(&m.ID, &m.FromEmail, &m.FromName, &m.ReplyTo, &m.Remaining); err != nil {
			return nil, err
		}
		out[m.ID] = &m
	}
	return out, rows.Err()
}

// ensurePersonalizations requests AI openers for every lead in the audience
// that has none yet, independent of today's send capacity, so openers are
// generated and reviewed ahead of when each lead's first email is due.
func (s *Scheduler) ensurePersonalizations(ctx context.Context, r runCtx) error {
	rows, err := s.pool.Query(ctx, `
		INSERT INTO lead_personalizations (organization_id, campaign_id, lead_id)
		SELECT cl.organization_id, cl.campaign_id, cl.lead_id FROM campaign_leads cl
		WHERE cl.campaign_id=$1 AND cl.status IN ('pending','active') AND cl.next_step_number = 1
		  AND NOT EXISTS (SELECT 1 FROM lead_personalizations lp WHERE lp.campaign_id=cl.campaign_id AND lp.lead_id=cl.lead_id)
		LIMIT 500
		ON CONFLICT DO NOTHING RETURNING lead_id`, r.ID)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, c := range jobs.Chunk(ids, 10) {
		if err := s.q.Enqueue(ctx, jobs.QueueDefault, jobs.TypePersonalize, jobs.Personalize{OrgID: r.OrgID, CampaignID: r.ID, LeadIDs: c}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scheduler) runCampaign(ctx context.Context, r runCtx) error {
	if r.Personalize {
		if err := s.ensurePersonalizations(ctx, r); err != nil {
			return err
		}
	}
	pool, err := s.mailboxPool(ctx, r.ID)
	if err != nil {
		return err
	}
	if len(pool) == 0 {
		return s.pause(ctx, r, "no active sending mailbox is assigned to this campaign")
	}
	sent, bounced, err := bounceStats(ctx, s.pool, r.ID)
	if err != nil {
		return err
	}
	if exceedsBounceLimit(sent, bounced, r.MaxBounce) {
		return s.pause(ctx, r, fmt.Sprintf("bounce rate %.1f%% exceeded the %.1f%% limit", 100*float64(bounced)/float64(sent), 100*r.MaxBounce))
	}

	win, err := NewWindow(r.TZ, r.WinStart, r.WinEnd, r.Days)
	if err != nil {
		return s.pause(ctx, r, "invalid sending window: "+err.Error())
	}
	now := s.now()
	if !win.Open(now) {
		return nil
	}

	var usedCampaign, usedOrg int
	if err := s.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM email_messages WHERE campaign_id=$1 AND status<>'cancelled' AND created_at > now()-interval '24 hours'),
		       (SELECT count(*) FROM email_messages WHERE organization_id=$2 AND status<>'cancelled' AND created_at > now()-interval '24 hours')`,
		r.ID, r.OrgID).Scan(&usedCampaign, &usedOrg); err != nil {
		return err
	}
	mailboxCapacity := 0
	for _, m := range pool {
		mailboxCapacity += max(0, m.Remaining)
	}
	budget := min(r.DailyLimit-usedCampaign, mailboxCapacity, r.OrgLimit-usedOrg,
		PerTick(r.DailyLimit, win.Length(), s.interval), 200)
	if budget <= 0 {
		return nil
	}

	var queued []uuid.UUID
	var needPersonalization []uuid.UUID
	err = database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		queued, needPersonalization, err = s.queueDue(ctx, tx, r, pool, budget)
		return err
	})
	if err != nil {
		return err
	}
	for _, id := range queued {
		if err := s.q.Enqueue(ctx, jobs.QueueEmail, jobs.TypeSendEmail, jobs.SendEmail{MessageID: id}, jobs.SendEmailOpts); err != nil {
			return err // the recovery sweep re-enqueues anything missed
		}
	}
	// Leads reaching their first email without an opener: generate one now.
	for _, c := range jobs.Chunk(needPersonalization, 10) {
		if err := s.q.Enqueue(ctx, jobs.QueueDefault, jobs.TypePersonalize, jobs.Personalize{OrgID: r.OrgID, CampaignID: r.ID, LeadIDs: c}); err != nil {
			return err
		}
	}
	if len(queued) > 0 {
		s.log.Info("campaign messages queued", "campaign_id", r.ID, "count", len(queued))
	}
	return nil
}

type dueLead struct {
	id, leadID      uuid.UUID
	contactID       *uuid.UUID
	mailboxID       *uuid.UUID
	email           string
	emailStatus     string
	stepNumber      int
	recipient       Recipient
	stepID          *uuid.UUID
	delay           int
	subjectTpl      string
	bodyTpl         string
	suppressed      bool
	prevSubject     string
	prevMessageID   string
	personalStatus  *string
	personalContent string
}

// pick chooses a mailbox for a lead: its existing one (keeps the thread), or
// the pool mailbox with the most remaining capacity.
func pick(pool map[uuid.UUID]*poolMailbox, current *uuid.UUID) *poolMailbox {
	if current != nil {
		if m, ok := pool[*current]; ok {
			if m.Remaining > 0 {
				return m
			}
			return nil // its mailbox is at capacity: wait rather than break the thread
		}
	}
	var best *poolMailbox
	for _, m := range pool {
		if m.Remaining > 0 && (best == nil || m.Remaining > best.Remaining) {
			best = m
		}
	}
	return best
}

// queueDue renders and inserts messages for due campaign leads inside tx.
// It returns the queued message IDs and the leads that still need an AI opener.
func (s *Scheduler) queueDue(ctx context.Context, tx pgx.Tx, r runCtx, pool map[uuid.UUID]*poolMailbox, limit int) ([]uuid.UUID, []uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		SELECT cl.id, cl.lead_id, cl.contact_id, cl.mailbox_id, cl.email::text, COALESCE(ct.email_status, 'unverified'), cl.next_step_number,
		       COALESCE(ct.first_name,''), COALESCE(ct.last_name,''), COALESCE(ct.job_title,''),
		       l.company_name, l.website, l.industry, l.city, l.state, l.country,
		       st.id, COALESCE(st.delay_days,0), COALESCE(st.subject_template,''), COALESCE(st.body_template,''),
		       EXISTS (SELECT 1 FROM suppression_list sl WHERE sl.organization_id = cl.organization_id AND sl.email = cl.email),
		       COALESCE(prev.subject, ''), COALESCE(prev.message_id, ''),
		       lp.status, COALESCE(lp.content, '')
		FROM campaign_leads cl
		JOIN leads l ON l.id = cl.lead_id
		LEFT JOIN lead_contacts ct ON ct.id = cl.contact_id
		LEFT JOIN campaign_steps st ON st.campaign_id = cl.campaign_id AND st.step_number = cl.next_step_number
		LEFT JOIN lead_personalizations lp ON lp.campaign_id = cl.campaign_id AND lp.lead_id = cl.lead_id
		LEFT JOIN LATERAL (
			SELECT m.subject, m.message_id FROM email_messages m
			WHERE m.campaign_lead_id = cl.id AND m.status = 'sent' ORDER BY m.step_number DESC LIMIT 1
		) prev ON true
		WHERE cl.campaign_id = $1 AND cl.status = 'active' AND cl.next_send_at <= now()
		ORDER BY cl.next_send_at, cl.id
		LIMIT $2
		FOR UPDATE OF cl SKIP LOCKED`, r.ID, limit)
	if err != nil {
		return nil, nil, err
	}
	var due []dueLead
	for rows.Next() {
		var d dueLead
		var rc = &d.recipient
		if err := rows.Scan(&d.id, &d.leadID, &d.contactID, &d.mailboxID, &d.email, &d.emailStatus, &d.stepNumber,
			&rc.FirstName, &rc.LastName, &rc.JobTitle, &rc.CompanyName, &rc.Website, &rc.Industry, &rc.City, &rc.State, &rc.Country,
			&d.stepID, &d.delay, &d.subjectTpl, &d.bodyTpl, &d.suppressed, &d.prevSubject, &d.prevMessageID,
			&d.personalStatus, &d.personalContent); err != nil {
			rows.Close()
			return nil, nil, err
		}
		due = append(due, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	setLead := func(id uuid.UUID, status, reason string, nextSend *time.Time) error {
		_, err := tx.Exec(ctx, `UPDATE campaign_leads SET status=$2, stop_reason=NULLIF($3,''), next_send_at=$4, updated_at=now() WHERE id=$1`,
			id, status, reason, nextSend)
		return err
	}

	var queued, needAI []uuid.UUID
	for _, d := range due {
		switch {
		case d.stepID == nil:
			// Sequence shortened after this lead was scheduled.
			if err := setLead(d.id, "completed", "", nil); err != nil {
				return nil, nil, err
			}
			continue
		case d.suppressed:
			if err := setLead(d.id, "suppressed", "address is on the suppression list", nil); err != nil {
				return nil, nil, err
			}
			continue
		case d.emailStatus == "unverified" && !contains(r.Allowed, "unverified"):
			// Verification is probably still running; check again later.
			later := s.now().Add(time.Hour)
			if err := setLead(d.id, "active", "awaiting email verification", &later); err != nil {
				return nil, nil, err
			}
			continue
		case !contains(r.Allowed, d.emailStatus):
			if err := setLead(d.id, "stopped", "email status "+d.emailStatus+" not allowed by campaign", nil); err != nil {
				return nil, nil, err
			}
			continue
		}

		// AI personalization gate: wait for a reviewed opener (failed or
		// rejected openers fall back to the template's {{personalized_line|…}}).
		line := ""
		if r.Personalize {
			st := ""
			if d.personalStatus != nil {
				st = *d.personalStatus
			}
			ready := st == "approved" || st == "rejected" || st == "failed" || (st == "generated" && !r.RequireApproval)
			if !ready {
				if st == "" {
					if _, err := tx.Exec(ctx, `INSERT INTO lead_personalizations (organization_id, campaign_id, lead_id) VALUES ($1,$2,$3)
						ON CONFLICT DO NOTHING`, r.OrgID, r.ID, d.leadID); err != nil {
						return nil, nil, err
					}
					needAI = append(needAI, d.leadID)
				}
				later := s.now().Add(15 * time.Minute)
				reason := "awaiting AI opener"
				if st == "generated" {
					reason = "awaiting review of AI opener"
				}
				if err := setLead(d.id, "active", reason, &later); err != nil {
					return nil, nil, err
				}
				continue
			}
			if st == "approved" || st == "generated" {
				line = d.personalContent
			}
		}

		mb := pick(pool, d.mailboxID)
		if mb == nil {
			continue // mailbox capacity exhausted this tick; retry next tick
		}
		mb.Remaining--

		sender := Sender{FromName: mb.FromName, FromEmail: mb.FromEmail, SenderCompany: r.SenderCompany, PhysicalAddress: r.PhysicalAddress}
		d.recipient.PersonalizedLine = line
		msgID := uuid.New()
		domain := mb.FromEmail[strings.LastIndex(mb.FromEmail, "@")+1:]
		rfcID := msgID.String() + "@" + domain
		unsubURL := s.unsub.URL(email.UnsubscribeClaims{OrgID: r.OrgID, Email: d.email, CampaignID: r.ID, MessageID: msgID})
		step := Step{StepNumber: d.stepNumber, SubjectTemplate: d.subjectTpl, BodyTemplate: d.bodyTpl}
		out := RenderStep(step, d.prevSubject, d.recipient, sender, unsubURL)
		inReplyTo := ""
		if strings.HasPrefix(out.Subject, "Re: ") {
			inReplyTo = d.prevMessageID
		}

		tag, err := tx.Exec(ctx, `
			INSERT INTO email_messages (id, organization_id, campaign_id, campaign_lead_id, lead_id, contact_id, step_id,
				step_number, mailbox_id, to_email, from_email, from_name, reply_to, subject, body_text, body_html,
				message_id, in_reply_to, status)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,'queued')
			ON CONFLICT (campaign_id, lead_id, step_id) WHERE status <> 'cancelled' DO NOTHING`,
			msgID, r.OrgID, r.ID, d.id, d.leadID, d.contactID, *d.stepID, d.stepNumber, mb.ID, d.email,
			mb.FromEmail, mb.FromName, mb.ReplyTo, out.Subject, out.Text, out.HTML, rfcID, inReplyTo)
		if err != nil {
			return nil, nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE campaign_leads SET status='sending', mailbox_id=$2, stop_reason=NULL, next_send_at=NULL, updated_at=now() WHERE id=$1`,
			d.id, mb.ID); err != nil {
			return nil, nil, err
		}
		if tag.RowsAffected() == 0 {
			// This step already has a live message (idempotency key hit); the
			// send worker will advance the lead when it completes.
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO email_events (organization_id, campaign_id, lead_id, email_message_id, message_id, event_type)
			VALUES ($1,$2,$3,$4,$5,'queued')`, r.OrgID, r.ID, d.leadID, msgID, rfcID); err != nil {
			return nil, nil, err
		}
		queued = append(queued, msgID)
	}
	return queued, needAI, nil
}

// recover handles messages whose jobs were lost or whose worker died.
func (s *Scheduler) recover(ctx context.Context) error {
	// Queued too long: the Redis job was probably lost. Re-enqueue; the
	// worker's atomic claim makes duplicates harmless.
	rows, err := s.pool.Query(ctx, `
		UPDATE email_messages SET updated_at=now()
		WHERE id IN (SELECT id FROM email_messages WHERE status='queued' AND updated_at < now() - make_interval(secs => $1) LIMIT 500)
		RETURNING id`, jobs.SweepAge.Seconds())
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := s.q.Enqueue(ctx, jobs.QueueEmail, jobs.TypeSendEmail, jobs.SendEmail{MessageID: id}, jobs.SendEmailOpts); err != nil {
			return err
		}
	}

	// Stuck in "sending": the worker died mid-send, so the provider may or may
	// not have accepted it. We never resend (a duplicate is worse than a gap);
	// the message is marked failed for visibility.
	_, err = s.pool.Exec(ctx, `
		WITH stuck AS (
			UPDATE email_messages SET status='failed', last_error='outcome unknown: worker stopped mid-send; not retried to avoid a duplicate', updated_at=now()
			WHERE status='sending' AND updated_at < now() - interval '15 minutes'
			RETURNING campaign_lead_id
		)
		UPDATE campaign_leads SET status='failed', stop_reason='send outcome unknown', updated_at=now()
		WHERE id IN (SELECT campaign_lead_id FROM stuck) AND status='sending'`)
	return err
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
