package sources

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"leadline/internal/integrations"
)

// Crunchbase searches organizations with the v4 Search API. Location and
// category names are resolved to Crunchbase UUIDs through Autocomplete first.
type Crunchbase struct{ BaseURL string }

func (cb *Crunchbase) base() string {
	if cb.BaseURL != "" {
		return cb.BaseURL
	}
	return "https://api.crunchbase.com/api/v4"
}

func (cb *Crunchbase) headers(c integrations.Credentials) map[string]string {
	return map[string]string{"X-cb-user-key": c.Get("api_key")}
}

func (cb *Crunchbase) Ping(ctx context.Context, c integrations.Credentials) error {
	_, err := call(ctx, "crunchbase", http.MethodGet, cb.base()+"/entities/organizations/crunchbase?field_ids=identifier", cb.headers(c), nil, nil)
	return err
}

func (cb *Crunchbase) resolve(ctx context.Context, c integrations.Credentials, collection, q string) (string, error) {
	var out obj
	u := cb.base() + "/autocompletes?" + url.Values{"query": {q}, "collection_ids": {collection}, "limit": {"1"}}.Encode()
	if _, err := call(ctx, "crunchbase", http.MethodGet, u, cb.headers(c), nil, &out); err != nil {
		return "", err
	}
	for _, e := range out.arr("entities") {
		if id := e.s("identifier.uuid"); id != "" {
			return id, nil
		}
	}
	return "", errorf("crunchbase", "no %s matches %q", strings.TrimSuffix(collection, "s"), q)
}

func (cb *Crunchbase) Search(ctx context.Context, c integrations.Credentials, p Params) ([]Candidate, error) {
	pred := func(field string, values ...string) map[string]any {
		return map[string]any{"type": "predicate", "field_id": field, "operator_id": "includes", "values": values}
	}
	query := []any{pred("facet_ids", "company")}
	if v := p.Str("location"); v != "" {
		id, err := cb.resolve(ctx, c, "locations", v)
		if err != nil {
			return nil, err
		}
		query = append(query, pred("location_identifiers", id))
	}
	if v := p.Str("category"); v != "" {
		id, err := cb.resolve(ctx, c, "categories", v)
		if err != nil {
			return nil, err
		}
		query = append(query, pred("categories", id))
	}
	if v := p.Str("funding"); v != "" {
		query = append(query, pred("last_funding_type", v))
	}
	if v := p.Str("size"); v != "" {
		query = append(query, pred("num_employees_enum", v))
	}
	body := map[string]any{
		"field_ids": []string{"identifier", "short_description", "website_url", "location_identifiers", "categories",
			"num_employees_enum", "founded_on", "last_funding_type", "funding_total", "linkedin", "facebook", "twitter",
			"contact_email", "phone_number"},
		"query": query, "limit": p.Int("limit", 25, 100),
		"order": []any{map[string]string{"field_id": "rank_org", "sort": "asc"}},
	}
	var out obj
	if _, err := call(ctx, "crunchbase", http.MethodPost, cb.base()+"/searches/organizations", cb.headers(c), body, &out); err != nil {
		return nil, err
	}
	var cands []Candidate
	for _, e := range out.arr("entities") {
		pr := e.o("properties")
		cand := Candidate{
			ExternalID: e.s("uuid", "properties.identifier.uuid"), CompanyName: pr.s("identifier.value"),
			Website: pr.s("website_url", "website"), Description: pr.s("short_description"),
			Email: pr.s("contact_email"), Phone: pr.s("phone_number"),
			LinkedInURL: pr.s("linkedin"), SocialURLs: nonEmptyStrs(pr.s("facebook"), pr.s("twitter")),
			FoundedYear: intPtr(yearOf(pr.s("founded_on"))), EmployeeCount: intPtr(employeesFromEnum(pr.s("num_employees_enum"))),
			Extra: map[string]any{},
		}
		if cats := pr.strs("categories"); len(cats) > 0 {
			cand.Industry = cats[0]
			cand.Extra["categories"] = cats
		}
		for _, loc := range pr.arr("location_identifiers") {
			switch loc.s("location_type") {
			case "city":
				cand.City = loc.s("value")
			case "region":
				cand.State = loc.s("value")
			case "country":
				cand.Country = loc.s("value")
			}
		}
		if f := pr.s("last_funding_type"); f != "" {
			cand.Extra["last_funding_type"] = strings.ReplaceAll(f, "_", " ")
		}
		if n := pr.o("funding_total").n("value_usd"); n > 0 {
			cand.Extra["funding_total_usd"] = n
		}
		if slug := pr.s("identifier.permalink"); slug != "" {
			cand.SourceURL = "https://www.crunchbase.com/organization/" + slug
		}
		cands = append(cands, cand)
	}
	return cands, nil
}

// employeesFromEnum turns "c_00011_00050" into the range midpoint (30).
func employeesFromEnum(e string) int {
	parts := strings.Split(e, "_")
	if len(parts) != 3 {
		return 0
	}
	lo, err1 := strconv.Atoi(parts[1])
	hi, err2 := strconv.Atoi(parts[2])
	if err1 != nil {
		return 0
	}
	if err2 != nil || hi < lo {
		return lo
	}
	return (lo + hi) / 2
}
