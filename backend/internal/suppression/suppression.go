// Package suppression maintains the per-organization do-not-contact list.
// Every send path checks it, and adding an address immediately stops any
// in-progress campaign sequences to that address.
package suppression

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/httpx"
	"leadline/pkg/validate"
)

type Reason string

const (
	Unsubscribed Reason = "unsubscribed"
	HardBounce   Reason = "hard_bounce"
	Complaint    Reason = "complaint"
	Manual       Reason = "manual"
	Erased       Reason = "erased"
)

// campaignLeadStatus maps a suppression reason to the status a stopped
// campaign_lead gets.
func (r Reason) campaignLeadStatus() string {
	switch r {
	case Unsubscribed, Complaint:
		return "unsubscribed"
	case HardBounce:
		return "bounced"
	default:
		return "suppressed"
	}
}

type Entry struct {
	ID         uuid.UUID  `json:"id"`
	Email      string     `json:"email"`
	Reason     Reason     `json:"reason"`
	Source     string     `json:"source"`
	CampaignID *uuid.UUID `json:"campaign_id"`
	CreatedAt  time.Time  `json:"created_at"`
}

type Service struct {
	pool  *pgxpool.Pool
	audit *audit.Log
}

func NewService(pool *pgxpool.Pool, a *audit.Log) *Service { return &Service{pool: pool, audit: a} }

// AddInput describes one suppression.
type AddInput struct {
	OrgID      uuid.UUID
	Email      string
	Reason     Reason
	Source     string
	CampaignID *uuid.UUID
	UserID     *uuid.UUID
}

// Add suppresses an address inside tx and stops every active sequence to it.
// It is idempotent; the first reason recorded wins (except that stronger
// signals — complaint/unsubscribe — replace a manual entry).
func Add(ctx context.Context, tx database.DBTX, in AddInput) error {
	email := validate.NormalizeEmail(in.Email)
	if email == "" {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO suppression_list (organization_id, email, reason, source, campaign_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (organization_id, email) DO UPDATE
		SET reason = EXCLUDED.reason, source = EXCLUDED.source
		WHERE suppression_list.reason = 'manual' AND EXCLUDED.reason IN ('unsubscribed','complaint','erased')`,
		in.OrgID, email, in.Reason, in.Source, in.CampaignID, in.UserID); err != nil {
		return err
	}
	// Stop sequences and cancel anything not yet handed to a provider.
	if _, err := tx.Exec(ctx, `
		UPDATE campaign_leads SET status = $3, stop_reason = $4, next_send_at = NULL, updated_at = now()
		WHERE organization_id = $1 AND email = $2 AND status IN ('pending','active','sending')`,
		in.OrgID, email, in.Reason.campaignLeadStatus(), string(in.Reason)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE email_messages SET status = 'cancelled', last_error = 'suppressed: ' || $3, updated_at = now()
		WHERE organization_id = $1 AND to_email = $2 AND status = 'queued'`,
		in.OrgID, email, string(in.Reason)); err != nil {
		return err
	}
	if in.Reason == Unsubscribed || in.Reason == Complaint {
		if _, err := tx.Exec(ctx, `
			UPDATE leads l SET status = 'unsubscribed', updated_at = now()
			WHERE l.organization_id = $1 AND (l.business_email = $2 OR EXISTS (
				SELECT 1 FROM lead_contacts c WHERE c.lead_id = l.id AND c.email = $2))
			AND l.status NOT IN ('won','lost')`, in.OrgID, email); err != nil {
			return err
		}
	}
	return nil
}

// IsSuppressed checks a single address.
func IsSuppressed(ctx context.Context, db database.DBTX, orgID uuid.UUID, email string) (bool, error) {
	var ok bool
	err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM suppression_list WHERE organization_id=$1 AND email=$2)`,
		orgID, validate.NormalizeEmail(email)).Scan(&ok)
	return ok, err
}

func (s *Service) List(ctx context.Context, p auth.Principal, q string, page httpx.Page) ([]Entry, int64, error) {
	pattern := "%" + strings.ToLower(strings.TrimSpace(q)) + "%"
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM suppression_list WHERE organization_id=$1 AND email::text LIKE $2`, p.OrgID, pattern).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, email::text, reason, source, campaign_id, created_at FROM suppression_list
		WHERE organization_id=$1 AND email::text LIKE $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4`,
		p.OrgID, pattern, page.Limit, page.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Email, &e.Reason, &e.Source, &e.CampaignID, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// AddManual suppresses a list of addresses on behalf of a user.
func (s *Service) AddManual(ctx context.Context, p auth.Principal, emails []string) (int, error) {
	v := validate.Errors{}
	clean := make([]string, 0, len(emails))
	for _, e := range emails {
		e = validate.NormalizeEmail(e)
		if e == "" {
			continue
		}
		if !validate.Email(e) {
			v.Add("emails", "invalid address: "+e)
			continue
		}
		clean = append(clean, e)
	}
	v.Check(len(clean) > 0 || len(v) > 0, "emails", "at least one address is required")
	v.Check(len(clean) <= 10000, "emails", "at most 10000 addresses per request")
	if err := v.Err(); err != nil {
		return 0, err
	}
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		for _, e := range clean {
			if err := Add(ctx, tx, AddInput{OrgID: p.OrgID, Email: e, Reason: Manual, Source: "user", UserID: &p.UserID}); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "suppression.added", EntityType: "suppression", Metadata: map[string]any{"count": len(clean)}})
	}
	return len(clean), err
}

// Remove deletes a manual or bounce entry. Unsubscribes, complaints and
// erasures reflect the recipient's choice and cannot be removed by the sender.
func (s *Service) Remove(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	var reason Reason
	err := s.pool.QueryRow(ctx, `SELECT reason FROM suppression_list WHERE organization_id=$1 AND id=$2`, p.OrgID, id).Scan(&reason)
	if database.IsNoRows(err) {
		return apperr.NotFound("suppression entry")
	}
	if err != nil {
		return err
	}
	if reason != Manual && reason != HardBounce {
		return apperr.Conflict("not_removable", "entries created by an unsubscribe, complaint or erasure request cannot be removed")
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM suppression_list WHERE organization_id=$1 AND id=$2`, p.OrgID, id); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "suppression.removed", EntityType: "suppression", EntityID: id.String(), Metadata: map[string]any{"reason": reason}})
	return nil
}

