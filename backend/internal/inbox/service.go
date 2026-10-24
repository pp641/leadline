package inbox

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"leadline/internal/auth"
	"leadline/internal/email"
	"leadline/internal/integrations"
	"leadline/internal/jobs"
	"leadline/internal/leads"
	"leadline/internal/replies"
	"leadline/internal/vault"
	"leadline/internal/webhooks"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/httpx"
	"leadline/pkg/queue"
	"leadline/pkg/redisx"
	"leadline/pkg/secure"
)

type Service struct {
	pool   *pgxpool.Pool
	rdb    *redis.Client
	events *email.Events
	llm    integrations.LLMProvider
	vault  *vault.Vault
	q      jobs.Enqueuer
	log    *slog.Logger
}

func NewService(pool *pgxpool.Pool, rdb *redis.Client, ev *email.Events, llm integrations.LLMProvider, v *vault.Vault, q jobs.Enqueuer, log *slog.Logger) *Service {
	return &Service{pool: pool, rdb: rdb, events: ev, llm: llm, vault: v, q: q, log: log}
}

type match struct {
	MessageRowID, CampaignID, CampaignLeadID, LeadID uuid.UUID
	RFCMessageID                                     string
}

// matchReply finds the outbound message an inbound email answers: first by
// threading headers, then (for replies) by sender address.
func (s *Service) matchReply(ctx context.Context, orgID uuid.UUID, p *Parsed) (*match, error) {
	ids := append([]string{}, p.References...)
	if p.InReplyTo != "" {
		ids = append(ids, p.InReplyTo)
	}
	ids = append(ids, p.EmbeddedIDs...)
	var m match
	scan := func(q string, args ...any) error {
		return s.pool.QueryRow(ctx, q, args...).Scan(&m.MessageRowID, &m.CampaignID, &m.CampaignLeadID, &m.LeadID, &m.RFCMessageID)
	}
	if len(ids) > 0 {
		err := scan(`SELECT id, campaign_id, campaign_lead_id, lead_id, message_id FROM email_messages
			WHERE organization_id=$1 AND message_id = ANY($2) ORDER BY step_number DESC LIMIT 1`, orgID, ids)
		if err == nil {
			return &m, nil
		}
		if !database.IsNoRows(err) {
			return nil, err
		}
	}
	if p.Bounce || p.FromEmail == "" {
		return nil, nil
	}
	err := scan(`SELECT id, campaign_id, campaign_lead_id, lead_id, message_id FROM email_messages
		WHERE organization_id=$1 AND to_email=$2 AND status='sent' AND sent_at > now() - interval '90 days'
		ORDER BY sent_at DESC LIMIT 1`, orgID, p.FromEmail)
	if database.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// Result reports what happened to one inbound message.
type Result struct {
	Stored         bool   `json:"stored"`
	Kind           string `json:"kind"`
	Classification string `json:"classification,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

// Process runs the pipeline for one raw message. Messages that don't match a
// campaign email are ignored and never stored (the mailbox may contain
// unrelated personal mail).
func (s *Service) Process(ctx context.Context, orgID uuid.UUID, mailboxID *uuid.UUID, raw []byte) (Result, error) {
	p, err := Parse(raw)
	if err != nil {
		return Result{Reason: "unparseable"}, nil
	}
	m, err := s.matchReply(ctx, orgID, p)
	if err != nil {
		return Result{}, err
	}
	if m == nil {
		return Result{Reason: "not a reply to a campaign email"}, nil
	}

	kind := "reply"
	var cls replies.Classification
	by := ""
	switch {
	case p.Bounce:
		kind = "bounce"
	case p.AutoReply:
		kind, cls, by = "auto_reply", replies.Classification{Label: "out_of_office", Confidence: 0.95, Summary: summarize(p.Text)}, "rules"
	default:
		cls, by = Classify(ctx, s.llm, replies.InboundEmail{MessageID: p.MessageID, From: p.FromEmail, Subject: p.Subject, TextBody: p.Text})
	}

	var inboundID uuid.UUID
	var conf *float64
	var label *string
	if cls.Label != "" {
		conf, label = &cls.Confidence, &cls.Label
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO inbound_emails (organization_id, mailbox_id, campaign_id, lead_id, campaign_lead_id, email_message_id,
			message_id, in_reply_to, from_email, from_name, subject, body_text, received_at, kind, classification, confidence, summary, classified_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		ON CONFLICT (organization_id, message_id) DO NOTHING RETURNING id`,
		orgID, mailboxID, m.CampaignID, m.LeadID, m.CampaignLeadID, m.MessageRowID, p.MessageID, p.InReplyTo,
		p.FromEmail, p.FromName, clip(p.Subject, 500), p.Text, p.Date, kind, label, conf, cls.Summary, by).Scan(&inboundID)
	if database.IsNoRows(err) {
		return Result{Reason: "already processed"}, nil
	}
	if err != nil {
		return Result{}, err
	}

	switch kind {
	case "bounce":
		bt := "hard"
		if !p.BounceHard {
			bt = "soft"
		}
		err = s.events.Ingest(ctx, email.InboundEvent{MessageID: m.RFCMessageID, EventType: "bounced", ProviderEventID: "inbound:" + p.MessageID,
			Metadata: map[string]any{"bounce_type": bt, "source": "inbox"}})
	case "auto_reply":
		// Out-of-office is not a real reply: the sequence continues.
		err = leads.RecordEvent(ctx, s.pool, orgID, m.LeadID, "auto_reply", nil, map[string]any{"campaign_id": m.CampaignID, "subject": p.Subject})
	default:
		err = s.events.Ingest(ctx, email.InboundEvent{MessageID: m.RFCMessageID, EventType: "replied", ProviderEventID: "inbound:" + p.MessageID,
			Metadata: map[string]any{"classification": cls.Label, "confidence": cls.Confidence, "summary": cls.Summary, "classified_by": by,
				"source": "inbox", "inbound_email_id": inboundID}})
		if err == nil {
			err = webhooks.Publish(ctx, s.pool, orgID, webhooks.ReplyReceived, map[string]any{
				"inbound_email_id": inboundID, "lead_id": m.LeadID, "campaign_id": m.CampaignID,
				"classification": cls.Label, "confidence": cls.Confidence, "summary": cls.Summary,
			})
		}
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Stored: true, Kind: kind, Classification: cls.Label}, nil
}

