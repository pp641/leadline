// Package catalog connects Leadline to the company database built by the
// separate company-scraper project. Leadline no longer searches providers or
// crawls websites itself: the scraper discovers companies, analyses their
// websites and verifies their contact details, and Leadline searches that
// database (read-only) and imports the companies a user picks as leads.
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/pkg/geo"
)

// Query selects companies. It is usually derived from an ICP.
type Query struct {
	Industries []string `json:"industries"`
	Countries  []string `json:"countries"`
	States     []string `json:"states"`
	Cities     []string `json:"cities"`
	Keywords   []string `json:"keywords"` // matched in name, industry and description
	// Website: any | missing | missing_or_poor | poor | present | good
	Website      string `json:"website"`
	VerifiedOnly bool   `json:"verified_only"`
	RequireEmail bool   `json:"require_email"`
	Limit        int    `json:"limit"`
}

// Company is a row of the scraper's companies table.
type Company struct {
	ID                 uuid.UUID       `json:"id"`
	Name               string          `json:"name"`
	Industry           string          `json:"industry"`
	Description        string          `json:"description"`
	Website            string          `json:"website"`
	Domain             string          `json:"domain"`
	Email              *string         `json:"email"`
	Phone              string          `json:"phone"`
	Address            string          `json:"address"`
	City               string          `json:"city"`
	State              string          `json:"state"`
	Country            string          `json:"country"`
	LinkedInURL        string          `json:"linkedin_url"`
	SocialURLs         []string        `json:"social_urls"`
	EmployeeCount      *int            `json:"employee_count"`
	FoundedYear        *int            `json:"founded_year"`
	Source             string          `json:"source"`
	SourceURL          string          `json:"source_url"`
	Attribution        string          `json:"attribution"`
	WebsiteStatus      string          `json:"website_status"`
	WebsiteQuality     string          `json:"website_quality"`
	TechStack          []string        `json:"tech_stack"`
	EmailStatus        string          `json:"email_status"`
	PhoneValid         *bool           `json:"phone_valid"`
	VerificationStatus string          `json:"verification_status"`
	Verification       json.RawMessage `json:"verification,omitempty"`
	WebsiteReport      json.RawMessage `json:"website_report,omitempty"`
	VerifiedAt         *time.Time      `json:"verified_at"`
}

// Source is where companies come from; tests substitute a fake.
type Source interface {
	Search(ctx context.Context, q Query) ([]Company, error)
	Get(ctx context.Context, ids []uuid.UUID) ([]Company, error)
	Ping(ctx context.Context) error
}

// PGSource reads the scraper's PostgreSQL database. Connect it with the
// read-only leadline_reader role; Leadline never writes there.
type PGSource struct{ pool *pgxpool.Pool }

// NewPGSource does not connect eagerly, so Leadline starts even while the
// scraper is down; queries fail until it is reachable.
func NewPGSource(url string) (*PGSource, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse SCRAPER_DATABASE_URL: %w", err)
	}
	cfg.MaxConns = 5
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	return &PGSource{pool: pool}, nil
}

func (s *PGSource) Close() { s.pool.Close() }

func (s *PGSource) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

const companyCols = `id, name, industry, description, website, domain, email, phone, address, city, state, country,
	linkedin_url, social_urls, employee_count, founded_year, source, source_url, attribution, website_status,
	website_quality, tech_stack, email_status, phone_valid, verification_status, verification, website_report, verified_at`

