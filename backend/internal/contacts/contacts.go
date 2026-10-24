// Package contacts manages the people at a lead's company. Each contact keeps
// its source and verification status; a public address is never assumed to
// be valid for outreach.
package contacts

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/auth"
	"leadline/internal/jobs"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/httpx"
	"leadline/pkg/validate"
)

type Contact struct {
	ID                  uuid.UUID       `json:"id"`
	LeadID              uuid.UUID       `json:"lead_id"`
	FirstName           string          `json:"first_name"`
	LastName            string          `json:"last_name"`
	JobTitle            string          `json:"job_title"`
	Email               *string         `json:"email"`
	EmailStatus         string          `json:"email_status"`
	EmailVerifiedAt     *time.Time      `json:"email_verified_at"`
	VerificationDetails json.RawMessage `json:"verification_details"`
	LinkedInURL         string          `json:"linkedin_url"`
	Source              string          `json:"source"`
	Confidence          float32         `json:"confidence"`
	IsPrimary           bool            `json:"is_primary"`
	Suppressed          bool            `json:"suppressed"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type Input struct {
	FirstName   string  `json:"first_name"`
	LastName    string  `json:"last_name"`
	JobTitle    string  `json:"job_title"`
	Email       string  `json:"email"`
	LinkedInURL string  `json:"linkedin_url"`
	Source      string  `json:"source"`
	Confidence  float32 `json:"confidence"`
	IsPrimary   bool    `json:"is_primary"`
}

// Prepare trims and validates contact input in place.
func (in *Input) Prepare() error {
	in.FirstName = strings.TrimSpace(in.FirstName)
	in.LastName = strings.TrimSpace(in.LastName)
	in.JobTitle = strings.TrimSpace(in.JobTitle)
	in.Email = validate.NormalizeEmail(in.Email)
	in.LinkedInURL = strings.TrimSpace(in.LinkedInURL)
	if in.Source == "" {
		in.Source = "manual"
	}
	if in.Confidence == 0 {
		in.Confidence = 1
	}
	v := validate.Errors{}
	v.Check(in.Email != "" || in.FirstName != "" || in.LastName != "", "email", "a contact needs a name or an email")
	if in.Email != "" {
		v.Check(validate.Email(in.Email), "email", "must be a valid email address")
	}
	v.MaxLen("first_name", in.FirstName, 100)
	v.MaxLen("last_name", in.LastName, 100)
	v.MaxLen("job_title", in.JobTitle, 150)
	v.Check(in.Confidence >= 0 && in.Confidence <= 1, "confidence", "must be between 0 and 1")
	return v.Err()
}

const cols = `c.id, c.lead_id, c.first_name, c.last_name, c.job_title, c.email::text, c.email_status, c.email_verified_at,
	c.verification_details, c.linkedin_url, c.source, c.confidence, c.is_primary, c.created_at, c.updated_at,
	EXISTS (SELECT 1 FROM suppression_list s WHERE s.organization_id = c.organization_id AND s.email = c.email)`

func scan(row pgx.Row) (*Contact, error) {
	var c Contact
	err := row.Scan(&c.ID, &c.LeadID, &c.FirstName, &c.LastName, &c.JobTitle, &c.Email, &c.EmailStatus, &c.EmailVerifiedAt,
		&c.VerificationDetails, &c.LinkedInURL, &c.Source, &c.Confidence, &c.IsPrimary, &c.CreatedAt, &c.UpdatedAt, &c.Suppressed)
	if database.IsNoRows(err) {
		return nil, apperr.NotFound("contact")
	}
	return &c, err
}

// Insert creates a contact for a lead (the caller guarantees the lead belongs to orgID).
func Insert(ctx context.Context, db database.DBTX, orgID, leadID uuid.UUID, in *Input) (uuid.UUID, error) {
	var email *string
	if in.Email != "" {
		email = &in.Email
	}
	var id uuid.UUID
	err := db.QueryRow(ctx, `
		INSERT INTO lead_contacts (organization_id, lead_id, first_name, last_name, job_title, email, linkedin_url, source, confidence, is_primary)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,
			$10 OR NOT EXISTS (SELECT 1 FROM lead_contacts WHERE lead_id = $2))
		RETURNING id`,
		orgID, leadID, in.FirstName, in.LastName, in.JobTitle, email, in.LinkedInURL, in.Source, in.Confidence, in.IsPrimary).Scan(&id)
	if err != nil {
		return uuid.Nil, err
	}
	if in.IsPrimary {
		if _, err := db.Exec(ctx, `UPDATE lead_contacts SET is_primary = false WHERE lead_id=$1 AND id<>$2`, leadID, id); err != nil {
			return uuid.Nil, err
		}
	}
	return id, nil
}

type Service struct {
	pool *pgxpool.Pool
	q    jobs.Enqueuer
}

func NewService(pool *pgxpool.Pool, q jobs.Enqueuer) *Service { return &Service{pool: pool, q: q} }

func (s *Service) ListForLead(ctx context.Context, orgID, leadID uuid.UUID) ([]Contact, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM lead_contacts c WHERE c.organization_id=$1 AND c.lead_id=$2
		ORDER BY c.is_primary DESC, c.created_at`, orgID, leadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Contact{}
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, orgID, id uuid.UUID) (*Contact, error) {
	return scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM lead_contacts c WHERE c.organization_id=$1 AND c.id=$2`, orgID, id))
}

func (s *Service) Create(ctx context.Context, p auth.Principal, leadID uuid.UUID, in Input) (*Contact, error) {
	if err := in.Prepare(); err != nil {
		return nil, err
	}
	var id uuid.UUID
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM leads WHERE organization_id=$1 AND id=$2)`, p.OrgID, leadID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return apperr.NotFound("lead")
		}
		var err error
		if id, err = Insert(ctx, tx, p.OrgID, leadID, &in); err != nil {
			return err
		}
		return recordEvent(ctx, tx, p, leadID, "contact_added", map[string]any{"contact_id": id, "name": strings.TrimSpace(in.FirstName + " " + in.LastName)})
	})
	if err != nil {
		return nil, err
	}
	if in.Email != "" {
		if err := jobs.EnqueueVerify(ctx, s.q, p.OrgID, []uuid.UUID{id}); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, p.OrgID, id)
}

