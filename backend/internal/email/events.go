package email

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/auth"
	"leadline/internal/suppression"
	"leadline/internal/webhooks"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/httpx"
	"leadline/pkg/logger"
	"leadline/pkg/metrics"
	"leadline/pkg/secure"
)

// EventTypes that can be ingested from providers or recorded manually.
var EventTypes = []string{"delivered", "bounced", "complained", "unsubscribed", "replied"}

// ReplyClassifications mirror the Phase 2 AI classifier labels so manual
// tagging today and automatic tagging later share one vocabulary.
var ReplyClassifications = []string{
	"positive", "interested", "question", "not_now", "not_interested", "unsubscribe",
	"wrong_person", "out_of_office", "spam", "unknown",
}

// PositiveClassifications count as positive replies in analytics.
var PositiveClassifications = []string{"positive", "interested"}

// InboundEvent is a provider-neutral delivery event.
type InboundEvent struct {
	MessageID         string         `json:"message_id"`          // our RFC Message-ID
	ProviderMessageID string         `json:"provider_message_id"` // alternatively, the provider's ID
	EventType         string         `json:"event_type"`
	ProviderEventID   string         `json:"provider_event_id"`
	OccurredAt        *time.Time     `json:"occurred_at"`
	Metadata          map[string]any `json:"metadata"`
}

// WebhookParser turns a provider's webhook request into InboundEvents. Each
// provider (SES/SNS, SendGrid, Mailgun, Postmark) gets one implementation.
type WebhookParser interface {
	Parse(r *http.Request, body []byte) ([]InboundEvent, error)
}

type Events struct {
	pool    *pgxpool.Pool
	parsers map[string]WebhookParser
	// OnReply is called after a reply event commits (set to the workflow
	// engine). eventID identifies the email_events row for deduplication.
	OnReply func(ctx context.Context, orgID, leadID, campaignID uuid.UUID, classification string, eventID uuid.UUID)
}

func NewEvents(pool *pgxpool.Pool, webhookSecret string) *Events {
	return &Events{pool: pool, parsers: map[string]WebhookParser{
		"generic": GenericParser{Secret: []byte(webhookSecret)},
	}}
}

type msgRef struct {
	ID, OrgID, CampaignID, CampaignLeadID, LeadID uuid.UUID
	ToEmail, MessageID                            string
}

func (e *Events) findMessage(ctx context.Context, db database.DBTX, ev InboundEvent) (*msgRef, error) {
	var m msgRef
	err := db.QueryRow(ctx, `
		SELECT id, organization_id, campaign_id, campaign_lead_id, lead_id, to_email::text, message_id
		FROM email_messages
		WHERE ($1 <> '' AND message_id = $1) OR ($2 <> '' AND provider_message_id = $2)
		LIMIT 1`, ev.MessageID, ev.ProviderMessageID).
		Scan(&m.ID, &m.OrgID, &m.CampaignID, &m.CampaignLeadID, &m.LeadID, &m.ToEmail, &m.MessageID)
	if database.IsNoRows(err) {
		return nil, apperr.NotFound("message")
	}
	return &m, err
}

// Ingest records an event and applies its consequences atomically:
// hard bounces and complaints suppress the address, replies stop the sequence.
// Duplicate deliveries of the same provider event are ignored.
func (e *Events) Ingest(ctx context.Context, ev InboundEvent) error {
	if !slices.Contains(EventTypes, ev.EventType) {
		return apperr.Invalid("unsupported event_type "+ev.EventType, nil)
	}
	if ev.Metadata == nil {
		ev.Metadata = map[string]any{}
	}
	occurred := time.Now()
	if ev.OccurredAt != nil {
		occurred = *ev.OccurredAt
	}
	var applied *msgRef
	var eventID uuid.UUID
	err := database.WithTx(ctx, e.pool, func(tx pgx.Tx) error {
		m, err := e.findMessage(ctx, tx, ev)
		if err != nil {
			return err
		}
		var providerEventID *string
		if ev.ProviderEventID != "" {
			providerEventID = &ev.ProviderEventID
		}
		meta, _ := json.Marshal(ev.Metadata)
		err = tx.QueryRow(ctx, `
			INSERT INTO email_events (organization_id, campaign_id, lead_id, email_message_id, message_id, event_type,
				provider_event_id, metadata, occurred_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING RETURNING id`,
			m.OrgID, m.CampaignID, m.LeadID, m.ID, m.MessageID, ev.EventType, providerEventID, meta, occurred).Scan(&eventID)
		if database.IsNoRows(err) {
			return nil // duplicate webhook delivery
		}
		if err != nil {
			return err
		}
		applied = m
		return e.apply(ctx, tx, m, ev)
	})
	if err == nil && applied != nil && ev.EventType == "replied" && e.OnReply != nil {
		class, _ := ev.Metadata["classification"].(string)
		e.OnReply(ctx, applied.OrgID, applied.LeadID, applied.CampaignID, class, eventID)
	}
	return err
}

