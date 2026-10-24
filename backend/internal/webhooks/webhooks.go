// Package webhooks delivers platform events to customer endpoints.
//
// Events are written to webhook_deliveries inside the same transaction as the
// change that caused them (transactional outbox), so an event is published if
// and only if the change commits. A dispatcher loop delivers pending rows with
// HMAC signatures, exponential backoff and a bounded number of attempts.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/internal/vault"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/httpx"
	"leadline/pkg/queue"
	"leadline/pkg/safehttp"
	"leadline/pkg/secure"
)

// Event types customers can subscribe to.
const (
	LeadCreated       = "lead.created"
	LeadStatusChanged = "lead.status_changed"
	EmailSent         = "email.sent"
	EmailBounced      = "email.bounced"
	EmailReplied      = "email.replied"
	EmailUnsubscribed = "email.unsubscribed"
	ReplyReceived     = "reply.received"
	CampaignCompleted = "campaign.completed"
	CampaignPaused    = "campaign.paused"
)

// Deal lifecycle events (internal/deals).
const (
	DealCreated      = "deal.created"
	DealStageChanged = "deal.stage_changed"
	DealWon          = "deal.won"
	DealLost         = "deal.lost"
)

var EventTypes = []string{LeadCreated, LeadStatusChanged, EmailSent, EmailBounced, EmailReplied, EmailUnsubscribed, ReplyReceived, CampaignCompleted, CampaignPaused,
	DealCreated, DealStageChanged, DealWon, DealLost}

const maxAttempts = 8

// Publish records an event for every active endpoint subscribed to it. Call it
// with the transaction that makes the underlying change.
func Publish(ctx context.Context, db database.DBTX, orgID uuid.UUID, eventType string, data any) error {
	payload, err := json.Marshal(map[string]any{
		"id": uuid.NewString(), "type": eventType, "created_at": time.Now().UTC().Format(time.RFC3339),
		"organization_id": orgID, "data": data,
	})
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `
		INSERT INTO webhook_deliveries (organization_id, endpoint_id, event_type, payload)
		SELECT organization_id, id, $2, $3 FROM webhook_endpoints
		WHERE organization_id = $1 AND active AND $2 = ANY(events)`, orgID, eventType, payload)
	return err
}

// Sign produces the X-Leadline-Signature header value: t=<unix>,v1=<hex hmac>.
// Receivers recompute HMAC-SHA256(secret, "<t>.<body>") and reject old timestamps.
func Sign(secret string, ts time.Time, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(m, "%d.", ts.Unix())
	m.Write(body)
	return fmt.Sprintf("t=%d,v1=%s", ts.Unix(), hex.EncodeToString(m.Sum(nil)))
}

// ───────────────────────── Dispatcher ─────────────────────────

type Dispatcher struct {
	pool   *pgxpool.Pool
	vault  *vault.Vault
	client *http.Client
	log    *slog.Logger
}

func NewDispatcher(pool *pgxpool.Pool, v *vault.Vault, log *slog.Logger) *Dispatcher {
	return &Dispatcher{pool: pool, vault: v, client: safehttp.NewClient(10 * time.Second), log: log}
}

func (d *Dispatcher) Run(ctx context.Context) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := d.DeliverDue(ctx, 50); err != nil && ctx.Err() == nil {
				d.log.Error("webhook dispatch failed", "error", err)
			} else if n == 50 {
				t.Reset(100 * time.Millisecond) // backlog: keep going
			} else {
				t.Reset(3 * time.Second)
			}
		}
	}
}

type due struct {
	id, endpointID uuid.UUID
	url            string
	secret         []byte
	eventType      string
	payload        []byte
	attempts       int
}

