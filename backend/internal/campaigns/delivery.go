package campaigns

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/email"
	"leadline/internal/jobs"
	"leadline/internal/suppression"
	"leadline/internal/vault"
	"leadline/internal/webhooks"
	"leadline/pkg/database"
	"leadline/pkg/metrics"
	"leadline/pkg/queue"
)

// Delivery is the email.send job handler.
//
// Exactly-once is impossible with an external provider, so the design is
// at-most-once per (campaign, lead, step):
//   - the scheduler's unique index means one live message row per step;
//   - the worker atomically claims the row (queued → sending) before calling
//     the provider, so concurrent or repeated jobs for the same message no-op;
//   - a crash after the provider accepted the message leaves it in "sending",
//     which the recovery sweep marks failed instead of resending.
type Delivery struct {
	pool     *pgxpool.Pool
	provider email.EmailProvider // default transport
	unsub    *email.Unsubscriber
	log      *slog.Logger
	vault    *vault.Vault
	// OrgSMTP, when set, returns the organization's connected email-sending
	// integration; it is used for mailboxes without their own SMTP server.
	OrgSMTP func(ctx context.Context, orgID uuid.UUID) (email.SMTPConfig, bool, error)
}

func NewDelivery(pool *pgxpool.Pool, p email.EmailProvider, u *email.Unsubscriber, log *slog.Logger, v *vault.Vault) *Delivery {
	return &Delivery{pool: pool, provider: p, unsub: u, log: log, vault: v}
}

// mailboxSMTP holds a mailbox's own SMTP settings, when it has them.
type mailboxSMTP struct {
	host, username, tls string
	port                int
	password            []byte
}

// providerFor returns the mailbox's own SMTP transport, else the
// organization's email-sending integration, else the default provider.
func (d *Delivery) providerFor(ctx context.Context, m *claimed) (email.EmailProvider, error) {
	if m.smtp.host == "" {
		if d.OrgSMTP != nil {
			cfg, ok, err := d.OrgSMTP(ctx, m.OrgID)
			if err != nil {
				return nil, &email.SendError{ProviderFault: true, Err: err}
			}
			if ok {
				return email.NewSMTP(cfg), nil
			}
		}
		return d.provider, nil
	}
	pw := ""
	if len(m.smtp.password) > 0 {
		var err error
		if pw, err = d.vault.Open(m.smtp.password); err != nil {
			return nil, &email.SendError{ProviderFault: true, Err: err}
		}
	}
	return email.NewSMTP(email.SMTPConfig{Host: m.smtp.host, Port: m.smtp.port, Username: m.smtp.username,
		Password: pw, TLS: m.smtp.tls, Guarded: true}), nil
}

type claimed struct {
	ID, OrgID, CampaignID, CampaignLeadID, LeadID uuid.UUID
	StepNumber                                    int
	CampaignStatus                                string
	Msg                                           email.EmailMessage
	smtp                                          mailboxSMTP
}

func (d *Delivery) Handle(ctx context.Context, job queue.Job) error {
	var p jobs.SendEmail
	if err := job.Decode(&p); err != nil {
		return queue.Permanent(err)
	}
	m, err := d.claim(ctx, p.MessageID)
	if err != nil || m == nil {
		return err // nil, nil: already handled elsewhere
	}
	log := d.log.With("message_id", m.ID, "campaign_id", m.CampaignID, "organization_id", m.OrgID)

	provider, sendErr := d.providerFor(ctx, m)
	var res email.SendResult
	if sendErr == nil {
		res, sendErr = provider.Send(ctx, m.Msg)
	}
	if sendErr == nil {
		metrics.EmailsSent.Inc()
		return d.markSent(context.WithoutCancel(ctx), m, res)
	}

	se, _ := email.AsSendError(sendErr)
	if se != nil && se.ProviderFault {
		log.Error("email provider rejected the request; pausing campaign", "error", sendErr)
		return d.pauseForProvider(context.WithoutCancel(ctx), m, sendErr)
	}
	permanent := se != nil && se.Permanent
	metrics.EmailsFailed.WithLabelValues(map[bool]string{true: "true", false: "false"}[permanent]).Inc()
	if permanent || job.IsFinalAttempt() {
		log.Warn("email send failed permanently", "error", sendErr, "attempt", job.Attempt)
		if err := d.markFailed(context.WithoutCancel(ctx), m, sendErr.Error(), se != nil && se.Bounce); err != nil {
			return err
		}
		return queue.Permanent(sendErr) // recorded in the dead-letter list for visibility
	}
	log.Warn("email send failed, will retry", "error", sendErr, "attempt", job.Attempt)
	if _, err := d.pool.Exec(context.WithoutCancel(ctx), `UPDATE email_messages SET status='queued', last_error=$2, updated_at=now() WHERE id=$1 AND status='sending'`,
		m.ID, sendErr.Error()); err != nil {
		return err
	}
	return sendErr
}

