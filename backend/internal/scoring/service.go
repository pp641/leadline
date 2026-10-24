package scoring

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/internal/icp"
	"leadline/internal/jobs"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/httpx"
	"leadline/pkg/queue"
)

type Service struct {
	pool  *pgxpool.Pool
	q     jobs.Enqueuer
	audit *audit.Log
}

func NewService(pool *pgxpool.Pool, q jobs.Enqueuer, a *audit.Log) *Service {
	return &Service{pool: pool, q: q, audit: a}
}

func (s *Service) Config(ctx context.Context, orgID uuid.UUID) (Config, error) {
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT scoring_config FROM organizations WHERE id=$1`, orgID).Scan(&raw); err != nil {
		if database.IsNoRows(err) {
			return Config{}, apperr.NotFound("organization")
		}
		return Config{}, err
	}
	if len(raw) == 0 {
		return DefaultConfig(), nil
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return DefaultConfig(), nil
	}
	return c, nil
}

// UpdateConfig saves new rules under a bumped version and rescores every lead.
func (s *Service) UpdateConfig(ctx context.Context, p auth.Principal, rules []Rule) (Config, error) {
	cur, err := s.Config(ctx, p.OrgID)
	if err != nil {
		return Config{}, err
	}
	next := Config{Version: cur.Version + 1, Rules: rules}
	for i := range next.Rules {
		if next.Rules[i].ID == "" {
			next.Rules[i].ID = uuid.NewString()[:8]
		}
	}
	if errs := next.Validate(); len(errs) > 0 {
		return Config{}, apperr.Invalid("validation failed", errs)
	}
	raw, _ := json.Marshal(next)
	if _, err := s.pool.Exec(ctx, `UPDATE organizations SET scoring_config=$2, updated_at=now() WHERE id=$1`, p.OrgID, raw); err != nil {
		return Config{}, err
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "scoring.updated", EntityType: "organization", EntityID: p.OrgID.String(), Metadata: map[string]any{"version": next.Version}})
	if err := s.q.Enqueue(ctx, jobs.QueueDefault, jobs.TypeRescoreOrg, jobs.RescoreOrg{OrgID: p.OrgID}); err != nil {
		return Config{}, err
	}
	return next, nil
}

// ScoreLeads recomputes and stores scores for the given leads.
func (s *Service) ScoreLeads(ctx context.Context, orgID uuid.UUID, leadIDs []uuid.UUID) error {
	if len(leadIDs) == 0 {
		return nil
	}
	cfg, err := s.Config(ctx, orgID)
	if err != nil {
		return err
	}
	facts, err := loadFacts(ctx, s.pool, orgID, leadIDs)
	if err != nil {
		return err
	}
	profile, err := icp.Default(ctx, s.pool, orgID)
	if err != nil {
		return err
	}
	now := time.Now()
	batch := &pgxBatch{}
	for id, f := range facts {
		// Automated component: fit against the organization's default ICP.
		var fit *int
		if profile != nil {
			r := icp.Fit(profile.Criteria, icpFacts(f), now)
			fit = &r.Fit
		}
		f.ICPFit = fit
		batch.add(`UPDATE leads SET icp_fit=$3 WHERE organization_id=$1 AND id=$2 AND icp_fit IS DISTINCT FROM $3`, orgID, id, fit)
		res := Score(cfg, f, now)
		reasons, _ := json.Marshal(res.Reasons)
		batch.add(`
			INSERT INTO lead_scores (organization_id, lead_id, score, reasons, score_version)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (lead_id) DO UPDATE SET score=EXCLUDED.score, reasons=EXCLUDED.reasons,
				score_version=EXCLUDED.score_version, created_at=now()`,
			orgID, id, res.Score, reasons, res.Version)
		batch.add(`UPDATE leads SET lead_score=$3 WHERE organization_id=$1 AND id=$2 AND lead_score<>$3`, orgID, id, res.Score)
	}
	return batch.exec(ctx, s.pool)
}

// RescoreOrg rescores every lead of an organization in pages.
func (s *Service) RescoreOrg(ctx context.Context, orgID uuid.UUID) error {
	var after uuid.UUID
	for {
		rows, err := s.pool.Query(ctx, `SELECT id FROM leads WHERE organization_id=$1 AND id > $2 ORDER BY id LIMIT 1000`, orgID, after)
		if err != nil {
			return err
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if len(ids) == 0 {
			return nil
		}
		if err := s.ScoreLeads(ctx, orgID, ids); err != nil {
			return err
		}
		after = ids[len(ids)-1]
	}
}

// Explain returns the stored score breakdown for one lead.
func (s *Service) Explain(ctx context.Context, orgID, leadID uuid.UUID) (*Result, error) {
	var r Result
	var reasons []byte
	err := s.pool.QueryRow(ctx, `SELECT score, reasons, score_version FROM lead_scores WHERE organization_id=$1 AND lead_id=$2`, orgID, leadID).
		Scan(&r.Score, &reasons, &r.Version)
	if database.IsNoRows(err) {
		return &Result{Reasons: []string{}}, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(reasons, &r.Reasons)
	return &r, nil
}

func loadFacts(ctx context.Context, db database.DBTX, orgID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]Facts, error) {
	rows, err := db.Query(ctx, `
		SELECT l.id, l.company_name, l.website_quality, l.tech_stack, l.website, l.industry, l.city, l.state, l.country, l.description, l.source,
		       l.employee_count, l.founded_year, l.phone <> '',
		       jsonb_array_length(l.social_urls) + (CASE WHEN l.linkedin_url <> '' THEN 1 ELSE 0 END),
		       (l.business_email IS NOT NULL OR EXISTS (SELECT 1 FROM lead_contacts c WHERE c.lead_id=l.id AND c.email IS NOT NULL)),
		       COALESCE((SELECT array_agg(c.email_status) FROM lead_contacts c WHERE c.lead_id=l.id AND c.email IS NOT NULL), '{}'),
		       COALESCE((SELECT array_agg(t.tag) FROM lead_tags t WHERE t.lead_id=l.id), '{}')
		FROM leads l WHERE l.organization_id=$1 AND l.id = ANY($2)`, orgID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uuid.UUID]Facts, len(ids))
	for rows.Next() {
		var id uuid.UUID
		var f Facts
		var statuses []string
		if err := rows.Scan(&id, &f.CompanyName, &f.WebsiteQuality, &f.TechStack, &f.Website, &f.Industry, &f.City, &f.State, &f.Country, &f.Description, &f.Source,
			&f.EmployeeCount, &f.FoundedYear, &f.HasPhone, &f.SocialCount, &f.HasEmail, &statuses, &f.Tags); err != nil {
			return nil, err
		}
		best := ""
		for _, st := range statuses {
			if best == "" || EmailStatusRank[st] > EmailStatusRank[best] {
				best = st
			}
		}
		f.BestEmailStatus = best
		out[id] = f
	}
	return out, rows.Err()
}

func icpFacts(f Facts) icp.Facts {
	return icp.Facts{
		CompanyName: f.CompanyName, Industry: f.Industry, Description: f.Description, City: f.City, State: f.State,
		Country: f.Country, Website: f.Website, WebsiteQuality: f.WebsiteQuality, EmployeeCount: f.EmployeeCount,
		FoundedYear: f.FoundedYear, TechStack: f.TechStack, SocialCount: f.SocialCount, HasEmail: f.HasEmail,
	}
}

// ExplainFit returns the ICP fit breakdown for one lead.
func (s *Service) ExplainFit(ctx context.Context, orgID, leadID uuid.UUID) (*icp.FitResult, error) {
	profile, err := icp.Default(ctx, s.pool, orgID)
	if err != nil || profile == nil {
		return nil, err
	}
	facts, err := loadFacts(ctx, s.pool, orgID, []uuid.UUID{leadID})
	if err != nil {
		return nil, err
	}
	f, ok := facts[leadID]
	if !ok {
		return nil, apperr.NotFound("lead")
	}
	r := icp.Fit(profile.Criteria, icpFacts(f), time.Now())
	return &r, nil
}

// Suggestion is a data-driven scoring rule proposal.
type Suggestion struct {
	Rule         Rule    `json:"rule"`
	Segment      string  `json:"segment"`
	Leads        int     `json:"leads"`
	PositiveRate float64 `json:"positive_rate"`
	BaselineRate float64 `json:"baseline_rate"`
	Lift         float64 `json:"lift"`
}

// Suggestions learns from outcomes: among contacted leads, it compares the
// share that became interested (or further) per segment against the overall
// rate, and proposes rules that reward segments that convert better and
// penalise those that convert worse. Segments need at least minSample leads.
func (s *Service) Suggestions(ctx context.Context, orgID uuid.UUID) ([]Suggestion, error) {
	const minSample = 10
	var total, positive int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE status IN ('interested','qualified','meeting','won'))
		FROM leads WHERE organization_id=$1 AND status <> 'new'`, orgID).Scan(&total, &positive); err != nil {
		return nil, err
	}
	out := []Suggestion{}
	if total < minSample*2 || positive == 0 {
		return out, nil
	}
	base := float64(positive) / float64(total)
	segments := []struct{ field, expr string }{
		{"industry", "lower(industry)"},
		{"city", "lower(city)"},
		{"website_quality", "website_quality"},
		{"source", "source"},
	}
	for _, seg := range segments {
		rows, err := s.pool.Query(ctx, `
			SELECT `+seg.expr+`, count(*), count(*) FILTER (WHERE status IN ('interested','qualified','meeting','won'))
			FROM leads WHERE organization_id=$1 AND status <> 'new' AND `+seg.expr+` <> ''
			GROUP BY 1 HAVING count(*) >= $2 ORDER BY 2 DESC LIMIT 20`, orgID, minSample)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var val string
			var n, pos int
			if err := rows.Scan(&val, &n, &pos); err != nil {
				rows.Close()
				return nil, err
			}
			rate := float64(pos) / float64(n)
			lift := rate / base
			if lift > 0.67 && lift < 1.5 {
				continue // not different enough to act on
			}
			pts := int((lift - 1) * 10)
			pts = max(-20, min(20, pts))
			if pts == 0 {
				continue
			}
			reason := fmt.Sprintf("%s %q converts %.1fx the average", seg.field, val, lift)
			out = append(out, Suggestion{
				Rule:    Rule{Field: seg.field, Operator: "eq", Values: []string{val}, Points: pts, Reason: reason, Enabled: true},
				Segment: seg.field + "=" + val, Leads: n, PositiveRate: rate, BaselineRate: base, Lift: lift,
			})
		}
		rows.Close()
	}
	return out, nil
}