// DeliverDue sends up to limit pending deliveries. Rows are locked with SKIP
// LOCKED, so any number of workers can run the dispatcher concurrently.
func (d *Dispatcher) DeliverDue(ctx context.Context, limit int) (int, error) {
	var batch []due
	err := database.WithTx(ctx, d.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT w.id, w.endpoint_id, e.url, e.secret_enc, w.event_type, w.payload, w.attempts
			FROM webhook_deliveries w JOIN webhook_endpoints e ON e.id = w.endpoint_id
			WHERE w.status = 'pending' AND w.next_attempt_at <= now()
			ORDER BY w.next_attempt_at LIMIT $1 FOR UPDATE OF w SKIP LOCKED`, limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			var x due
			if err := rows.Scan(&x.id, &x.endpointID, &x.url, &x.secret, &x.eventType, &x.payload, &x.attempts); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, x)
		}
		rows.Close()
		// Lease the rows for a minute so a crash mid-send leads to a retry, not a loss.
		ids := make([]uuid.UUID, len(batch))
		for i, b := range batch {
			ids[i] = b.id
		}
		_, err = tx.Exec(ctx, `UPDATE webhook_deliveries SET next_attempt_at = now() + interval '1 minute' WHERE id = ANY($1)`, ids)
		return err
	})
	if err != nil {
		return 0, err
	}
	for _, x := range batch {
		d.deliver(ctx, x)
	}
	return len(batch), nil
}

func (d *Dispatcher) deliver(ctx context.Context, x due) {
	status, sendErr := d.send(ctx, x)
	attempts := x.attempts + 1
	if sendErr == nil {
		_, _ = d.pool.Exec(ctx, `UPDATE webhook_deliveries SET status='succeeded', attempts=$2, response_status=$3,
			delivered_at=now(), last_error=NULL WHERE id=$1`, x.id, attempts, status)
		return
	}
	final := attempts >= maxAttempts
	next := time.Now().Add(queue.Backoff(attempts))
	st := "pending"
	if final {
		st = "failed"
	}
	_, _ = d.pool.Exec(ctx, `UPDATE webhook_deliveries SET status=$2, attempts=$3, response_status=NULLIF($4,0),
		last_error=$5, next_attempt_at=$6 WHERE id=$1`, x.id, st, attempts, status, truncate(sendErr.Error(), 500), next)
}

func (d *Dispatcher) send(ctx context.Context, x due) (int, error) {
	secret, err := d.vault.Open(x.secret)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, x.url, bytes.NewReader(x.payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Leadline-Webhooks/1.0")
	req.Header.Set("X-Leadline-Event", x.eventType)
	req.Header.Set("X-Leadline-Delivery", x.id.String())
	req.Header.Set("X-Leadline-Signature", Sign(secret, time.Now(), x.payload))
	resp, err := d.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("endpoint returned HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ───────────────────────── Endpoint management ─────────────────────────

type Endpoint struct {
	ID          uuid.UUID `json:"id"`
	URL         string    `json:"url"`
	Description string    `json:"description"`
	Events      []string  `json:"events"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	// Secret is only returned once, on creation.
	Secret string `json:"secret,omitempty"`
}

type Delivery struct {
	ID             uuid.UUID       `json:"id"`
	EventType      string          `json:"event_type"`
	Status         string          `json:"status"`
	Attempts       int             `json:"attempts"`
	ResponseStatus *int            `json:"response_status"`
	LastError      *string         `json:"last_error"`
	Payload        json.RawMessage `json:"payload"`
	CreatedAt      time.Time       `json:"created_at"`
	DeliveredAt    *time.Time      `json:"delivered_at"`
}

type Service struct {
	pool      *pgxpool.Pool
	vault     *vault.Vault
	audit     *audit.Log
	allowHTTP bool
}

func NewService(pool *pgxpool.Pool, v *vault.Vault, a *audit.Log, allowHTTP bool) *Service {
	return &Service{pool: pool, vault: v, audit: a, allowHTTP: allowHTTP}
}

type Input struct {
	URL         string   `json:"url"`
	Description string   `json:"description"`
	Events      []string `json:"events"`
	Active      *bool    `json:"active"`
}

func (s *Service) validate(in *Input) error {
	errs := map[string]string{}
	u, err := url.Parse(strings.TrimSpace(in.URL))
	switch {
	case err != nil || u.Host == "":
		errs["url"] = "must be an absolute URL"
	case u.Scheme != "https" && !(s.allowHTTP && u.Scheme == "http"):
		errs["url"] = "must use https"
	}
	if len(in.Events) == 0 {
		errs["events"] = "subscribe to at least one event"
	}
	for _, e := range in.Events {
		if !slices.Contains(EventTypes, e) {
			errs["events"] = "unknown event " + e
		}
	}
	if len(errs) > 0 {
		return apperr.Invalid("validation failed", errs)
	}
	in.URL = u.String()
	return nil
}

func (s *Service) List(ctx context.Context, p auth.Principal) ([]Endpoint, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, url, description, events, active, created_at FROM webhook_endpoints WHERE organization_id=$1 ORDER BY created_at`, p.OrgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Endpoint{}
	for rows.Next() {
		var e Endpoint
		if err := rows.Scan(&e.ID, &e.URL, &e.Description, &e.Events, &e.Active, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) Create(ctx context.Context, p auth.Principal, in Input) (*Endpoint, error) {
	if err := s.validate(&in); err != nil {
		return nil, err
	}
	raw, err := secure.RandomToken(24)
	if err != nil {
		return nil, err
	}
	secret := "whsec_" + raw
	sealed, err := s.vault.Seal(secret)
	if err != nil {
		return nil, err
	}
	e := Endpoint{URL: in.URL, Description: in.Description, Events: in.Events, Active: true, Secret: secret}
	if err := s.pool.QueryRow(ctx, `INSERT INTO webhook_endpoints (organization_id, url, description, events, secret_enc)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, created_at`, p.OrgID, in.URL, in.Description, in.Events, sealed).Scan(&e.ID, &e.CreatedAt); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "webhook.created", EntityType: "webhook", EntityID: e.ID.String()})
	return &e, nil
}

func (s *Service) Update(ctx context.Context, p auth.Principal, id uuid.UUID, in Input) error {
	if err := s.validate(&in); err != nil {
		return err
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	tag, err := s.pool.Exec(ctx, `UPDATE webhook_endpoints SET url=$3, description=$4, events=$5, active=$6, updated_at=now()
		WHERE organization_id=$1 AND id=$2`, p.OrgID, id, in.URL, in.Description, in.Events, active)
	if err == nil && tag.RowsAffected() == 0 {
		return apperr.NotFound("webhook")
	}
	return err
}

func (s *Service) Delete(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM webhook_endpoints WHERE organization_id=$1 AND id=$2`, p.OrgID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return apperr.NotFound("webhook")
	}
	if err == nil {
		s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "webhook.deleted", EntityType: "webhook", EntityID: id.String()})
	}
	return err
}

