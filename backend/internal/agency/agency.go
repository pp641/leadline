// Package agency implements multi-client mode: an agency organization owns
// client organizations. Agency members get access to every client (agency
// owners act as client admins) without separate memberships, while each
// client's data stays fully isolated in its own tenant.
package agency

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
	"leadline/internal/organization"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/httpx"
	"leadline/pkg/secure"
)

type Client struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	Leads           int64     `json:"leads"`
	ActiveCampaigns int64     `json:"active_campaigns"`
	Sent30d         int64     `json:"sent_30d"`
	Replies30d      int64     `json:"replies_30d"`
	Bounced30d      int64     `json:"bounced_30d"`
	CreatedAt       time.Time `json:"created_at"`
}

type Service struct {
	pool  *pgxpool.Pool
	audit *audit.Log
}

func NewService(pool *pgxpool.Pool, a *audit.Log) *Service { return &Service{pool: pool, audit: a} }

// SetAgency turns agency mode on or off for the caller's organization.
func (s *Service) SetAgency(ctx context.Context, p auth.Principal, on bool) error {
	var parent *uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT parent_id FROM organizations WHERE id=$1`, p.OrgID).Scan(&parent); err != nil {
		return err
	}
	if parent != nil && on {
		return apperr.Conflict("is_client", "a client organization cannot itself be an agency")
	}
	if !on {
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM organizations WHERE parent_id=$1`, p.OrgID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return apperr.Conflict("has_clients", "remove or detach client organizations before turning agency mode off")
		}
	}
	_, err := s.pool.Exec(ctx, `UPDATE organizations SET is_agency=$2, updated_at=now() WHERE id=$1`, p.OrgID, on)
	if err == nil {
		s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "organization.agency_mode", Metadata: map[string]any{"enabled": on}})
	}
	return err
}

type ClientInput struct {
	Name            string `json:"name"`
	LegalName       string `json:"legal_name"`
	PhysicalAddress string `json:"physical_address"`
	DailySendLimit  *int   `json:"daily_send_limit"`
}

// CreateClient creates a client organization under the agency.
func (s *Service) CreateClient(ctx context.Context, p auth.Principal, in ClientInput) (*organization.Organization, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 120 {
		return nil, apperr.Invalid("validation failed", map[string]string{"name": "is required"})
	}
	var isAgency bool
	if err := s.pool.QueryRow(ctx, `SELECT is_agency FROM organizations WHERE id=$1`, p.OrgID).Scan(&isAgency); err != nil {
		return nil, err
	}
	if !isAgency {
		return nil, apperr.Conflict("not_agency", "turn on agency mode first")
	}
	limit := 500
	if in.DailySendLimit != nil {
		limit = *in.DailySendLimit
	}
	suffix, _ := secure.RandomToken(4)
	slug := strings.Trim(strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, strings.ToLower(in.Name)), "-")
	if len(slug) > 40 {
		slug = slug[:40]
	}
	slug += "-" + strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(suffix))
	var id uuid.UUID
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO organizations (name, slug, legal_name, physical_address, daily_send_limit, parent_id)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, in.Name, slug, strings.TrimSpace(in.LegalName),
			strings.TrimSpace(in.PhysicalAddress), limit, p.OrgID).Scan(&id)
	})
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "agency.client_created", EntityType: "organization", EntityID: id.String()})
	o, err := organization.NewRepository(s.pool).Get(ctx, id)
	if err != nil {
		return nil, err
	}
	o.Role = auth.RoleAdmin
	return o, nil
}

// Clients lists client organizations with a 30-day performance rollup.
func (s *Service) Clients(ctx context.Context, p auth.Principal) ([]Client, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.name, o.created_at,
		  (SELECT count(*) FROM leads WHERE organization_id=o.id),
		  (SELECT count(*) FROM campaigns WHERE organization_id=o.id AND status IN ('running','scheduled')),
		  (SELECT count(*) FROM email_messages WHERE organization_id=o.id AND status='sent' AND sent_at > now()-interval '30 days'),
		  (SELECT count(DISTINCT lead_id) FROM email_events WHERE organization_id=o.id AND event_type='replied' AND occurred_at > now()-interval '30 days'),
		  (SELECT count(DISTINCT email_message_id) FROM email_events WHERE organization_id=o.id AND event_type='bounced' AND occurred_at > now()-interval '30 days')
		FROM organizations o WHERE o.parent_id=$1 ORDER BY o.name`, p.OrgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Client{}
	for rows.Next() {
		var c Client
		if err := rows.Scan(&c.ID, &c.Name, &c.CreatedAt, &c.Leads, &c.ActiveCampaigns, &c.Sent30d, &c.Replies30d, &c.Bounced30d); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteClient permanently deletes a client organization and all its data.
func (s *Service) DeleteClient(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	if p.Role != auth.RoleOwner {
		return apperr.Forbidden("only agency owners can delete clients")
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1 AND parent_id=$2`, id, p.OrgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("client")
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "agency.client_deleted", EntityType: "organization", EntityID: id.String()})
	return nil
}

type Handler struct {
	svc *Service
	mw  *auth.Middleware
}

func NewHandler(svc *Service, mw *auth.Middleware) *Handler { return &Handler{svc: svc, mw: mw} }

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/v1/agency", h.mw.Org(auth.RoleOwner, h.setAgency))
	mux.HandleFunc("GET /api/v1/agency/clients", h.mw.Org(auth.RoleViewer, h.clients))
	mux.HandleFunc("POST /api/v1/agency/clients", h.mw.Org(auth.RoleAdmin, h.createClient))
	mux.HandleFunc("DELETE /api/v1/agency/clients/{id}", h.mw.Org(auth.RoleOwner, h.deleteClient))
}

func (h *Handler) setAgency(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.svc.SetAgency(r.Context(), auth.MustPrincipal(r.Context()), in.Enabled); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) clients(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Clients(r.Context(), auth.MustPrincipal(r.Context()))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) createClient(w http.ResponseWriter, r *http.Request) {
	var in ClientInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	o, err := h.svc.CreateClient(r.Context(), auth.MustPrincipal(r.Context()), in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.Created(w, o)
}

func (h *Handler) deleteClient(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Error(w, r, apperr.NotFound("client"))
		return
	}
	if err := h.svc.DeleteClient(r.Context(), auth.MustPrincipal(r.Context()), id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}
