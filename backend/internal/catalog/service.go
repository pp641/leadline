package catalog

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/internal/icp"
	"leadline/internal/jobs"
	"leadline/internal/leads"
	"leadline/internal/webhooks"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
)

type Service struct {
	pool *pgxpool.Pool
	q    jobs.Enqueuer
	log  *audit.Log
	// Source is nil when SCRAPER_DATABASE_URL is not set.
	Source Source
}

func NewService(pool *pgxpool.Pool, src Source, q jobs.Enqueuer, a *audit.Log) *Service {
	return &Service{pool: pool, Source: src, q: q, log: a}
}

func (s *Service) source() (Source, error) {
	if s.Source == nil {
		return nil, apperr.Conflict("company_db_not_configured", "the company database is not connected — set SCRAPER_DATABASE_URL")
	}
	return s.Source, nil
}

// Status reports whether the company database is configured and reachable.
func (s *Service) Status(ctx context.Context) map[string]any {
	if s.Source == nil {
		return map[string]any{"configured": false, "reachable": false}
	}
	err := s.Source.Ping(ctx)
	out := map[string]any{"configured": true, "reachable": err == nil}
	if err != nil {
		out["error"] = "company database unreachable"
	}
	return out
}

// FromCriteria turns an ICP into a company search.
func FromCriteria(c icp.Criteria) Query {
	return Query{
		Industries: c.Industries, Countries: c.Countries, States: c.States, Cities: c.Cities,
		Keywords: c.Keywords, Website: c.Website, RequireEmail: c.RequireEmail,
	}
}

type SearchInput struct {
	ICPID        *uuid.UUID `json:"icp_id"`
	Query        *Query     `json:"query"` // ad-hoc search when no ICP
	VerifiedOnly bool       `json:"verified_only"`
	Limit        int        `json:"limit"`
}

// Result is a company plus the lead it already exists as, if any.
type Result struct {
	Company         Company    `json:"company"`
	DuplicateLeadID *uuid.UUID `json:"duplicate_lead_id"`
}

func (s *Service) Search(ctx context.Context, p auth.Principal, in SearchInput) ([]Result, error) {
	src, err := s.source()
	if err != nil {
		return nil, err
	}
	var q Query
	switch {
	case in.ICPID != nil:
		var raw []byte
		err := s.pool.QueryRow(ctx, `SELECT criteria FROM icps WHERE organization_id=$1 AND id=$2`, p.OrgID, *in.ICPID).Scan(&raw)
		if database.IsNoRows(err) {
			return nil, apperr.NotFound("ICP")
		}
		if err != nil {
			return nil, err
		}
		var c icp.Criteria
		_ = json.Unmarshal(raw, &c)
		q = FromCriteria(c)
	case in.Query != nil:
		q = *in.Query
	default:
		return nil, apperr.Invalid("validation failed", map[string]string{"icp_id": "choose an ICP or provide a query"})
	}
	q.VerifiedOnly = q.VerifiedOnly || in.VerifiedOnly
	q.Limit = in.Limit
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	companies, err := src.Search(ctx, q)
	if err != nil {
		return nil, apperr.Unavailable("the company database is unavailable; is company-scraper running?", err)
	}
	out := make([]Result, 0, len(companies))
	for _, c := range companies {
		dup, err := findDuplicate(ctx, s.pool, p.OrgID, c)
		if err != nil {
			return nil, err
		}
		out = append(out, Result{Company: c, DuplicateLeadID: dup})
	}
	return out, nil
}