// Test queues a ping event to one endpoint.
func (s *Service) Test(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	payload, _ := json.Marshal(map[string]any{"id": uuid.NewString(), "type": "ping", "created_at": time.Now().UTC(), "organization_id": p.OrgID, "data": map[string]string{"message": "Webhook test from Leadline"}})
	tag, err := s.pool.Exec(ctx, `INSERT INTO webhook_deliveries (organization_id, endpoint_id, event_type, payload)
		SELECT organization_id, id, 'ping', $3 FROM webhook_endpoints WHERE organization_id=$1 AND id=$2`, p.OrgID, id, payload)
	if err == nil && tag.RowsAffected() == 0 {
		return apperr.NotFound("webhook")
	}
	return err
}

func (s *Service) Deliveries(ctx context.Context, p auth.Principal, id uuid.UUID) ([]Delivery, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, event_type, status, attempts, response_status, last_error, payload, created_at, delivered_at
		FROM webhook_deliveries WHERE organization_id=$1 AND endpoint_id=$2 ORDER BY created_at DESC LIMIT 100`, p.OrgID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.EventType, &d.Status, &d.Attempts, &d.ResponseStatus, &d.LastError, &d.Payload, &d.CreatedAt, &d.DeliveredAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ───────────────────────── HTTP ─────────────────────────

type Handler struct {
	svc *Service
	mw  *auth.Middleware
}

func NewHandler(svc *Service, mw *auth.Middleware) *Handler { return &Handler{svc: svc, mw: mw} }

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/webhook-endpoints", h.mw.Org(auth.RoleAdmin, h.list))
	mux.HandleFunc("POST /api/v1/webhook-endpoints", h.mw.Org(auth.RoleAdmin, h.create))
	mux.HandleFunc("PUT /api/v1/webhook-endpoints/{id}", h.mw.Org(auth.RoleAdmin, h.update))
	mux.HandleFunc("DELETE /api/v1/webhook-endpoints/{id}", h.mw.Org(auth.RoleAdmin, h.delete))
	mux.HandleFunc("POST /api/v1/webhook-endpoints/{id}/test", h.mw.Org(auth.RoleAdmin, h.test))
	mux.HandleFunc("GET /api/v1/webhook-endpoints/{id}/deliveries", h.mw.Org(auth.RoleAdmin, h.deliveries))
}

func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, apperr.NotFound("webhook")
	}
	return id, nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.List(r.Context(), auth.MustPrincipal(r.Context()))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out, map[string]any{"event_types": EventTypes})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	e, err := h.svc.Create(r.Context(), auth.MustPrincipal(r.Context()), in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.Created(w, e)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.svc.Update(r.Context(), auth.MustPrincipal(r.Context()), id, in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.svc.Delete(r.Context(), auth.MustPrincipal(r.Context()), id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) test(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.svc.Test(r.Context(), auth.MustPrincipal(r.Context()), id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]string{"status": "queued"}, nil)
}

func (h *Handler) deliveries(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.Deliveries(r.Context(), auth.MustPrincipal(r.Context()), id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}