// Erase handles a data-subject erasure request: it deletes contact records
// for the address, redacts it from sent-message history, and keeps only a
// suppression entry so the person is never contacted again.
func (s *Service) Erase(ctx context.Context, p auth.Principal, email string) error {
	email = validate.NormalizeEmail(email)
	if !validate.Email(email) {
		return apperr.Invalid("validation failed", map[string]string{"email": "must be a valid email address"})
	}
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := Add(ctx, tx, AddInput{OrgID: p.OrgID, Email: email, Reason: Erased, Source: "erasure_request", UserID: &p.UserID}); err != nil {
			return err
		}
		stmts := []string{
			`DELETE FROM lead_contacts WHERE organization_id=$1 AND email=$2`,
			`UPDATE leads SET business_email = NULL, updated_at = now() WHERE organization_id=$1 AND business_email=$2`,
			`UPDATE email_messages SET to_email='erased@invalid', subject='[erased]', body_text='[erased]', body_html='[erased]'
			 WHERE organization_id=$1 AND to_email=$2`,
			`UPDATE campaign_leads SET email='erased@invalid' WHERE organization_id=$1 AND email=$2`,
		}
		for _, q := range stmts {
			if _, err := tx.Exec(ctx, q, p.OrgID, email); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		// The address itself is deliberately not written to the audit log.
		s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "data.erased", EntityType: "email"})
	}
	return err
}

// ───────────────────────── HTTP ─────────────────────────

type Handler struct {
	svc *Service
	mw  *auth.Middleware
}

func NewHandler(svc *Service, mw *auth.Middleware) *Handler { return &Handler{svc: svc, mw: mw} }

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/suppressions", h.mw.Org(auth.RoleViewer, h.list))
	mux.HandleFunc("POST /api/v1/suppressions", h.mw.Org(auth.RoleMember, h.add))
	mux.HandleFunc("DELETE /api/v1/suppressions/{id}", h.mw.Org(auth.RoleAdmin, h.remove))
	mux.HandleFunc("POST /api/v1/compliance/erase", h.mw.Org(auth.RoleAdmin, h.erase))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page := httpx.PageFrom(r)
	out, total, err := h.svc.List(r.Context(), auth.MustPrincipal(r.Context()), r.URL.Query().Get("q"), page)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out, page.Meta(total))
}

func (h *Handler) add(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Emails []string `json:"emails"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	n, err := h.svc.AddManual(r.Context(), auth.MustPrincipal(r.Context()), in.Emails)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.Created(w, map[string]int{"added": n})
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Error(w, r, apperr.NotFound("suppression entry"))
		return
	}
	if err := h.svc.Remove(r.Context(), auth.MustPrincipal(r.Context()), id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) erase(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.svc.Erase(r.Context(), auth.MustPrincipal(r.Context()), in.Email); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}
