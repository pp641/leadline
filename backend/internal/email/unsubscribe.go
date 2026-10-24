package email

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/audit"
	"leadline/internal/suppression"
	"leadline/internal/webhooks"
	"leadline/pkg/database"
	"leadline/pkg/httpx"
	"leadline/pkg/logger"
	"leadline/pkg/secure"
)

// UnsubscribeClaims identify who is unsubscribing. Tokens are HMAC-signed and
// intentionally do not expire: unsubscribe links must keep working.
type UnsubscribeClaims struct {
	OrgID      uuid.UUID `json:"o"`
	Email      string    `json:"e"`
	CampaignID uuid.UUID `json:"c"`
	MessageID  uuid.UUID `json:"m"`
}

type Unsubscriber struct {
	pool   *pgxpool.Pool
	secret []byte
	appURL string
	audit  *audit.Log
}

func NewUnsubscriber(pool *pgxpool.Pool, secret []byte, appURL string, a *audit.Log) *Unsubscriber {
	return &Unsubscriber{pool: pool, secret: secret, appURL: appURL, audit: a}
}

func (u *Unsubscriber) Token(c UnsubscribeClaims) string {
	raw, _ := json.Marshal(c)
	return secure.Sign(u.secret, raw)
}

// URL is the public one-click unsubscribe link placed in emails.
func (u *Unsubscriber) URL(c UnsubscribeClaims) string {
	return u.appURL + "/api/v1/public/unsubscribe/" + u.Token(c)
}

func (u *Unsubscriber) Parse(token string) (*UnsubscribeClaims, error) {
	raw, err := secure.Verify(u.secret, token)
	if err != nil {
		return nil, err
	}
	var c UnsubscribeClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Unsubscribe suppresses the address and records the event. Idempotent.
func (u *Unsubscriber) Unsubscribe(ctx context.Context, c *UnsubscribeClaims, source string) error {
	return database.WithTx(ctx, u.pool, func(tx pgx.Tx) error {
		campaignID := &c.CampaignID
		if c.CampaignID == uuid.Nil {
			campaignID = nil
		}
		if err := suppression.Add(ctx, tx, suppression.AddInput{
			OrgID: c.OrgID, Email: c.Email, Reason: suppression.Unsubscribed, Source: source, CampaignID: campaignID,
		}); err != nil {
			return err
		}
		// Record one unsubscribe event per message, linked to the lead.
		_, err := tx.Exec(ctx, `
			INSERT INTO email_events (organization_id, campaign_id, lead_id, email_message_id, message_id, event_type, provider_event_id, metadata)
			SELECT m.organization_id, m.campaign_id, m.lead_id, m.id, m.message_id, 'unsubscribed', 'unsub:' || m.id::text,
			       jsonb_build_object('source', $3::text)
			FROM email_messages m WHERE m.id = $1 AND m.organization_id = $2
			ON CONFLICT DO NOTHING`, c.MessageID, c.OrgID, source)
		if err != nil {
			return err
		}
		if err := webhooks.Publish(ctx, tx, c.OrgID, webhooks.EmailUnsubscribed, map[string]any{"campaign_id": c.CampaignID, "email": c.Email, "source": source}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO lead_events (organization_id, lead_id, event_type, data)
			SELECT m.organization_id, m.lead_id, 'unsubscribed', jsonb_build_object('campaign_id', m.campaign_id)
			FROM email_messages m WHERE m.id = $1 AND m.organization_id = $2`, c.MessageID, c.OrgID)
		return err
	})
}

var unsubPage = template.Must(template.New("u").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Unsubscribe</title>
<style>body{font-family:-apple-system,Segoe UI,Arial,sans-serif;background:#f6f7f9;color:#1f2328;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0;padding:16px}
.card{background:#fff;border:1px solid #e3e5e8;border-radius:12px;padding:32px;max-width:420px;width:100%;text-align:center}
button{background:#1f2328;color:#fff;border:0;border-radius:8px;padding:10px 20px;font-size:15px;cursor:pointer}
p{color:#57606a;line-height:1.5}</style></head>
<body><div class="card">
{{if .Done}}<h1>You're unsubscribed</h1><p>{{.Email}} will not receive further emails from this sender.</p>
{{else if .Invalid}}<h1>Link not valid</h1><p>This unsubscribe link is invalid. Reply to the email asking to be removed and the sender is required to honour it.</p>
{{else}}<h1>Unsubscribe</h1><p>Stop receiving emails from this sender at <strong>{{.Email}}</strong>?</p>
<form method="post"><button type="submit">Unsubscribe</button></form>{{end}}
</div></body></html>`))

// Register mounts the public (unauthenticated) unsubscribe endpoints.
func (u *Unsubscriber) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/public/unsubscribe/{token}", u.page)
	mux.HandleFunc("POST /api/v1/public/unsubscribe/{token}", u.post)
}

func (u *Unsubscriber) page(w http.ResponseWriter, r *http.Request) {
	c, err := u.Parse(r.PathValue("token"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = unsubPage.Execute(w, map[string]any{"Invalid": true})
		return
	}
	_ = unsubPage.Execute(w, map[string]any{"Email": c.Email})
}

// post handles both the confirmation form and RFC 8058 one-click requests
// from mailbox providers (body "List-Unsubscribe=One-Click").
func (u *Unsubscriber) post(w http.ResponseWriter, r *http.Request) {
	c, err := u.Parse(r.PathValue("token"))
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_ = unsubPage.Execute(w, map[string]any{"Invalid": true})
		return
	}
	_ = r.ParseForm()
	source := "link"
	if r.PostForm.Get("List-Unsubscribe") == "One-Click" {
		source = "one_click"
	}
	if err := u.Unsubscribe(r.Context(), c, source); err != nil {
		logger.From(r.Context()).Error("unsubscribe failed", "error", err)
		httpx.Error(w, r, err)
		return
	}
	u.audit.Record(r.Context(), audit.Entry{OrgID: c.OrgID, Action: "recipient.unsubscribed", EntityType: "campaign", EntityID: c.CampaignID.String()})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = unsubPage.Execute(w, map[string]any{"Done": true, "Email": c.Email})
}
