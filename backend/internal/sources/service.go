package sources

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/internal/contacts"
	"leadline/internal/integrations"
	"leadline/internal/jobs"
	"leadline/internal/leads"
	"leadline/internal/webhooks"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/httpx"
)

type Service struct {
	pool *pgxpool.Pool
	reg  *integrations.Registry
	q    jobs.Enqueuer
	log  *audit.Log
}

func NewService(pool *pgxpool.Pool, reg *integrations.Registry, q jobs.Enqueuer, a *audit.Log) *Service {
	return &Service{pool: pool, reg: reg, q: q, log: a}
}

// SourceInfo describes an import option for Leads → Import.
type SourceInfo struct {
	integrations.Provider
	Connected bool `json:"connected"`
}

func (s *Service) List(ctx context.Context, p auth.Principal) ([]SourceInfo, error) {
	connected := map[string]bool{}
	kinds, err := s.reg.ConnectedWith(ctx, p.OrgID, integrations.CapImport)
	if err != nil {
		return nil, err
	}
	for _, k := range kinds {
		connected[k] = true
	}
	out := []SourceInfo{}
	for _, prov := range integrations.Catalog {
		if _, ok := Registry[prov.Kind]; ok {
			out = append(out, SourceInfo{Provider: prov, Connected: connected[prov.Kind]})
		}
	}
	return out, nil
}

// Result is a candidate plus the existing lead it duplicates, if any.
type Result struct {
	Candidate
	DuplicateLeadID *uuid.UUID `json:"duplicate_lead_id"`
}

func (s *Service) source(kind string) (Source, error) {
	src, ok := Registry[kind]
	if !ok {
		return nil, apperr.NotFound("lead source")
	}
	return src, nil
}

func (s *Service) Search(ctx context.Context, p auth.Principal, kind string, params Params) ([]Result, error) {
	src, err := s.source(kind)
	if err != nil {
		return nil, err
	}
	creds, err := s.reg.Credentials(ctx, p.OrgID, kind)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cands, err := src.Search(ctx, creds, params)
	if err != nil {
		if ae, ok := apperr.As(err); ok {
			return nil, ae
		}
		return nil, apperr.Invalid(err.Error(), nil)
	}
	out := make([]Result, 0, len(cands))
	for _, c := range cands {
		Normalize(&c)
		if c.CompanyName == "" {
			continue
		}
		dup, err := findDuplicate(ctx, s.pool, p.OrgID, &c)
		if err != nil {
			return nil, err
		}
		out = append(out, Result{Candidate: c, DuplicateLeadID: dup})
	}
	return out, nil
}

