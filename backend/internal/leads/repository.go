package leads

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"leadline/pkg/apperr"
	"leadline/pkg/database"
)

type Repository struct{ db database.DBTX }

func NewRepository(db database.DBTX) *Repository { return &Repository{db: db} }

const selectLead = `
	SELECT l.id, l.company_name, l.website, l.domain, l.industry, l.description, l.country, l.state, l.city,
	       l.address, l.phone, l.business_email::text, l.linkedin_url, l.social_urls, l.employee_count,
	       l.founded_year, l.source, l.source_url, l.import_id, l.lead_score, l.status, l.created_at, l.updated_at,
	       l.owner_id, (SELECT u.full_name FROM users u WHERE u.id = l.owner_id), l.website_quality, l.tech_stack, l.icp_fit, l.enriched_at,
	       COALESCE((SELECT array_agg(t.tag ORDER BY t.tag) FROM lead_tags t WHERE t.lead_id = l.id), '{}'),
	       pc.id, pc.first_name, pc.last_name, pc.email::text, pc.email_status,
	       (SELECT c.name FROM campaign_leads cl JOIN campaigns c ON c.id = cl.campaign_id
	         WHERE cl.lead_id = l.id ORDER BY cl.created_at DESC LIMIT 1)
	FROM leads l
	LEFT JOIN LATERAL (
		SELECT c.id, c.first_name, c.last_name, c.email, c.email_status FROM lead_contacts c
		WHERE c.lead_id = l.id ORDER BY c.is_primary DESC, (c.email IS NULL), c.created_at LIMIT 1
	) pc ON true`