// claim atomically moves a message from queued to sending. It returns nil
// when the message is gone, already claimed, cancelled, or no longer allowed
// to be sent.
func (d *Delivery) claim(ctx context.Context, id uuid.UUID) (*claimed, error) {
	var m *claimed
	err := database.WithTx(ctx, d.pool, func(tx pgx.Tx) error {
		var c claimed
		var suppressed bool
		err := tx.QueryRow(ctx, `
			SELECT m.id, m.organization_id, m.campaign_id, m.campaign_lead_id, m.lead_id, m.step_number, c.status,
			       m.message_id, m.from_email, m.from_name, m.reply_to, m.to_email::text, m.subject, m.body_text, m.body_html,
			       m.in_reply_to,
			       EXISTS (SELECT 1 FROM suppression_list s WHERE s.organization_id = m.organization_id AND s.email = m.to_email),
			       COALESCE(mb.smtp_host, ''), COALESCE(mb.smtp_port, 587), COALESCE(mb.smtp_username, ''), mb.smtp_password_enc,
			       COALESCE(mb.smtp_tls, 'starttls')
			FROM email_messages m JOIN campaigns c ON c.id = m.campaign_id
			LEFT JOIN mailboxes mb ON mb.id = m.mailbox_id
			WHERE m.id = $1 AND m.status = 'queued'
			FOR UPDATE OF m SKIP LOCKED`, id).Scan(
			&c.ID, &c.OrgID, &c.CampaignID, &c.CampaignLeadID, &c.LeadID, &c.StepNumber, &c.CampaignStatus,
			&c.Msg.MessageID, &c.Msg.FromEmail, &c.Msg.FromName, &c.Msg.ReplyTo, &c.Msg.ToEmail, &c.Msg.Subject,
			&c.Msg.TextBody, &c.Msg.HTMLBody, &c.Msg.InReplyTo, &suppressed,
			&c.smtp.host, &c.smtp.port, &c.smtp.username, &c.smtp.password, &c.smtp.tls)
		if database.IsNoRows(err) {
			return nil
		}
		if err != nil {
			return err
		}
		switch {
		case suppressed:
			if _, err := tx.Exec(ctx, `UPDATE email_messages SET status='cancelled', last_error='suppressed', updated_at=now() WHERE id=$1`, c.ID); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE campaign_leads SET status='suppressed', stop_reason='address is on the suppression list', updated_at=now() WHERE id=$1 AND status='sending'`, c.CampaignLeadID)
			return err
		case c.CampaignStatus != StatusRunning:
			// Paused/cancelled after queueing: release the idempotency key and
			// put the lead back so resuming picks the step up again.
			if _, err := tx.Exec(ctx, `UPDATE email_messages SET status='cancelled', last_error='campaign '||$2, updated_at=now() WHERE id=$1`, c.ID, c.CampaignStatus); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE campaign_leads SET status = CASE WHEN $2 = 'cancelled' THEN 'stopped' ELSE 'active' END,
				next_send_at = now(), updated_at=now() WHERE id=$1 AND status='sending'`, c.CampaignLeadID, c.CampaignStatus)
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE email_messages SET status='sending', attempts=attempts+1, updated_at=now() WHERE id=$1`, c.ID); err != nil {
			return err
		}
		m = &c
		return nil
	})
	if err != nil || m == nil {
		return nil, err
	}
	m.Msg.UnsubscribeURL = d.unsub.URL(email.UnsubscribeClaims{OrgID: m.OrgID, Email: m.Msg.ToEmail, CampaignID: m.CampaignID, MessageID: m.ID})
	m.Msg.Headers = map[string]string{"X-Campaign-ID": m.CampaignID.String()}
	return m, nil
}

func (d *Delivery) markSent(ctx context.Context, m *claimed, res email.SendResult) error {
	return database.WithTx(ctx, d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE email_messages SET status='sent', sent_at=now(), provider=$2, provider_message_id=$3,
			last_error=NULL, updated_at=now() WHERE id=$1`, m.ID, res.Provider, res.ProviderMessageID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO email_events (organization_id, campaign_id, lead_id, email_message_id, message_id, event_type, metadata)
			VALUES ($1,$2,$3,$4,$5,'sent',$6)`, m.OrgID, m.CampaignID, m.LeadID, m.ID, m.Msg.MessageID,
			map[string]any{"provider": res.Provider, "step": m.StepNumber}); err != nil {
			return err
		}
		// Advance the sequence — unless the lead was stopped meanwhile
		// (reply, unsubscribe, bounce), in which case its status is no longer 'sending'.
		var nextDelay *int
		err := tx.QueryRow(ctx, `SELECT delay_days FROM campaign_steps WHERE campaign_id=$1 AND step_number=$2`,
			m.CampaignID, m.StepNumber+1).Scan(&nextDelay)
		if err != nil && !database.IsNoRows(err) {
			return err
		}
		if nextDelay != nil {
			next := time.Now().Add(time.Duration(*nextDelay) * 24 * time.Hour)
			_, err = tx.Exec(ctx, `UPDATE campaign_leads SET status='active', next_step_number=$2, next_send_at=$3,
				last_sent_at=now(), stop_reason=NULL, updated_at=now() WHERE id=$1 AND status='sending'`, m.CampaignLeadID, m.StepNumber+1, next)
		} else {
			_, err = tx.Exec(ctx, `UPDATE campaign_leads SET status='completed', next_send_at=NULL, last_sent_at=now(),
				stop_reason=NULL, updated_at=now() WHERE id=$1 AND status='sending'`, m.CampaignLeadID)
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE leads SET status='contacted', updated_at=now() WHERE id=$1 AND status='new'`, m.LeadID); err != nil {
			return err
		}
		if err := webhooks.Publish(ctx, tx, m.OrgID, webhooks.EmailSent, map[string]any{
			"campaign_id": m.CampaignID, "lead_id": m.LeadID, "message_id": m.Msg.MessageID, "step": m.StepNumber, "to": m.Msg.ToEmail,
		}); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"campaign_id": m.CampaignID, "step": m.StepNumber, "subject": m.Msg.Subject})
		_, err = tx.Exec(ctx, `INSERT INTO lead_events (organization_id, lead_id, event_type, data) VALUES ($1,$2,'email_sent',$3)`, m.OrgID, m.LeadID, data)
		return err
	})
}