func scanCompanies(rows pgx.Rows) ([]Company, error) {
	defer rows.Close()
	out := []Company{}
	for rows.Next() {
		var c Company
		var social []byte
		if err := rows.Scan(&c.ID, &c.Name, &c.Industry, &c.Description, &c.Website, &c.Domain, &c.Email, &c.Phone,
			&c.Address, &c.City, &c.State, &c.Country, &c.LinkedInURL, &social, &c.EmployeeCount, &c.FoundedYear,
			&c.Source, &c.SourceURL, &c.Attribution, &c.WebsiteStatus, &c.WebsiteQuality, &c.TechStack, &c.EmailStatus,
			&c.PhoneValid, &c.VerificationStatus, &c.Verification, &c.WebsiteReport, &c.VerifiedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(social, &c.SocialURLs)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PGSource) Get(ctx context.Context, ids []uuid.UUID) ([]Company, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+companyCols+` FROM companies WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	return scanCompanies(rows)
}

func (s *PGSource) Search(ctx context.Context, q Query) ([]Company, error) {
	where, args := q.where()
	args = append(args, q.Limit)
	rows, err := s.pool.Query(ctx, `SELECT `+companyCols+` FROM companies WHERE `+where+`
		ORDER BY (verification_status = 'verified') DESC, (email IS NOT NULL) DESC, updated_at DESC, id
		LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, err
	}
	return scanCompanies(rows)
}

// where compiles the query to SQL conditions over the companies table.
func (q Query) where() (string, []any) {
	var conds []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if ps := patterns(q.Industries, true); len(ps) > 0 {
		conds = append(conds, "lower(industry) LIKE ANY("+arg(ps)+")")
	}
	// Location narrows level by level (country, then state, then city), so
	// "India + Uttar Pradesh + Noida" means Noida, not all of India.
	for _, l := range []struct {
		col  string
		vals []string
	}{{"country", geo.Variants(q.Countries)}, {"state", q.States}, {"city", q.Cities}} {
		if v := lower(l.vals); len(v) > 0 {
			conds = append(conds, "lower(trim("+l.col+")) = ANY("+arg(v)+")")
		}
	}
	if ps := patterns(q.Keywords, false); len(ps) > 0 {
		p := arg(ps)
		conds = append(conds, "(lower(name) LIKE ANY("+p+") OR lower(industry) LIKE ANY("+p+") OR lower(description) LIKE ANY("+p+"))")
	}
	switch q.Website {
	case "missing":
		conds = append(conds, "website = ''")
	case "present":
		conds = append(conds, "website <> ''")
	case "good", "poor":
		conds = append(conds, "website_quality = "+arg(q.Website))
	case "missing_or_poor":
		conds = append(conds, "(website = '' OR website_quality IN ('none','poor'))")
	}
	if q.VerifiedOnly {
		conds = append(conds, "verification_status = 'verified'")
	}
	if q.RequireEmail {
		conds = append(conds, "email IS NOT NULL AND email_status NOT IN ('invalid','disposable')")
	}
	if len(conds) == 0 {
		return "true", args
	}
	return strings.Join(conds, " AND "), args
}

func lower(vals []string) []string {
	var out []string
	for _, v := range vals {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// patterns turns words into LIKE patterns; singular forms let "restaurants" match "restaurant".
func patterns(vals []string, singular bool) []string {
	var out []string
	for _, v := range lower(vals) {
		if singular && len(v) > 3 {
			v = strings.TrimSuffix(v, "s")
		}
		v = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(v)
		out = append(out, "%"+v+"%")
	}
	return out
}

// Faceter is implemented by sources that can list the values present in
// their data, for the ICP Builder's dropdowns.
type Faceter interface {
	FacetRows(ctx context.Context) (FacetRows, error)
}

// FacetRows are raw value counts, before spelling is merged.
type FacetRows struct {
	Places     []PlaceCount
	Industries map[string]int
	TechStack  map[string]int
}

// PlaceCount is the number of companies at one country/state/city.
type PlaceCount struct {
	Country, State, City string
	Count                int
}

func (s *PGSource) FacetRows(ctx context.Context) (FacetRows, error) {
	return queryFacetRows(ctx, s.pool, "companies", "industry", "", nil)
}

// queryFacetRows counts locations, industries and technologies in a table
// shaped like companies or leads; filter and args scope it (e.g. to an org).
func queryFacetRows(ctx context.Context, db interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, table, industryCol, filter string, args []any) (FacetRows, error) {
	out := FacetRows{Industries: map[string]int{}, TechStack: map[string]int{}}
	where := "true"
	if filter != "" {
		where = filter
	}
	rows, err := db.Query(ctx, `SELECT trim(country), trim(state), trim(city), count(*) FROM `+table+` WHERE `+where+` GROUP BY 1,2,3`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var p PlaceCount
		if err := rows.Scan(&p.Country, &p.State, &p.City, &p.Count); err != nil {
			rows.Close()
			return out, err
		}
		out.Places = append(out.Places, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	for _, f := range []struct {
		expr string
		dst  map[string]int
	}{{"lower(trim(" + industryCol + "))", out.Industries}, {"lower(trim(unnest(tech_stack)))", out.TechStack}} {
		rows, err := db.Query(ctx, `SELECT v, count(*) FROM (SELECT `+f.expr+` AS v FROM `+table+` WHERE `+where+`) t WHERE v <> '' GROUP BY 1`, args...)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var v string
			var n int
			if err := rows.Scan(&v, &n); err != nil {
				rows.Close()
				return out, err
			}
			f.dst[v] += n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return out, err
		}
	}
	return out, nil
}
