// Package dashboards stores custom analytics dashboards: a named, ordered list
// of widgets plus step-by-step lead filters. The widget and step objects are
// owned by the UI (see frontend/components/dashboards) and stored as JSON;
// the server validates their size and shape only.
package dashboards

import (
	"context"
	"encoding/json"
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
)

type Dashboard struct {
	ID        uuid.UUID       `json:"id"`
	Name      string          `json:"name"`
	Widgets   json.RawMessage `json:"widgets"`
	Steps     json.RawMessage `json:"steps"`
	CreatedBy *uuid.UUID      `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type Input struct {
	Name    string          `json:"name"`
	Widgets json.RawMessage `json:"widgets"`
	Steps   json.RawMessage `json:"steps"`
}

const (
	maxWidgets = 40
	maxSteps   = 12
	maxJSON    = 256 << 10
)

func (in *Input) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	errs := map[string]string{}
	if in.Name == "" || len(in.Name) > 120 {
		errs["name"] = "is required (up to 120 characters)"
	}
	check := func(field string, raw *json.RawMessage, max int) {
		if len(*raw) == 0 || string(*raw) == "null" {
			*raw = json.RawMessage("[]")
			return
		}
		if len(*raw) > maxJSON {
			errs[field] = "is too large"
			return
		}
		var items []map[string]any
		if err := json.Unmarshal(*raw, &items); err != nil {
			errs[field] = "must be an array of objects"
			return
		}
		if len(items) > max {
			errs[field] = "has too many items"
		}
	}
	check("widgets", &in.Widgets, maxWidgets)
	check("steps", &in.Steps, maxSteps)
	if len(errs) > 0 {
		return apperr.Invalid("validation failed", errs)
	}
	return nil
}

type Service struct {
	pool *pgxpool.Pool
	log  *audit.Log
}

func NewService(pool *pgxpool.Pool, a *audit.Log) *Service { return &Service{pool: pool, log: a} }

const cols = `id, name, widgets, steps, created_by, created_at, updated_at`

func scan(row pgx.Row) (*Dashboard, error) {
	var d Dashboard
	if err := row.Scan(&d.ID, &d.Name, &d.Widgets, &d.Steps, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if database.IsNoRows(err) {
			return nil, apperr.NotFound("dashboard")
		}
		return nil, err
	}
	return &d, nil
}

func (s *Service) List(ctx context.Context, p auth.Principal) ([]Dashboard, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM dashboards WHERE organization_id=$1 ORDER BY updated_at DESC`, p.OrgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Dashboard{}
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, p auth.Principal, id uuid.UUID) (*Dashboard, error) {
	return scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM dashboards WHERE organization_id=$1 AND id=$2`, p.OrgID, id))
}

func (s *Service) Create(ctx context.Context, p auth.Principal, in Input) (*Dashboard, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	d, err := scan(s.pool.QueryRow(ctx, `INSERT INTO dashboards (organization_id, name, widgets, steps, created_by)
		VALUES ($1,$2,$3,$4,$5) RETURNING `+cols, p.OrgID, in.Name, in.Widgets, in.Steps, p.UserID))
	if err == nil {
		s.log.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "dashboard.created", EntityType: "dashboard", EntityID: d.ID.String()})
	}
	return d, err
}

func (s *Service) Update(ctx context.Context, p auth.Principal, id uuid.UUID, in Input) (*Dashboard, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	return scan(s.pool.QueryRow(ctx, `UPDATE dashboards SET name=$3, widgets=$4, steps=$5, updated_at=now()
		WHERE organization_id=$1 AND id=$2 RETURNING `+cols, p.OrgID, id, in.Name, in.Widgets, in.Steps))
}

func (s *Service) Delete(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM dashboards WHERE organization_id=$1 AND id=$2`, p.OrgID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return apperr.NotFound("dashboard")
	}
	if err == nil {
		s.log.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "dashboard.deleted", EntityType: "dashboard", EntityID: id.String()})
	}
	return err
}

// ───────────── HTTP ─────────────

type Handler struct {
	svc *Service
	mw  *auth.Middleware
}

func NewHandler(svc *Service, mw *auth.Middleware) *Handler { return &Handler{svc: svc, mw: mw} }

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/dashboards", h.mw.Org(auth.RoleViewer, h.list))
	mux.HandleFunc("POST /api/v1/dashboards", h.mw.Org(auth.RoleMember, h.create))
	mux.HandleFunc("GET /api/v1/dashboards/{id}", h.mw.Org(auth.RoleViewer, h.get))
	mux.HandleFunc("PUT /api/v1/dashboards/{id}", h.mw.Org(auth.RoleMember, h.update))
	mux.HandleFunc("DELETE /api/v1/dashboards/{id}", h.mw.Org(auth.RoleMember, h.delete))
}

func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, apperr.NotFound("dashboard")
	}
	return id, nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.List(r.Context(), auth.MustPrincipal(r.Context()))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	d, err := h.svc.Get(r.Context(), auth.MustPrincipal(r.Context()), id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, d)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	d, err := h.svc.Create(r.Context(), auth.MustPrincipal(r.Context()), in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.Created(w, d)
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
	d, err := h.svc.Update(r.Context(), auth.MustPrincipal(r.Context()), id, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, d)
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