// pauseForProvider returns the message to the sequence untouched and pauses
// the campaign so an operator can fix the provider configuration.
func (d *Delivery) pauseForProvider(ctx context.Context, m *claimed, sendErr error) error {
	return database.WithTx(ctx, d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE email_messages SET status='cancelled', last_error=$2, updated_at=now() WHERE id=$1`, m.ID, sendErr.Error()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE campaign_leads SET status='active', next_send_at=now(), updated_at=now() WHERE id=$1 AND status='sending'`, m.CampaignLeadID); err != nil {
			return err
		}
		reason := "email provider rejected the request — check the SMTP/provider credentials (" + truncate(sendErr.Error(), 200) + ")"
		tag, err := tx.Exec(ctx, `UPDATE campaigns SET status='paused', paused_reason=$2, updated_at=now() WHERE id=$1 AND status='running'`, m.CampaignID, reason)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return webhooks.Publish(ctx, tx, m.OrgID, webhooks.CampaignPaused, map[string]any{"campaign_id": m.CampaignID, "reason": reason})
	})
}

func (d *Delivery) markFailed(ctx context.Context, m *claimed, reason string, bounce bool) error {
	return database.WithTx(ctx, d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE email_messages SET status='failed', last_error=$2, updated_at=now() WHERE id=$1`, m.ID, reason); err != nil {
			return err
		}
		evType := "failed"
		if bounce {
			// Synchronous rejection of the recipient (SMTP 550-553) is a hard bounce.
			evType = "bounced"
			if err := suppression.Add(ctx, tx, suppression.AddInput{OrgID: m.OrgID, Email: m.Msg.ToEmail,
				Reason: suppression.HardBounce, Source: "smtp_rejection", CampaignID: &m.CampaignID}); err != nil {
				return err
			}
			metrics.Bounces.Inc()
		}
		if _, err := tx.Exec(ctx, `INSERT INTO email_events (organization_id, campaign_id, lead_id, email_message_id, message_id, event_type, metadata)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, m.OrgID, m.CampaignID, m.LeadID, m.ID, m.Msg.MessageID, evType,
			map[string]any{"error": reason, "bounce_type": "hard"}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE campaign_leads SET status='failed', stop_reason=$2, next_send_at=NULL, updated_at=now()
			WHERE id=$1 AND status='sending'`, m.CampaignLeadID, truncate(reason, 300))
		return err
	})
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