// findDuplicate matches an existing lead by website domain, or by name and city.
func findDuplicate(ctx context.Context, db database.DBTX, orgID uuid.UUID, c *Candidate) (*uuid.UUID, error) {
	_, domain := leads.NormalizeWebsite(c.Website)
	if domain == "" {
		domain = c.Domain
	}
	var id uuid.UUID
	err := db.QueryRow(ctx, `
		SELECT id FROM leads WHERE organization_id=$1 AND (
			($2 <> '' AND domain = $2) OR
			(lower(company_name) = lower($3) AND lower(city) = lower($4)))
		LIMIT 1`, orgID, domain, leads.Clean(c.CompanyName), leads.Clean(c.City)).Scan(&id)
	if database.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

type ImportResult struct {
	Imported   int         `json:"imported"`
	Duplicates int         `json:"duplicates"`
	Skipped    int         `json:"skipped"`
	LeadIDs    []uuid.UUID `json:"lead_ids"`
}

// Import turns candidates (as returned by Search) into leads with contacts,
// provenance and an enrichment record, then queues verification and scoring.
func (s *Service) Import(ctx context.Context, p auth.Principal, kind string, cands []Candidate, tags []string) (*ImportResult, error) {
	if _, err := s.source(kind); err != nil {
		return nil, err
	}
	if len(cands) == 0 || len(cands) > 500 {
		return nil, apperr.Invalid("validation failed", map[string]string{"candidates": "choose between 1 and 500 companies"})
	}
	tags = leads.NormalizeTags(append(tags, kind))
	res := &ImportResult{LeadIDs: []uuid.UUID{}}
	var contactIDs []uuid.UUID
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		for i := range cands {
			c := &cands[i]
			Normalize(c)
			if dup, err := findDuplicate(ctx, tx, p.OrgID, c); err != nil {
				return err
			} else if dup != nil {
				res.Duplicates++
				continue
			}
			id, cids, err := s.insert(ctx, tx, p, kind, c, tags)
			if err != nil {
				return err
			}
			if id == uuid.Nil {
				res.Skipped++
				continue
			}
			res.LeadIDs = append(res.LeadIDs, id)
			contactIDs = append(contactIDs, cids...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.Imported = len(res.LeadIDs)
	s.log.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "source.imported", EntityType: "lead",
		Metadata: map[string]any{"source": kind, "count": res.Imported}})
	if err := jobs.EnqueueVerify(ctx, s.q, p.OrgID, contactIDs); err != nil {
		return nil, err
	}
	return res, jobs.EnqueueScore(ctx, s.q, p.OrgID, res.LeadIDs)
}

func (s *Service) insert(ctx context.Context, tx pgx.Tx, p auth.Principal, kind string, c *Candidate, tags []string) (uuid.UUID, []uuid.UUID, error) {
	in := &leads.Input{
		CompanyName: c.CompanyName, Website: c.Website, Industry: c.Industry, Description: c.Description,
		Country: c.Country, State: c.State, City: c.City, Address: c.Address, Phone: c.Phone,
		BusinessEmail: c.Email, LinkedInURL: c.LinkedInURL, SocialURLs: c.SocialURLs,
		EmployeeCount: c.EmployeeCount, FoundedYear: c.FoundedYear, Source: kind, SourceURL: c.SourceURL,
	}
	domain, err := leads.Prepare(in)
	if err != nil {
		// Retry without the optional fields that most often fail validation.
		in.BusinessEmail, in.SourceURL, in.LinkedInURL, in.SocialURLs = "", "", "", nil
		if domain, err = leads.Prepare(in); err != nil {
			return uuid.Nil, nil, nil // unusable record (e.g. no name)
		}
	}
	leadID, err := leads.Insert(ctx, tx, p.OrgID, in, domain, nil)
	if err != nil {
		return uuid.Nil, nil, err
	}
	if len(c.TechStack) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE leads SET tech_stack=$2 WHERE id=$1`, leadID, c.TechStack); err != nil {
			return uuid.Nil, nil, err
		}
	}
	var contactIDs []uuid.UUID
	for i, person := range c.Contacts {
		ci := &contacts.Input{FirstName: person.FirstName, LastName: person.LastName, JobTitle: person.Title,
			Email: person.Email, LinkedInURL: person.LinkedInURL, Source: kind, Confidence: person.Confidence,
			IsPrimary: i == 0 && person.Email != ""}
		if ci.Prepare() != nil {
			continue
		}
		cid, err := contacts.Insert(ctx, tx, p.OrgID, leadID, ci)
		if err != nil {
			return uuid.Nil, nil, err
		}
		if ci.Email != "" {
			contactIDs = append(contactIDs, cid)
		}
	}
	report, _ := json.Marshal(map[string]any{"external_id": c.ExternalID, "source_url": c.SourceURL, "extra": c.Extra,
		"contacts_found": len(c.Contacts), "imported_at": time.Now().UTC()})
	if _, err := tx.Exec(ctx, `INSERT INTO lead_enrichment (organization_id, lead_id, provider, data) VALUES ($1,$2,$3,$4)
		ON CONFLICT (lead_id, provider) DO UPDATE SET data=EXCLUDED.data, enriched_at=now()`, p.OrgID, leadID, kind, report); err != nil {
		return uuid.Nil, nil, err
	}
	meta := map[string]any{"external_id": c.ExternalID}
	if _, err := tx.Exec(ctx, `INSERT INTO lead_sources (organization_id, lead_id, source, source_url, consent_basis, metadata)
		VALUES ($1,$2,$3,$4,'legitimate_interest',$5)`, p.OrgID, leadID, kind, in.SourceURL, meta); err != nil {
		return uuid.Nil, nil, err
	}
	if err := leads.AddTags(ctx, tx, p.OrgID, []uuid.UUID{leadID}, tags); err != nil {
		return uuid.Nil, nil, err
	}
	if err := leads.RecordEvent(ctx, tx, p.OrgID, leadID, "discovered", &p.UserID, map[string]any{"source": kind, "provider": kind}); err != nil {
		return uuid.Nil, nil, err
	}
	return leadID, contactIDs, webhooks.Publish(ctx, tx, p.OrgID, webhooks.LeadCreated,
		map[string]any{"lead_id": leadID, "company_name": in.CompanyName, "source": kind})
}

// ───────────── HTTP ─────────────

type Handler struct {
	svc *Service
	mw  *auth.Middleware
}

func NewHandler(svc *Service, mw *auth.Middleware) *Handler { return &Handler{svc: svc, mw: mw} }

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/sources", h.mw.Org(auth.RoleViewer, h.list))
	mux.HandleFunc("POST /api/v1/sources/{kind}/search", h.mw.Org(auth.RoleMember, h.search))
	mux.HandleFunc("POST /api/v1/sources/{kind}/import", h.mw.Org(auth.RoleMember, h.importLeads))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.List(r.Context(), auth.MustPrincipal(r.Context()))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Params Params `json:"params"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.Search(r.Context(), auth.MustPrincipal(r.Context()), r.PathValue("kind"), in.Params)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) importLeads(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Candidates []Candidate `json:"candidates"`
		Tags       []string    `json:"tags"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.Import(r.Context(), auth.MustPrincipal(r.Context()), r.PathValue("kind"), in.Candidates, in.Tags)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}