func scanLead(row pgx.Row) (*Lead, error) {
	var l Lead
	var pcID *uuid.UUID
	var pcFirst, pcLast, pcStatus *string
	var pcEmail *string
	err := row.Scan(&l.ID, &l.CompanyName, &l.Website, &l.Domain, &l.Industry, &l.Description, &l.Country, &l.State,
		&l.City, &l.Address, &l.Phone, &l.BusinessEmail, &l.LinkedInURL, &l.SocialURLs, &l.EmployeeCount,
		&l.FoundedYear, &l.Source, &l.SourceURL, &l.ImportID, &l.LeadScore, &l.Status, &l.CreatedAt, &l.UpdatedAt,
		&l.OwnerID, &l.OwnerName, &l.WebsiteQuality, &l.TechStack, &l.ICPFit, &l.EnrichedAt,
		&l.Tags, &pcID, &pcFirst, &pcLast, &pcEmail, &pcStatus, &l.LatestCampaign)
	if err != nil {
		if database.IsNoRows(err) {
			return nil, apperr.NotFound("lead")
		}
		return nil, err
	}
	if pcID != nil {
		l.PrimaryContact = &ContactSummary{ID: *pcID, FirstName: deref(pcFirst), LastName: deref(pcLast), Email: pcEmail, EmailStatus: deref(pcStatus)}
	}
	return &l, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (r *Repository) List(ctx context.Context, orgID uuid.UUID, f Filter, limit, offset int) ([]Lead, int64, error) {
	where, args := f.Where(orgID)
	var total int64
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM leads l WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	n := len(args)
	args = append(args, limit, offset)
	rows, err := r.db.Query(ctx, selectLead+` WHERE `+where+` ORDER BY `+f.OrderBy()+
		` LIMIT $`+strconv.Itoa(n+1)+` OFFSET $`+strconv.Itoa(n+2), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Lead{}
	for rows.Next() {
		l, err := scanLead(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *l)
	}
	return out, total, rows.Err()
}

// Each streams every matching lead to fn, in pages, for exports.
func (r *Repository) Each(ctx context.Context, orgID uuid.UUID, f Filter, fn func(*Lead) error) error {
	const pageSize = 1000
	for offset := 0; ; offset += pageSize {
		page, _, err := r.List(ctx, orgID, f, pageSize, offset)
		if err != nil {
			return err
		}
		for i := range page {
			if err := fn(&page[i]); err != nil {
				return err
			}
		}
		if len(page) < pageSize {
			return nil
		}
	}
}

func (r *Repository) Get(ctx context.Context, orgID, id uuid.UUID) (*Lead, error) {
	return scanLead(r.db.QueryRow(ctx, selectLead+` WHERE l.organization_id = $1 AND l.id = $2`, orgID, id))
}

// IDs returns the IDs of leads matching f (capped at max).
func (r *Repository) IDs(ctx context.Context, orgID uuid.UUID, f Filter, max int) ([]uuid.UUID, error) {
	where, args := f.Where(orgID)
	args = append(args, max)
	rows, err := r.db.Query(ctx, `SELECT l.id FROM leads l WHERE `+where+` ORDER BY l.created_at LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Insert writes a normalized lead and returns its ID.
func Insert(ctx context.Context, db database.DBTX, orgID uuid.UUID, in *Input, domain string, importID *uuid.UUID) (uuid.UUID, error) {
	social, _ := json.Marshal(nonNil(in.SocialURLs))
	var id uuid.UUID
	err := db.QueryRow(ctx, `
		INSERT INTO leads (organization_id, company_name, website, domain, industry, description, country, state, city,
			address, phone, business_email, linkedin_url, social_urls, employee_count, founded_year, source, source_url,
			import_id, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		RETURNING id`,
		orgID, in.CompanyName, in.Website, domain, in.Industry, in.Description, in.Country, in.State, in.City,
		in.Address, in.Phone, nullStr(in.BusinessEmail), in.LinkedInURL, social, in.EmployeeCount, in.FoundedYear,
		in.Source, in.SourceURL, importID, in.Status).Scan(&id)
	return id, err
}

func (r *Repository) Update(ctx context.Context, db database.DBTX, orgID, id uuid.UUID, in *Input, domain string) error {
	social, _ := json.Marshal(nonNil(in.SocialURLs))
	tag, err := db.Exec(ctx, `
		UPDATE leads SET company_name=$3, website=$4, domain=$5, industry=$6, description=$7, country=$8, state=$9,
			city=$10, address=$11, phone=$12, business_email=$13, linkedin_url=$14, social_urls=$15,
			employee_count=$16, founded_year=$17, source=$18, source_url=$19, status=$20, updated_at=now()
		WHERE organization_id=$1 AND id=$2`,
		orgID, id, in.CompanyName, in.Website, domain, in.Industry, in.Description, in.Country, in.State, in.City,
		in.Address, in.Phone, nullStr(in.BusinessEmail), in.LinkedInURL, social, in.EmployeeCount, in.FoundedYear,
		in.Source, in.SourceURL, in.Status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("lead")
	}
	return nil
}

func SetTags(ctx context.Context, db database.DBTX, orgID, leadID uuid.UUID, tags []string) error {
	if _, err := db.Exec(ctx, `DELETE FROM lead_tags WHERE organization_id=$1 AND lead_id=$2 AND NOT (tag = ANY($3))`, orgID, leadID, tags); err != nil {
		return err
	}
	return AddTags(ctx, db, orgID, []uuid.UUID{leadID}, tags)
}

func AddTags(ctx context.Context, db database.DBTX, orgID uuid.UUID, leadIDs []uuid.UUID, tags []string) error {
	if len(tags) == 0 || len(leadIDs) == 0 {
		return nil
	}
	_, err := db.Exec(ctx, `
		INSERT INTO lead_tags (organization_id, lead_id, tag)
		SELECT $1::uuid, l.id, t FROM leads l, unnest($3::text[]) t
		WHERE l.organization_id = $1 AND l.id = ANY($2)
		ON CONFLICT DO NOTHING`, orgID, leadIDs, tags)
	return err
}

func RemoveTags(ctx context.Context, db database.DBTX, orgID uuid.UUID, leadIDs []uuid.UUID, tags []string) error {
	_, err := db.Exec(ctx, `DELETE FROM lead_tags WHERE organization_id=$1 AND lead_id = ANY($2) AND tag = ANY($3)`, orgID, leadIDs, tags)
	return err
}

// RecordEvent appends to a lead's activity timeline.
func RecordEvent(ctx context.Context, db database.DBTX, orgID, leadID uuid.UUID, eventType string, actor *uuid.UUID, data map[string]any) error {
	raw := []byte("{}")
	if data != nil {
		raw, _ = json.Marshal(data)
	}
	_, err := db.Exec(ctx, `INSERT INTO lead_events (organization_id, lead_id, event_type, actor_user_id, data) VALUES ($1,$2,$3,$4,$5)`,
		orgID, leadID, eventType, actor, raw)
	return err
}

func (r *Repository) Events(ctx context.Context, orgID, leadID uuid.UUID, limit int) ([]Event, error) {
	rows, err := r.db.Query(ctx, `
		SELECT e.id, e.event_type, e.actor_user_id, u.full_name, e.data, e.created_at
		FROM lead_events e LEFT JOIN users u ON u.id = e.actor_user_id
		WHERE e.organization_id=$1 AND e.lead_id=$2 ORDER BY e.created_at DESC, e.id DESC LIMIT $3`, orgID, leadID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.EventType, &e.ActorID, &e.ActorName, &e.Data, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

var facetColumns = map[string]string{
	"industry": "l.industry", "city": "l.city", "state": "l.state", "country": "l.country", "source": "l.source",
}

// Facets returns the most common values of a column, for filter dropdowns.
func (r *Repository) Facets(ctx context.Context, orgID uuid.UUID, field string) ([]Facet, error) {
	var q string
	if field == "tag" {
		q = `SELECT tag, count(*) FROM lead_tags WHERE organization_id=$1 GROUP BY tag ORDER BY count(*) DESC, tag LIMIT 200`
	} else {
		col, ok := facetColumns[field]
		if !ok {
			return nil, apperr.Invalid("unknown facet field", nil)
		}
		q = `SELECT ` + col + `, count(*) FROM leads l WHERE l.organization_id=$1 AND ` + col + ` <> ''
			GROUP BY 1 ORDER BY count(*) DESC, 1 LIMIT 200`
	}
	rows, err := r.db.Query(ctx, q, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Facet{}
	for rows.Next() {
		var f Facet
		if err := rows.Scan(&f.Value, &f.Count); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
