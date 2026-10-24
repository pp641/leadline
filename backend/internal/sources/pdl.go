package sources

import (
	"context"
	"net/http"
	"strings"

	"leadline/internal/integrations"
)

// PDL searches People Data Labs' company or person datasets with an
// Elasticsearch-style query (v5 Search APIs).
type PDL struct{ BaseURL string }

func (d *PDL) base() string {
	if d.BaseURL != "" {
		return d.BaseURL
	}
	return "https://api.peopledatalabs.com/v5"
}

func (d *PDL) headers(c integrations.Credentials) map[string]string {
	return map[string]string{"X-Api-Key": c.Get("api_key")}
}

func (d *PDL) Ping(ctx context.Context, c integrations.Credentials) error {
	_, err := call(ctx, "people data labs", http.MethodGet, d.base()+"/autocomplete?field=industry&text=soft&size=1", d.headers(c), nil, nil)
	return err
}

func (d *PDL) Search(ctx context.Context, c integrations.Credentials, p Params) ([]Candidate, error) {
	people := p.Str("mode") == "people"
	field := func(company, person string) string {
		if people {
			return person
		}
		return company
	}
	var must []any
	term := func(f, v string) {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			must = append(must, map[string]any{"term": map[string]string{f: v}})
		}
	}
	term(field("location.country", "location_country"), p.Str("country"))
	term(field("location.region", "location_region"), p.Str("region"))
	term(field("location.locality", "location_locality"), p.Str("city"))
	term(field("size", "job_company_size"), p.Str("size"))
	if v := p.Str("industry"); v != "" {
		must = append(must, map[string]any{"match": map[string]string{field("industry", "job_company_industry"): strings.ToLower(v)}})
	}
	if people {
		var should []any
		for _, t := range p.List("titles") {
			should = append(should, map[string]any{"match_phrase": map[string]string{"job_title": strings.ToLower(t)}})
		}
		if len(should) > 0 {
			must = append(must, map[string]any{"bool": map[string]any{"should": should}})
		}
	}
	if len(must) == 0 {
		return nil, errorf("people data labs", "add at least one filter (industry, location or size)")
	}
	body := map[string]any{"query": map[string]any{"bool": map[string]any{"must": must}}, "size": p.Int("limit", 25, 100)}
	path := "/company/search"
	if people {
		path = "/person/search"
	}
	var out obj
	status, err := call(ctx, "people data labs", http.MethodPost, d.base()+path, d.headers(c), body, &out)
	if status == http.StatusNotFound {
		return []Candidate{}, nil // PDL answers 404 when nothing matches
	}
	if err != nil {
		return nil, err
	}
	if people {
		return pdlPeople(out.arr("data")), nil
	}
	var cands []Candidate
	for _, o := range out.arr("data") {
		loc := o.o("location")
		cands = append(cands, Candidate{
			ExternalID: o.s("id"), CompanyName: titleIfLower(o.s("display_name", "name")), Website: o.s("website"),
			Industry: o.s("industry"), Description: o.s("summary", "headline"),
			Country: loc.s("country"), State: loc.s("region"), City: loc.s("locality"), Address: loc.s("street_address", "name"),
			LinkedInURL: o.s("linkedin_url"), SocialURLs: nonEmptyStrs(o.s("facebook_url"), o.s("twitter_url")),
			EmployeeCount: intPtr(o.n("employee_count")), FoundedYear: intPtr(o.n("founded")),
			SourceURL: o.s("linkedin_url"),
		})
	}
	return cands, nil
}

func pdlPeople(people []obj) []Candidate {
	byCompany := map[string]*Candidate{}
	var order []string
	for _, o := range people {
		name := titleIfLower(o.s("job_company_name"))
		if name == "" {
			continue
		}
		key := strings.ToLower(firstNonEmpty(o.s("job_company_website"), name))
		if byCompany[key] == nil {
			byCompany[key] = &Candidate{
				ExternalID: o.s("job_company_id"), CompanyName: name, Website: o.s("job_company_website"),
				Industry: o.s("job_company_industry"),
				Country:  o.s("job_company_location_country"), State: o.s("job_company_location_region"), City: o.s("job_company_location_locality"),
				LinkedInURL: o.s("job_company_linkedin_url"), FoundedYear: intPtr(o.n("job_company_founded")),
				EmployeeCount: intPtr(o.n("job_company_employee_count")),
			}
			order = append(order, key)
		}
		first, last := titleIfLower(o.s("first_name")), titleIfLower(o.s("last_name"))
		if first == "" && last == "" {
			first, last = splitName(titleIfLower(o.s("full_name")))
		}
		email := o.s("work_email")
		if email == "" {
			email = o.s("emails.0.address")
		}
		byCompany[key].Contacts = append(byCompany[key].Contacts, Contact{
			FirstName: first, LastName: last, Title: titleIfLower(o.s("job_title")), Email: email,
			LinkedInURL: o.s("linkedin_url"), Phone: o.s("mobile_phone"),
		})
	}
	out := make([]Candidate, 0, len(order))
	for _, k := range order {
		out = append(out, *byCompany[k])
	}
	return out
}