// findDuplicate matches an existing lead by company ID, website domain, or name and city.
func findDuplicate(ctx context.Context, db database.DBTX, orgID uuid.UUID, c Company) (*uuid.UUID, error) {
	_, domain := leads.NormalizeWebsite(c.Website)
	var id uuid.UUID
	err := db.QueryRow(ctx, `
		SELECT id FROM leads WHERE organization_id=$1 AND (
			catalog_company_id = $2 OR
			($3 <> '' AND domain = $3) OR
			(lower(company_name) = lower($4) AND lower(city) = lower($5)))
		LIMIT 1`, orgID, c.ID, domain, leads.Clean(c.Name), leads.Clean(c.City)).Scan(&id)
	if database.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// usableEmail drops addresses the scraper found to be undeliverable.
func usableEmail(c Company) string {
	if c.Email == nil || slices.Contains([]string{"invalid", "disposable"}, c.EmailStatus) {
		return ""
	}
	return *c.Email
}

type ImportResult struct {
	Imported   int         `json:"imported"`
	Duplicates int         `json:"duplicates"`
	LeadIDs    []uuid.UUID `json:"lead_ids"`
}

// Import creates leads from companies, skipping ones that already exist.
func (s *Service) Import(ctx context.Context, p auth.Principal, ids []uuid.UUID, tags []string) (*ImportResult, error) {
	src, err := s.source()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 || len(ids) > 500 {
		return nil, apperr.Invalid("validation failed", map[string]string{"company_ids": "choose between 1 and 500 companies"})
	}
	companies, err := src.Get(ctx, ids)
	if err != nil {
		return nil, apperr.Unavailable("the company database is unavailable; is company-scraper running?", err)
	}
	tags = leads.NormalizeTags(tags)
	res := &ImportResult{LeadIDs: []uuid.UUID{}}
	err = database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		for _, c := range companies {
			if dup, err := findDuplicate(ctx, tx, p.OrgID, c); err != nil {
				return err
			} else if dup != nil {
				res.Duplicates++
				continue
			}
			id, err := s.insertLead(ctx, tx, p, c, tags)
			if err != nil {
				return err
			}
			if id != uuid.Nil {
				res.LeadIDs = append(res.LeadIDs, id)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.Imported = len(res.LeadIDs)
	s.log.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "catalog.imported", EntityType: "lead", Metadata: map[string]any{"count": res.Imported}})
	return res, jobs.EnqueueScore(ctx, s.q, p.OrgID, res.LeadIDs)
}

func (s *Service) insertLead(ctx context.Context, tx pgx.Tx, p auth.Principal, c Company, tags []string) (uuid.UUID, error) {
	in := &leads.Input{
		CompanyName: c.Name, Website: c.Website, Industry: c.Industry, Description: c.Description,
		Country: c.Country, State: c.State, City: c.City, Address: c.Address, Phone: c.Phone,
		BusinessEmail: usableEmail(c), LinkedInURL: c.LinkedInURL, SocialURLs: c.SocialURLs,
		EmployeeCount: c.EmployeeCount, FoundedYear: c.FoundedYear, Source: c.Source, SourceURL: c.SourceURL,
	}
	domain, err := leads.Prepare(in)
	if err != nil {
		return uuid.Nil, nil // unusable record (e.g. no name); skip it
	}
	leadID, err := leads.Insert(ctx, tx, p.OrgID, in, domain, nil)
	if err != nil {
		return uuid.Nil, err
	}
	// Website signals were already gathered by the scraper; they feed scoring and ICP fit.
	quality := c.WebsiteQuality
	if !slices.Contains([]string{"", "none", "poor", "ok", "good"}, quality) {
		quality = ""
	}
	tech := c.TechStack
	if tech == nil {
		tech = []string{}
	}
	if _, err := tx.Exec(ctx, `UPDATE leads SET catalog_company_id=$2, website_quality=$3, tech_stack=$4, enriched_at=$5 WHERE id=$1`,
		leadID, c.ID, quality, tech, c.VerifiedAt); err != nil {
		return uuid.Nil, err
	}
	report, _ := json.Marshal(map[string]any{
		"company_id": c.ID, "verification_status": c.VerificationStatus, "verification": c.Verification,
		"website_status": c.WebsiteStatus, "website": c.WebsiteReport, "email_status": c.EmailStatus, "phone_valid": c.PhoneValid,
	})
	if _, err := tx.Exec(ctx, `INSERT INTO lead_enrichment (organization_id, lead_id, provider, data) VALUES ($1,$2,'company_db',$3)
		ON CONFLICT (lead_id, provider) DO UPDATE SET data=EXCLUDED.data, enriched_at=now()`, p.OrgID, leadID, report); err != nil {
		return uuid.Nil, err
	}
	basis := "legitimate_interest"
	if c.Source == "openstreetmap" || c.Source == "website" {
		basis = "public_business_contact" // published by the business itself
	}
	meta := map[string]any{"company_id": c.ID, "verification_status": c.VerificationStatus}
	if c.Attribution != "" {
		meta["attribution"] = c.Attribution
	}
	if _, err := tx.Exec(ctx, `INSERT INTO lead_sources (organization_id, lead_id, source, source_url, consent_basis, metadata)
		VALUES ($1,$2,$3,$4,$5,$6)`, p.OrgID, leadID, in.Source, in.SourceURL, basis, meta); err != nil {
		return uuid.Nil, err
	}
	if err := leads.AddTags(ctx, tx, p.OrgID, []uuid.UUID{leadID}, tags); err != nil {
		return uuid.Nil, err
	}
	if err := leads.RecordEvent(ctx, tx, p.OrgID, leadID, "discovered", &p.UserID, map[string]any{
		"source": "company_db", "company_id": c.ID, "verification_status": c.VerificationStatus}); err != nil {
		return uuid.Nil, err
	}
	return leadID, webhooks.Publish(ctx, tx, p.OrgID, webhooks.LeadCreated,
		map[string]any{"lead_id": leadID, "company_name": in.CompanyName, "source": in.Source})
}

// Report returns the stored enrichment data for a lead, keyed by provider.
func (s *Service) Report(ctx context.Context, orgID, leadID uuid.UUID) (map[string]json.RawMessage, error) {
	rows, err := s.pool.Query(ctx, `SELECT provider, data FROM lead_enrichment WHERE organization_id=$1 AND lead_id=$2`, orgID, leadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var p string
		var d json.RawMessage
		if err := rows.Scan(&p, &d); err != nil {
			return nil, err
		}
		out[p] = d
	}
	return out, rows.Err()
}
