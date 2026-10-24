// Package icp implements Ideal Customer Profiles. An ICP is a set of criteria
// that compiles three ways: into a search of the company database (see
// internal/catalog), into an explainable 0–100 fit score for every lead
// (feeding automated scoring), and into lead-list filters in the UI.
package icp

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/auth"
	"leadline/internal/jobs"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/geo"
	"leadline/pkg/httpx"
)

type Criteria struct {
	Industries       []string `json:"industries"`
	Countries        []string `json:"countries"`
	States           []string `json:"states"`
	Cities           []string `json:"cities"`
	EmployeesMin     *int     `json:"employees_min"`
	EmployeesMax     *int     `json:"employees_max"`
	FoundedAfterYear *int     `json:"founded_after_year"`
	// Website: any | missing | missing_or_poor | poor | present | good
	Website       string   `json:"website"`
	Keywords      []string `json:"keywords"`       // matched in name, industry, description
	TechStack     []string `json:"tech_stack"`     // e.g. wordpress, shopify, wix
	BuyingSignals []string `json:"buying_signals"` // phrases in description, e.g. "now hiring", "newly opened"
	RequireSocial bool     `json:"require_social"`
	RequireEmail  bool     `json:"require_email"`
}

type ICP struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Criteria  Criteria  `json:"criteria"`
	IsDefault bool      `json:"is_default"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Facts are the lead attributes the fit score reads.
type Facts struct {
	CompanyName, Industry, Description, City, State, Country, Website, WebsiteQuality string
	EmployeeCount, FoundedYear                                                        *int
	TechStack                                                                         []string
	SocialCount                                                                       int
	HasEmail                                                                          bool
}

type FitResult struct {
	Fit     int      `json:"fit"`
	Matched []string `json:"matched"`
	Missed  []string `json:"missed"`
}

func fold(list []string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func anyContains(haystack string, needles []string) bool {
	h := strings.ToLower(haystack)
	for _, n := range needles {
		if strings.Contains(h, n) {
			return true
		}
	}
	return false
}

// Fit scores a lead against the criteria. Only criteria that are set count,
// each with a fixed weight; the result is the matched share of those weights.
func Fit(c Criteria, f Facts, now time.Time) FitResult {
	res := FitResult{Matched: []string{}, Missed: []string{}}
	total, got := 0, 0
	check := func(weight int, set, ok bool, label string) {
		if !set {
			return
		}
		total += weight
		if ok {
			got += weight
			res.Matched = append(res.Matched, label)
		} else {
			res.Missed = append(res.Missed, label)
		}
	}
	inds := fold(c.Industries)
	check(25, len(inds) > 0, slices.Contains(inds, strings.ToLower(f.Industry)) || anyContains(f.Industry, inds), "Industry")

	cities, states, countries := fold(c.Cities), fold(c.States), fold(c.Countries)
	locSet := len(cities)+len(states)+len(countries) > 0
	locOK := (len(cities) == 0 || slices.Contains(cities, strings.ToLower(f.City))) &&
		(len(states) == 0 || slices.Contains(states, strings.ToLower(f.State))) &&
		(len(countries) == 0 || slices.ContainsFunc(countries, func(c string) bool { return geo.Same(c, f.Country) }))
	check(20, locSet, locOK, "Location")

	sizeSet := c.EmployeesMin != nil || c.EmployeesMax != nil
	sizeOK := f.EmployeeCount != nil &&
		(c.EmployeesMin == nil || *f.EmployeeCount >= *c.EmployeesMin) &&
		(c.EmployeesMax == nil || *f.EmployeeCount <= *c.EmployeesMax)
	check(15, sizeSet, sizeOK, "Company size")

	check(15, c.FoundedAfterYear != nil, f.FoundedYear != nil && c.FoundedAfterYear != nil && *f.FoundedYear >= *c.FoundedAfterYear, "Opened recently")

	hasSite := strings.TrimSpace(f.Website) != ""
	var siteOK bool
	switch c.Website {
	case "missing":
		siteOK = !hasSite
	case "missing_or_poor":
		siteOK = !hasSite || f.WebsiteQuality == "poor" || f.WebsiteQuality == "none"
	case "poor":
		siteOK = f.WebsiteQuality == "poor"
	case "present":
		siteOK = hasSite
	case "good":
		siteOK = f.WebsiteQuality == "good" || f.WebsiteQuality == "ok"
	}
	check(15, c.Website != "" && c.Website != "any", siteOK, "Website")

	kws := fold(c.Keywords)
	check(10, len(kws) > 0, anyContains(f.CompanyName+" "+f.Industry+" "+f.Description, kws), "Keywords")

	tech := fold(c.TechStack)
	techOK := false
	for _, t := range f.TechStack {
		if slices.Contains(tech, strings.ToLower(t)) {
			techOK = true
		}
	}
	check(10, len(tech) > 0, techOK, "Technology")

	signals := fold(c.BuyingSignals)
	check(10, len(signals) > 0, anyContains(f.Description, signals), "Buying signals")
	check(5, c.RequireSocial, f.SocialCount > 0, "Social presence")
	check(5, c.RequireEmail, f.HasEmail, "Contact email")

	if total == 0 {
		return res
	}
	res.Fit = (got*100 + total/2) / total
	return res
}

func (c Criteria) validate() map[string]string {
	errs := map[string]string{}
	if !slices.Contains([]string{"", "any", "missing", "missing_or_poor", "poor", "present", "good"}, c.Website) {
		errs["criteria.website"] = "invalid website criterion"
	}
	if c.EmployeesMin != nil && c.EmployeesMax != nil && *c.EmployeesMin > *c.EmployeesMax {
		errs["criteria.employees_max"] = "must be at least the minimum"
	}
	return errs
}

// ───────────────────────── Service ─────────────────────────

type Service struct {
	pool *pgxpool.Pool
	q    jobs.Enqueuer
}

func NewService(pool *pgxpool.Pool, q jobs.Enqueuer) *Service { return &Service{pool: pool, q: q} }

func scan(row pgx.Row) (*ICP, error) {
	var i ICP
	var raw []byte
	if err := row.Scan(&i.ID, &i.Name, &raw, &i.IsDefault, &i.CreatedAt, &i.UpdatedAt); err != nil {
		if database.IsNoRows(err) {
			return nil, apperr.NotFound("ICP")
		}
		return nil, err
	}
	_ = json.Unmarshal(raw, &i.Criteria)
	return &i, nil
}

const cols = `id, name, criteria, is_default, created_at, updated_at`

// Default returns the organization's default ICP, or nil.
func Default(ctx context.Context, db database.DBTX, orgID uuid.UUID) (*ICP, error) {
	i, err := scan(db.QueryRow(ctx, `SELECT `+cols+` FROM icps WHERE organization_id=$1 AND is_default`, orgID))
	if apperr.IsNotFound(err) {
		return nil, nil
	}
	return i, err
}

func (s *Service) List(ctx context.Context, p auth.Principal) ([]ICP, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM icps WHERE organization_id=$1 ORDER BY is_default DESC, created_at`, p.OrgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ICP{}
	for rows.Next() {
		i, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *i)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, p auth.Principal, id uuid.UUID) (*ICP, error) {
	return scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM icps WHERE organization_id=$1 AND id=$2`, p.OrgID, id))
}

type Input struct {
	Name      string   `json:"name"`
	Criteria  Criteria `json:"criteria"`
	IsDefault bool     `json:"is_default"`
}

func (s *Service) Save(ctx context.Context, p auth.Principal, id *uuid.UUID, in Input) (*ICP, error) {
	in.Name = strings.TrimSpace(in.Name)
	errs := in.Criteria.validate()
	if in.Name == "" || len(in.Name) > 120 {
		errs["name"] = "is required"
	}
	if len(errs) > 0 {
		return nil, apperr.Invalid("validation failed", errs)
	}
	raw, _ := json.Marshal(in.Criteria)
	var saved uuid.UUID
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var first bool
		if err := tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM icps WHERE organization_id=$1)`, p.OrgID).Scan(&first); err != nil {
			return err
		}
		makeDefault := in.IsDefault || first
		if makeDefault {
			if _, err := tx.Exec(ctx, `UPDATE icps SET is_default=false WHERE organization_id=$1 AND is_default`, p.OrgID); err != nil {
				return err
			}
		}
		if id == nil {
			return tx.QueryRow(ctx, `INSERT INTO icps (organization_id, name, criteria, is_default, created_by) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
				p.OrgID, in.Name, raw, makeDefault, p.UserID).Scan(&saved)
		}
		tag, err := tx.Exec(ctx, `UPDATE icps SET name=$3, criteria=$4, is_default = is_default OR $5, updated_at=now() WHERE organization_id=$1 AND id=$2`,
			p.OrgID, *id, in.Name, raw, makeDefault)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("ICP")
		}
		saved = *id
		return nil
	})
	if err != nil {
		return nil, err
	}
	// The default ICP feeds every lead's fit score.
	if err := s.q.Enqueue(ctx, jobs.QueueDefault, jobs.TypeRescoreOrg, jobs.RescoreOrg{OrgID: p.OrgID}); err != nil {
		return nil, err
	}
	return s.Get(ctx, p, saved)
}

func (s *Service) Delete(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM icps WHERE organization_id=$1 AND id=$2`, p.OrgID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return apperr.NotFound("ICP")
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
	mux.HandleFunc("GET /api/v1/icps", h.mw.Org(auth.RoleViewer, h.list))
	mux.HandleFunc("POST /api/v1/icps", h.mw.Org(auth.RoleMember, h.create))
	mux.HandleFunc("GET /api/v1/icps/{id}", h.mw.Org(auth.RoleViewer, h.get))
	mux.HandleFunc("PUT /api/v1/icps/{id}", h.mw.Org(auth.RoleMember, h.update))
	mux.HandleFunc("DELETE /api/v1/icps/{id}", h.mw.Org(auth.RoleMember, h.delete))
}

func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, apperr.NotFound("ICP")
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
	i, err := h.svc.Get(r.Context(), auth.MustPrincipal(r.Context()), id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, i)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	i, err := h.svc.Save(r.Context(), auth.MustPrincipal(r.Context()), nil, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.Created(w, i)
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
	i, err := h.svc.Save(r.Context(), auth.MustPrincipal(r.Context()), &id, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, i)
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