// Sync pulls new mail for one mailbox (background job).
func (s *Service) Sync(ctx context.Context, mailboxID uuid.UUID) error {
	release, ok, err := redisx.TryLock(ctx, s.rdb, "inbox-sync:"+mailboxID.String(), 10*time.Minute)
	if err != nil || !ok {
		return err
	}
	defer release()
	var orgID uuid.UUID
	var cfg IMAPConfig
	var pw []byte
	var st syncState
	var validity, last int64
	err = s.pool.QueryRow(ctx, `SELECT organization_id, imap_host, imap_port, imap_username, imap_password_enc, imap_tls, imap_uidvalidity, imap_last_uid
		FROM mailboxes WHERE id=$1 AND inbox_sync_enabled AND status='active'`, mailboxID).
		Scan(&orgID, &cfg.Host, &cfg.Port, &cfg.Username, &pw, &cfg.TLS, &validity, &last)
	if database.IsNoRows(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if cfg.Password, err = s.vault.Open(pw); err != nil {
		return s.syncFailed(ctx, mailboxID, err)
	}
	st = syncState{UIDValidity: uint32(validity), LastUID: uint32(last)}
	msgs, newState, fetchErr := fetchNew(cfg, st, 200)
	processed := 0
	for _, f := range msgs {
		if _, err := s.Process(ctx, orgID, &mailboxID, f.Raw); err != nil {
			s.log.Error("inbound processing failed", "mailbox_id", mailboxID, "uid", f.UID, "error", err)
			// Stop before this UID so it is retried next sync.
			newState.LastUID = f.UID - 1
			break
		}
		processed++
	}
	if _, err := s.pool.Exec(ctx, `UPDATE mailboxes SET imap_uidvalidity=$2, imap_last_uid=$3, last_synced_at=now(), sync_error=$4 WHERE id=$1`,
		mailboxID, int64(newState.UIDValidity), int64(newState.LastUID), errString(fetchErr)); err != nil {
		return err
	}
	if processed > 0 {
		s.log.Info("inbox synced", "mailbox_id", mailboxID, "messages", processed)
	}
	return nil // fetch errors are recorded on the mailbox, not retried as job failures
}

func (s *Service) syncFailed(ctx context.Context, mailboxID uuid.UUID, err error) error {
	_, _ = s.pool.Exec(ctx, `UPDATE mailboxes SET sync_error=$2, last_synced_at=now() WHERE id=$1`, mailboxID, err.Error())
	return nil
}

func errString(err error) *string {
	if err == nil {
		return nil
	}
	s := err.Error()
	return &s
}

// Poll enqueues a sync for every mailbox with inbox sync on (worker loop).
func (s *Service) Poll(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		rows, err := s.pool.Query(ctx, `SELECT id FROM mailboxes WHERE inbox_sync_enabled AND status='active'`)
		if err == nil {
			var ids []uuid.UUID
			for rows.Next() {
				var id uuid.UUID
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()
			for _, id := range ids {
				_ = s.q.Enqueue(ctx, jobs.QueueDefault, jobs.TypeInboxSync, jobs.InboxSync{MailboxID: id}, queue.EnqueueOpts{MaxAttempts: 1})
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) HandleSync(ctx context.Context, job queue.Job) error {
	var p jobs.InboxSync
	if err := job.Decode(&p); err != nil {
		return queue.Permanent(err)
	}
	return s.Sync(ctx, p.MailboxID)
}

// TestMailbox checks SMTP and IMAP credentials without sending anything.
func (s *Service) TestMailbox(ctx context.Context, p auth.Principal, mailboxID uuid.UUID) (map[string]string, error) {
	var smtpCfg email.SMTPConfig
	var imapCfg IMAPConfig
	var smtpPW, imapPW []byte
	err := s.pool.QueryRow(ctx, `SELECT smtp_host, smtp_port, smtp_username, smtp_password_enc, smtp_tls,
		imap_host, imap_port, imap_username, imap_password_enc, imap_tls FROM mailboxes WHERE organization_id=$1 AND id=$2`, p.OrgID, mailboxID).
		Scan(&smtpCfg.Host, &smtpCfg.Port, &smtpCfg.Username, &smtpPW, &smtpCfg.TLS, &imapCfg.Host, &imapCfg.Port, &imapCfg.Username, &imapPW, &imapCfg.TLS)
	if database.IsNoRows(err) {
		return nil, apperr.NotFound("mailbox")
	}
	if err != nil {
		return nil, err
	}
	out := map[string]string{"smtp": "using server default provider", "imap": "not configured"}
	if smtpCfg.Host != "" {
		smtpCfg.Guarded = true
		if len(smtpPW) > 0 {
			smtpCfg.Password, _ = s.vault.Open(smtpPW)
		}
		if err := email.NewSMTP(smtpCfg).Check(ctx); err != nil {
			out["smtp"] = "failed: " + err.Error()
		} else {
			out["smtp"] = "ok"
		}
	}
	if imapCfg.Host != "" {
		if len(imapPW) > 0 {
			imapCfg.Password, _ = s.vault.Open(imapPW)
		}
		if err := checkIMAP(imapCfg); err != nil {
			out["imap"] = "failed: " + err.Error()
		} else {
			out["imap"] = "ok"
		}
	}
	return out, nil
}

// ───────────────────────── Inbox API ─────────────────────────

type Message struct {
	ID             uuid.UUID  `json:"id"`
	LeadID         *uuid.UUID `json:"lead_id"`
	CompanyName    *string    `json:"company_name"`
	CampaignID     *uuid.UUID `json:"campaign_id"`
	CampaignName   *string    `json:"campaign_name"`
	CampaignLeadID *uuid.UUID `json:"campaign_lead_id"`
	FromEmail      string     `json:"from_email"`
	FromName       string     `json:"from_name"`
	Subject        string     `json:"subject"`
	BodyText       string     `json:"body_text"`
	ReceivedAt     time.Time  `json:"received_at"`
	Kind           string     `json:"kind"`
	Classification *string    `json:"classification"`
	Confidence     *float64   `json:"confidence"`
	Summary        string     `json:"summary"`
	ClassifiedBy   string     `json:"classified_by"`
	Status         string     `json:"status"`
}

func (s *Service) List(ctx context.Context, p auth.Principal, classification, status, kind string, page httpx.Page) ([]Message, int64, error) {
	where := `i.organization_id=$1 AND ($2 = '' OR i.classification=$2) AND ($3 = '' OR i.status=$3) AND ($4 = '' OR i.kind=$4)`
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM inbound_emails i WHERE `+where, p.OrgID, classification, status, kind).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT i.id, i.lead_id, l.company_name, i.campaign_id, c.name, i.campaign_lead_id, i.from_email, i.from_name, i.subject,
		       i.body_text, i.received_at, i.kind, i.classification, i.confidence, i.summary, i.classified_by, i.status
		FROM inbound_emails i LEFT JOIN leads l ON l.id=i.lead_id LEFT JOIN campaigns c ON c.id=i.campaign_id
		WHERE `+where+` ORDER BY i.received_at DESC LIMIT $5 OFFSET $6`, p.OrgID, classification, status, kind, page.Limit, page.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var conf *float32
		if err := rows.Scan(&m.ID, &m.LeadID, &m.CompanyName, &m.CampaignID, &m.CampaignName, &m.CampaignLeadID, &m.FromEmail, &m.FromName,
			&m.Subject, &m.BodyText, &m.ReceivedAt, &m.Kind, &m.Classification, &conf, &m.Summary, &m.ClassifiedBy, &m.Status); err != nil {
			return nil, 0, err
		}
		if conf != nil {
			f := float64(*conf)
			m.Confidence = &f
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}

// Update changes status and/or reclassifies (a manual reclassification is
// re-ingested, so e.g. changing to "unsubscribe" suppresses the address).
func (s *Service) Update(ctx context.Context, p auth.Principal, id uuid.UUID, status, classification string) error {
	var msgID, kind string
	var cur *string
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(m.message_id, ''), i.kind, i.classification FROM inbound_emails i
		LEFT JOIN email_messages m ON m.id=i.email_message_id WHERE i.organization_id=$1 AND i.id=$2`, p.OrgID, id).Scan(&msgID, &kind, &cur)
	if database.IsNoRows(err) {
		return apperr.NotFound("message")
	}
	if err != nil {
		return err
	}
	if status != "" {
		if status != "new" && status != "handled" && status != "archived" {
			return apperr.Invalid("validation failed", map[string]string{"status": "must be new, handled or archived"})
		}
		if _, err := s.pool.Exec(ctx, `UPDATE inbound_emails SET status=$3 WHERE organization_id=$1 AND id=$2`, p.OrgID, id, status); err != nil {
			return err
		}
	}
	if classification != "" && (cur == nil || *cur != classification) {
		valid := false
		for _, c := range email.ReplyClassifications {
			valid = valid || c == classification
		}
		if !valid {
			return apperr.Invalid("validation failed", map[string]string{"classification": "invalid classification"})
		}
		if _, err := s.pool.Exec(ctx, `UPDATE inbound_emails SET classification=$3, confidence=1, classified_by='manual', kind='reply' WHERE organization_id=$1 AND id=$2`,
			p.OrgID, id, classification); err != nil {
			return err
		}
		if msgID != "" {
			return s.events.Ingest(ctx, email.InboundEvent{MessageID: msgID, EventType: "replied",
				ProviderEventID: fmt.Sprintf("reclass:%s:%s:%d", id, classification, time.Now().UnixNano()),
				Metadata:        map[string]any{"classification": classification, "source": "manual_reclassification", "recorded_by": p.UserID.String()}})
		}
	}
	return nil
}

// ───────────────────────── HTTP ─────────────────────────

type Handler struct {
	svc           *Service
	mw            *auth.Middleware
	webhookSecret []byte
}

func NewHandler(svc *Service, mw *auth.Middleware, webhookSecret string) *Handler {
	return &Handler{svc: svc, mw: mw, webhookSecret: []byte(webhookSecret)}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/inbox", h.mw.Org(auth.RoleViewer, h.list))
	mux.HandleFunc("PATCH /api/v1/inbox/{id}", h.mw.Org(auth.RoleMember, h.update))
	mux.HandleFunc("POST /api/v1/mailboxes/{id}/sync", h.mw.Org(auth.RoleMember, h.sync))
	mux.HandleFunc("POST /api/v1/mailboxes/{id}/test", h.mw.Org(auth.RoleAdmin, h.test))
	mux.HandleFunc("POST /api/v1/webhooks/inbound/generic", h.inboundWebhook)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page := httpx.PageFrom(r)
	q := r.URL.Query()
	out, total, err := h.svc.List(r.Context(), auth.MustPrincipal(r.Context()), q.Get("classification"), q.Get("status"), q.Get("kind"), page)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out, map[string]any{"total": total, "page": page.Offset/page.Limit + 1, "per_page": page.Limit, "classifications": email.ReplyClassifications})
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Error(w, r, apperr.NotFound("message"))
		return
	}
	var in struct {
		Status         string `json:"status"`
		Classification string `json:"classification"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.svc.Update(r.Context(), auth.MustPrincipal(r.Context()), id, in.Status, in.Classification); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) sync(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Error(w, r, apperr.NotFound("mailbox"))
		return
	}
	p := auth.MustPrincipal(r.Context())
	var ok bool
	if err := h.svc.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM mailboxes WHERE organization_id=$1 AND id=$2 AND inbox_sync_enabled)`, p.OrgID, id).Scan(&ok); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if !ok {
		httpx.Error(w, r, apperr.Conflict("sync_disabled", "turn on inbox sync for this mailbox first"))
		return
	}
	if err := h.svc.q.Enqueue(r.Context(), jobs.QueueDefault, jobs.TypeInboxSync, jobs.InboxSync{MailboxID: id}, queue.EnqueueOpts{MaxAttempts: 1}); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]string{"status": "queued"}, nil)
}

func (h *Handler) test(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Error(w, r, apperr.NotFound("mailbox"))
		return
	}
	out, err := h.svc.TestMailbox(r.Context(), auth.MustPrincipal(r.Context()), id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

// inboundWebhook accepts raw messages forwarded by an email provider's inbound
// routing: {"mailbox_email": "...", "raw": "<base64 RFC 5322>"}, signed like
// the delivery-events webhook (X-Webhook-Signature, HMAC-SHA256 hex).
func (h *Handler) inboundWebhook(w http.ResponseWriter, r *http.Request) {
	if len(h.webhookSecret) == 0 {
		httpx.Error(w, r, apperr.Forbidden("webhooks are disabled: WEBHOOK_SECRET is not set"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 15<<20))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if subtle.ConstantTimeCompare([]byte(secure.HMACHex(h.webhookSecret, body)), []byte(r.Header.Get("X-Webhook-Signature"))) != 1 {
		httpx.Error(w, r, apperr.Unauthorized("invalid webhook signature"))
		return
	}
	var in struct {
		MailboxEmail string `json:"mailbox_email"`
		Raw          string `json:"raw"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		httpx.Error(w, r, apperr.Invalid("invalid JSON", nil))
		return
	}
	raw, err := base64.StdEncoding.DecodeString(in.Raw)
	if err != nil {
		httpx.Error(w, r, apperr.Invalid("raw must be base64", nil))
		return
	}
	// The receiving mailbox identifies the tenant.
	var orgID, mailboxID uuid.UUID
	err = h.svc.pool.QueryRow(r.Context(), `SELECT organization_id, id FROM mailboxes WHERE from_email=$1 OR reply_to=$1 LIMIT 1`,
		strings.ToLower(strings.TrimSpace(in.MailboxEmail))).Scan(&orgID, &mailboxID)
	if database.IsNoRows(err) {
		httpx.OK(w, Result{Reason: "unknown mailbox"})
		return
	}
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	res, err := h.svc.Process(r.Context(), orgID, &mailboxID, raw)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, res)
}