// ───────────────────────── Jobs ─────────────────────────

func (s *Service) HandleScoreLeads(ctx context.Context, job queue.Job) error {
	var p jobs.ScoreLeads
	if err := job.Decode(&p); err != nil {
		return queue.Permanent(err)
	}
	return s.ScoreLeads(ctx, p.OrgID, p.LeadIDs)
}

func (s *Service) HandleRescoreOrg(ctx context.Context, job queue.Job) error {
	var p jobs.RescoreOrg
	if err := job.Decode(&p); err != nil {
		return queue.Permanent(err)
	}
	return s.RescoreOrg(ctx, p.OrgID)
}

// ───────────────────────── HTTP ─────────────────────────

type Handler struct {
	svc *Service
	mw  *auth.Middleware
}

func NewHandler(svc *Service, mw *auth.Middleware) *Handler { return &Handler{svc: svc, mw: mw} }

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/scoring", h.mw.Org(auth.RoleViewer, h.get))
	mux.HandleFunc("PUT /api/v1/scoring", h.mw.Org(auth.RoleAdmin, h.put))
	mux.HandleFunc("GET /api/v1/scoring/suggestions", h.mw.Org(auth.RoleViewer, h.suggestions))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.Config(r.Context(), auth.MustPrincipal(r.Context()).OrgID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, c, map[string]any{
		"text_fields": TextFields, "number_fields": NumberFields, "bool_fields": BoolFields,
		"text_operators": TextOperators, "number_operators": NumOperators, "bool_operators": BoolOperators,
	})
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Rules []Rule `json:"rules"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	c, err := h.svc.UpdateConfig(r.Context(), auth.MustPrincipal(r.Context()), in.Rules)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, c)
}

func (h *Handler) suggestions(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Suggestions(r.Context(), auth.MustPrincipal(r.Context()).OrgID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

// FilterByICP keeps the leads whose fit against an ICP (any profile, not only
// the default) is at least minFit, ordered by fit descending.
func FilterByICP(ctx context.Context, db database.DBTX, orgID, icpID uuid.UUID, minFit int, ids []uuid.UUID) ([]uuid.UUID, error) {
	var raw []byte
	err := db.QueryRow(ctx, `SELECT criteria FROM icps WHERE organization_id=$1 AND id=$2`, orgID, icpID).Scan(&raw)
	if database.IsNoRows(err) {
		return nil, apperr.NotFound("ICP")
	}
	if err != nil {
		return nil, err
	}
	var c icp.Criteria
	_ = json.Unmarshal(raw, &c)
	facts, err := loadFacts(ctx, db, orgID, ids)
	if err != nil {
		return nil, err
	}
	type scored struct {
		id  uuid.UUID
		fit int
	}
	now := time.Now()
	var keep []scored
	for id, f := range facts {
		if r := icp.Fit(c, icpFacts(f), now); r.Fit >= minFit {
			keep = append(keep, scored{id, r.Fit})
		}
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].fit > keep[j].fit })
	out := make([]uuid.UUID, len(keep))
	for i, k := range keep {
		out[i] = k.id
	}
	return out, nil
}