func (s *Service) Update(ctx context.Context, p auth.Principal, id uuid.UUID, in Input) (*Contact, error) {
	if err := in.Prepare(); err != nil {
		return nil, err
	}
	cur, err := s.Get(ctx, p.OrgID, id)
	if err != nil {
		return nil, err
	}
	emailChanged := deref(cur.Email) != in.Email
	var email *string
	if in.Email != "" {
		email = &in.Email
	}
	err = database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE lead_contacts SET first_name=$3, last_name=$4, job_title=$5, email=$6, linkedin_url=$7, source=$8,
				confidence=$9, is_primary=$10,
				email_status = CASE WHEN $11 THEN 'unverified' ELSE email_status END,
				email_verified_at = CASE WHEN $11 THEN NULL ELSE email_verified_at END,
				updated_at=now()
			WHERE organization_id=$1 AND id=$2`,
			p.OrgID, id, in.FirstName, in.LastName, in.JobTitle, email, in.LinkedInURL, in.Source, in.Confidence, in.IsPrimary, emailChanged); err != nil {
			return err
		}
		if in.IsPrimary {
			if _, err := tx.Exec(ctx, `UPDATE lead_contacts SET is_primary=false WHERE lead_id=$1 AND id<>$2`, cur.LeadID, id); err != nil {
				return err
			}
		}
		return recordEvent(ctx, tx, p, cur.LeadID, "contact_updated", map[string]any{"contact_id": id})
	})
	if err != nil {
		return nil, err
	}
	if emailChanged && in.Email != "" {
		if err := jobs.EnqueueVerify(ctx, s.q, p.OrgID, []uuid.UUID{id}); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, p.OrgID, id)
}

func (s *Service) Delete(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	cur, err := s.Get(ctx, p.OrgID, id)
	if err != nil {
		return err
	}
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM lead_contacts WHERE organization_id=$1 AND id=$2`, p.OrgID, id); err != nil {
			return err
		}
		return recordEvent(ctx, tx, p, cur.LeadID, "contact_deleted", map[string]any{"contact_id": id})
	})
}

func recordEvent(ctx context.Context, db database.DBTX, p auth.Principal, leadID uuid.UUID, t string, data map[string]any) error {
	raw, _ := json.Marshal(data)
	_, err := db.Exec(ctx, `INSERT INTO lead_events (organization_id, lead_id, event_type, actor_user_id, data) VALUES ($1,$2,$3,$4,$5)`,
		p.OrgID, leadID, t, p.UserID, raw)
	return err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ───────────────────────── HTTP ─────────────────────────

type Handler struct {
	svc *Service
	mw  *auth.Middleware
}

func NewHandler(svc *Service, mw *auth.Middleware) *Handler { return &Handler{svc: svc, mw: mw} }

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/leads/{id}/contacts", h.mw.Org(auth.RoleViewer, h.list))
	mux.HandleFunc("POST /api/v1/leads/{id}/contacts", h.mw.Org(auth.RoleMember, h.create))
	mux.HandleFunc("GET /api/v1/contacts/{id}", h.mw.Org(auth.RoleViewer, h.get))
	mux.HandleFunc("PUT /api/v1/contacts/{id}", h.mw.Org(auth.RoleMember, h.update))
	mux.HandleFunc("DELETE /api/v1/contacts/{id}", h.mw.Org(auth.RoleMember, h.delete))
}

func pathID(r *http.Request, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, apperr.NotFound(what)
	}
	return id, nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "lead")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.ListForLead(r.Context(), auth.MustPrincipal(r.Context()).OrgID, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "lead")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	c, err := h.svc.Create(r.Context(), auth.MustPrincipal(r.Context()), id, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.Created(w, c)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "contact")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	c, err := h.svc.Get(r.Context(), auth.MustPrincipal(r.Context()).OrgID, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, c)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "contact")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	c, err := h.svc.Update(r.Context(), auth.MustPrincipal(r.Context()), id, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, c)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "contact")
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