func (e *Events) apply(ctx context.Context, tx pgx.Tx, m *msgRef, ev InboundEvent) error {
	hook := map[string]string{"bounced": webhooks.EmailBounced, "replied": webhooks.EmailReplied,
		"unsubscribed": webhooks.EmailUnsubscribed, "complained": webhooks.EmailUnsubscribed}[ev.EventType]
	if hook != "" {
		if err := webhooks.Publish(ctx, tx, m.OrgID, hook, map[string]any{
			"campaign_id": m.CampaignID, "lead_id": m.LeadID, "message_id": m.MessageID, "event_type": ev.EventType, "metadata": ev.Metadata,
		}); err != nil {
			return err
		}
	}
	add := func(reason suppression.Reason) error {
		return suppression.Add(ctx, tx, suppression.AddInput{
			OrgID: m.OrgID, Email: m.ToEmail, Reason: reason, Source: "provider_event", CampaignID: &m.CampaignID,
		})
	}
	leadEvent := func(t string, data map[string]any) error {
		raw, _ := json.Marshal(data)
		_, err := tx.Exec(ctx, `INSERT INTO lead_events (organization_id, lead_id, event_type, data) VALUES ($1,$2,$3,$4)`, m.OrgID, m.LeadID, t, raw)
		return err
	}
	switch ev.EventType {
	case "bounced":
		metrics.Bounces.Inc()
		bounceType, _ := ev.Metadata["bounce_type"].(string)
		if bounceType == "soft" || bounceType == "transient" {
			return leadEvent("email_soft_bounced", map[string]any{"campaign_id": m.CampaignID})
		}
		if err := add(suppression.HardBounce); err != nil {
			return err
		}
		return leadEvent("email_bounced", map[string]any{"campaign_id": m.CampaignID})
	case "complained":
		if err := add(suppression.Complaint); err != nil {
			return err
		}
		return leadEvent("spam_complaint", map[string]any{"campaign_id": m.CampaignID})
	case "unsubscribed":
		if err := add(suppression.Unsubscribed); err != nil {
			return err
		}
		return leadEvent("unsubscribed", map[string]any{"campaign_id": m.CampaignID})
	case "replied":
		metrics.Replies.Inc()
		class, _ := ev.Metadata["classification"].(string)
		// Never keep emailing someone who opted out or is not interested.
		switch class {
		case "unsubscribe", "spam":
			if err := add(suppression.Unsubscribed); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE campaign_leads cl SET status='replied', stop_reason='replied', next_send_at=NULL, updated_at=now()
			FROM campaigns c
			WHERE cl.id = $1 AND c.id = cl.campaign_id
			  AND cl.status IN ('pending','active','sending','completed')
			  AND (c.stop_on_reply OR $2 IN ('not_interested','unsubscribe','spam'))`, m.CampaignLeadID, class); err != nil {
			return err
		}
		newStatus := "replied"
		if slices.Contains(PositiveClassifications, class) {
			newStatus = "interested"
		}
		if _, err := tx.Exec(ctx, `UPDATE leads SET status=$3, updated_at=now() WHERE organization_id=$1 AND id=$2 AND status IN ('new','contacted','replied')`,
			m.OrgID, m.LeadID, newStatus); err != nil {
			return err
		}
		return leadEvent("replied", map[string]any{"campaign_id": m.CampaignID, "classification": class})
	}
	return nil
}

// GenericParser accepts our own JSON format, authenticated with an HMAC-SHA256
// of the raw body in the X-Webhook-Signature header (hex).
//
//	{"events":[{"message_id":"…","event_type":"bounced","provider_event_id":"…","metadata":{"bounce_type":"hard"}}]}
type GenericParser struct{ Secret []byte }

func (g GenericParser) Parse(r *http.Request, body []byte) ([]InboundEvent, error) {
	if len(g.Secret) == 0 {
		return nil, apperr.Forbidden("webhooks are disabled: WEBHOOK_SECRET is not set")
	}
	want := secure.HMACHex(g.Secret, body)
	if subtle.ConstantTimeCompare([]byte(want), []byte(r.Header.Get("X-Webhook-Signature"))) != 1 {
		return nil, apperr.Unauthorized("invalid webhook signature")
	}
	var payload struct {
		Events []InboundEvent `json:"events"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, apperr.Invalid("invalid JSON", nil)
	}
	return payload.Events, nil
}

// ───────────────────────── HTTP ─────────────────────────

type EventsHandler struct {
	events *Events
	mw     *auth.Middleware
}

func NewEventsHandler(e *Events, mw *auth.Middleware) *EventsHandler {
	return &EventsHandler{events: e, mw: mw}
}

func (h *EventsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/webhooks/email/{provider}", h.webhook)
	mux.HandleFunc("GET /api/v1/email-events", h.mw.Org(auth.RoleViewer, h.list))
	mux.HandleFunc("POST /api/v1/email-events", h.mw.Org(auth.RoleMember, h.record))
}

func (h *EventsHandler) webhook(w http.ResponseWriter, r *http.Request) {
	parser, ok := h.events.parsers[r.PathValue("provider")]
	if !ok {
		httpx.Error(w, r, apperr.NotFound("webhook provider"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	evs, err := parser.Parse(r, body)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	accepted := 0
	for _, ev := range evs {
		if err := h.events.Ingest(r.Context(), ev); err != nil {
			// Unknown messages are acknowledged so providers stop retrying.
			logger.From(r.Context()).Warn("webhook event skipped", "event_type", ev.EventType, "error", err)
			continue
		}
		accepted++
	}
	httpx.OK(w, map[string]int{"received": len(evs), "accepted": accepted})
}

// record lets users log a reply (or other event) by hand — e.g. a reply that
// arrived in their own inbox — until inbound processing lands in Phase 2.
func (h *EventsHandler) record(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	var in struct {
		CampaignLeadID uuid.UUID      `json:"campaign_lead_id"`
		EventType      string         `json:"event_type"`
		Classification string         `json:"classification"`
		Note           string         `json:"note"`
		Metadata       map[string]any `json:"metadata"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if in.Classification != "" && !slices.Contains(ReplyClassifications, in.Classification) {
		httpx.Error(w, r, apperr.Invalid("validation failed", map[string]string{"classification": "invalid classification"}))
		return
	}
	// Resolve the latest message to this campaign lead, scoped to the caller's org.
	var msgID string
	err := h.events.pool.QueryRow(r.Context(), `
		SELECT m.message_id FROM email_messages m
		WHERE m.organization_id=$1 AND m.campaign_lead_id=$2 AND m.status='sent'
		ORDER BY m.step_number DESC LIMIT 1`, p.OrgID, in.CampaignLeadID).Scan(&msgID)
	if database.IsNoRows(err) {
		httpx.Error(w, r, apperr.Invalid("no sent email found for this campaign lead", nil))
		return
	}
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	meta := in.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	meta["source"] = "manual"
	meta["recorded_by"] = p.UserID.String()
	if in.Classification != "" {
		meta["classification"] = in.Classification
	}
	if in.Note != "" {
		meta["note"] = in.Note
	}
	ev := InboundEvent{MessageID: msgID, EventType: in.EventType, Metadata: meta}
	if err := h.events.Ingest(r.Context(), ev); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.Created(w, map[string]string{"status": "recorded"})
}

type EventRecord struct {
	ID          uuid.UUID       `json:"id"`
	CampaignID  *uuid.UUID      `json:"campaign_id"`
	LeadID      *uuid.UUID      `json:"lead_id"`
	MessageID   string          `json:"message_id"`
	EventType   string          `json:"event_type"`
	Metadata    json.RawMessage `json:"metadata"`
	OccurredAt  time.Time       `json:"occurred_at"`
	CompanyName *string         `json:"company_name"`
}

func (h *EventsHandler) list(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	page := httpx.PageFrom(r)
	q := r.URL.Query()
	var campaignID, leadID *uuid.UUID
	if v, err := uuid.Parse(q.Get("campaign_id")); err == nil {
		campaignID = &v
	}
	if v, err := uuid.Parse(q.Get("lead_id")); err == nil {
		leadID = &v
	}
	types := httpx.QueryList(r, "event_type")
	if types == nil {
		types = []string{} // nil would encode as NULL and cardinality(NULL) is NULL
	}
	rows, err := h.events.pool.Query(r.Context(), `
		SELECT e.id, e.campaign_id, e.lead_id, e.message_id, e.event_type, e.metadata, e.occurred_at, l.company_name
		FROM email_events e LEFT JOIN leads l ON l.id = e.lead_id
		WHERE e.organization_id=$1
		  AND ($2::uuid IS NULL OR e.campaign_id=$2)
		  AND ($3::uuid IS NULL OR e.lead_id=$3)
		  AND (cardinality($4::text[]) = 0 OR e.event_type = ANY($4))
		ORDER BY e.occurred_at DESC LIMIT $5 OFFSET $6`,
		p.OrgID, campaignID, leadID, types, page.Limit, page.Offset)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	defer rows.Close()
	out := []EventRecord{}
	for rows.Next() {
		var e EventRecord
		if err := rows.Scan(&e.ID, &e.CampaignID, &e.LeadID, &e.MessageID, &e.EventType, &e.Metadata, &e.OccurredAt, &e.CompanyName); err != nil {
			httpx.Error(w, r, err)
			return
		}
		out = append(out, e)
	}
	httpx.OK(w, out)
}
