package sources

import (
	"context"
	"net/http"
	"strings"

	"leadline/internal/integrations"
)

// Apollo searches companies (mixed_companies/search) or people
// (mixed_people/api_search). People search returns no emails; with
// reveal_emails on, people are enriched through people/bulk_match, which
// spends Apollo credits.
type Apollo struct{ BaseURL string }

func (a *Apollo) base() string {
	if a.BaseURL != "" {
		return a.BaseURL
	}
	return "https://api.apollo.io/api/v1"
}

func (a *Apollo) headers(c integrations.Credentials) map[string]string {
	return map[string]string{"x-api-key": c.Get("api_key"), "Cache-Control": "no-cache"}
}

func (a *Apollo) Ping(ctx context.Context, c integrations.Credentials) error {
	var out obj
	if _, err := call(ctx, "apollo", http.MethodGet, a.base()+"/auth/health", a.headers(c), nil, &out); err != nil {
		return err
	}
	if v, ok := out["is_logged_in"].(bool); ok && !v {
		return errorf("apollo", "the API key was not accepted")
	}
	return nil
}

func (a *Apollo) Search(ctx context.Context, c integrations.Credentials, p Params) ([]Candidate, error) {
	limit := p.Int("limit", 25, 100)
	body := map[string]any{"page": 1, "per_page": limit}
	if v := p.List("locations"); len(v) > 0 {
		body["organization_locations"] = v
	}
	if v := p.Str("size"); v != "" {
		body["organization_num_employees_ranges"] = []string{v}
	}
	if p.Str("mode") == "people" {
		return a.people(ctx, c, p, body)
	}
	if v := p.List("keywords"); len(v) > 0 {
		body["q_organization_keyword_tags"] = v
	}
	var out obj
	if _, err := call(ctx, "apollo", http.MethodPost, a.base()+"/mixed_companies/search", a.headers(c), body, &out); err != nil {
		return nil, err
	}
	orgs := append(out.arr("organizations"), out.arr("accounts")...)
	cands := make([]Candidate, 0, len(orgs))
	for _, o := range orgs {
		cands = append(cands, apolloCompany(o))
	}
	return cands, nil
}

func apolloCompany(o obj) Candidate {
	c := Candidate{
		ExternalID: o.s("id", "organization_id"), CompanyName: o.s("name"),
		Website: o.s("website_url"), Domain: o.s("primary_domain", "domain"),
		Industry: o.s("industry"), Description: o.s("short_description", "seo_description"),
		Country: o.s("country"), State: o.s("state"), City: o.s("city"),
		Address:       o.s("raw_address", "street_address"),
		Phone:         o.s("phone", "sanitized_phone", "primary_phone.number"),
		LinkedInURL:   o.s("linkedin_url"),
		SocialURLs:    nonEmptyStrs(o.s("twitter_url"), o.s("facebook_url")),
		EmployeeCount: intPtr(o.n("estimated_num_employees")), FoundedYear: intPtr(o.n("founded_year")),
		TechStack: o.strs("technology_names"),
	}
	if c.LinkedInURL != "" {
		c.SourceURL = c.LinkedInURL
	}
	if c.ExternalID != "" {
		c.SourceURL = "https://app.apollo.io/#/organizations/" + c.ExternalID
	}
	return c
}

func (a *Apollo) people(ctx context.Context, c integrations.Credentials, p Params, body map[string]any) ([]Candidate, error) {
	if v := p.List("titles"); len(v) > 0 {
		body["person_titles"] = v
	}
	if v := p.Str("keywords"); v != "" {
		body["q_keywords"] = v
	}
	var out obj
	if _, err := call(ctx, "apollo", http.MethodPost, a.base()+"/mixed_people/api_search", a.headers(c), body, &out); err != nil {
		return nil, err
	}
	people := out.arr("people")
	if p.Bool("reveal_emails") && len(people) > 0 {
		var enriched []obj
		for i := 0; i < len(people); i += 10 {
			end := min(i+10, len(people))
			var details []map[string]string
			for _, person := range people[i:end] {
				details = append(details, map[string]string{"id": person.s("id")})
			}
			var res obj
			if _, err := call(ctx, "apollo", http.MethodPost, a.base()+"/people/bulk_match", a.headers(c),
				map[string]any{"details": details, "reveal_personal_emails": false}, &res); err != nil {
				return nil, err
			}
			enriched = append(enriched, res.arr("matches")...)
		}
		if len(enriched) > 0 {
			people = enriched
		}
	}
	// One candidate per company, with its people as contacts.
	byCompany := map[string]*Candidate{}
	var order []string
	for _, person := range people {
		org := person.o("organization")
		cand := apolloCompany(org)
		if cand.CompanyName == "" {
			cand.CompanyName = person.s("organization_name")
		}
		if cand.CompanyName == "" {
			continue
		}
		if cand.City == "" {
			cand.City, cand.State, cand.Country = person.s("city"), person.s("state"), person.s("country")
		}
		key := strings.ToLower(firstNonEmpty(cand.Domain, cand.CompanyName))
		if byCompany[key] == nil {
			byCompany[key] = &cand
			order = append(order, key)
		}
		status := strings.ToLower(person.s("email_status"))
		if status == "verified" {
			status = "valid"
		}
		byCompany[key].Contacts = append(byCompany[key].Contacts, Contact{
			FirstName: person.s("first_name"), LastName: person.s("last_name", "last_name_obfuscated"), Title: person.s("title"),
			Email: person.s("email"), EmailStatus: status, LinkedInURL: person.s("linkedin_url"),
			Phone: person.s("sanitized_phone", "phone_numbers.0.sanitized_number"),
		})
	}
	cands := make([]Candidate, 0, len(order))
	for _, k := range order {
		cands = append(cands, *byCompany[k])
	}
	return cands, nil
}

func nonEmptyStrs(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
