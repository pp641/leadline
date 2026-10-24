package sources

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"leadline/internal/integrations"
)

// ───────────── Lusha ─────────────

// Lusha enriches company domains (v2 Company API). Each lookup uses credits.
type Lusha struct{ BaseURL string }

func (l *Lusha) base() string {
	if l.BaseURL != "" {
		return l.BaseURL
	}
	return "https://api.lusha.com"
}

func (l *Lusha) headers(c integrations.Credentials) map[string]string {
	return map[string]string{"api_key": c.Get("api_key")}
}

func (l *Lusha) Ping(ctx context.Context, c integrations.Credentials) error {
	status, err := call(ctx, "lusha", http.MethodGet, l.base()+"/account/usage", l.headers(c), nil, nil)
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return err
	}
	if err != nil && status == 0 {
		return err
	}
	return nil // any authenticated answer means the key works
}

func (l *Lusha) Search(ctx context.Context, c integrations.Credentials, p Params) ([]Candidate, error) {
	domains := p.Domains("domains", 50)
	if len(domains) == 0 {
		return nil, errorf("lusha", "enter at least one company domain")
	}
	var cands []Candidate
	for _, d := range domains {
		var out obj
		status, err := call(ctx, "lusha", http.MethodGet, l.base()+"/v2/company?"+url.Values{"domain": {d}}.Encode(), l.headers(c), nil, &out)
		if status == http.StatusNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		o := out.o("data")
		if len(o) == 0 {
			o = out
		}
		loc := o.o("location")
		cands = append(cands, Candidate{
			ExternalID: o.s("id", "companyId"), CompanyName: firstNonEmpty(o.s("name", "companyName"), d),
			Domain: firstNonEmpty(o.s("domain", "fqdn"), d), Website: o.s("website"),
			Industry: o.s("mainIndustry", "industry", "industryPrimaryGroupDetails.name"), Description: o.s("description"),
			Country: loc.s("country", "countryIso2"), State: loc.s("state"), City: loc.s("city"),
			Address:       loc.s("fullLocation", "rawLocation"),
			LinkedInURL:   firstNonEmpty(o.s("social.linkedin.url", "social.linkedin", "linkedin", "linkedinUrl")),
			EmployeeCount: intPtr(o.n("employees", "companySize.0", "size")), FoundedYear: intPtr(yearOf(o.s("founded", "foundedYear"))),
			SourceURL: "https://www.lusha.com",
		})
	}
	return cands, nil
}

// ───────────── Snov.io ─────────────

// Snov uses OAuth client credentials and the v2 Domain Search: company info,
// generic domain emails and prospects (names + positions) per domain. Each
// search is a start/result task pair.
type Snov struct{ BaseURL string }

func (s *Snov) base() string {
	if s.BaseURL != "" {
		return s.BaseURL
	}
	return "https://api.snov.io"
}

func (s *Snov) token(ctx context.Context, c integrations.Credentials) (string, error) {
	var out obj
	if _, err := callForm(ctx, "snov.io", s.base()+"/v1/oauth/access_token", url.Values{
		"grant_type": {"client_credentials"}, "client_id": {c.Get("client_id")}, "client_secret": {c.Get("client_secret")}}, &out); err != nil {
		return "", err
	}
	tok := out.s("access_token")
	if tok == "" {
		return "", errorf("snov.io", "no access token returned — check the client ID and secret")
	}
	return tok, nil
}

func (s *Snov) Ping(ctx context.Context, c integrations.Credentials) error {
	_, err := s.token(ctx, c)
	return err
}

// task starts a v2 search and polls its result.
func (s *Snov) task(ctx context.Context, tok, kind, domain string) (obj, error) {
	h := map[string]string{"Authorization": "Bearer " + tok}
	var start obj
	if _, err := call(ctx, "snov.io", http.MethodPost, s.base()+"/v2/domain-search"+kind+"/start?"+url.Values{"domain": {domain}}.Encode(), h,
		map[string]string{"domain": domain}, &start); err != nil {
		return nil, err
	}
	hash := start.s("meta.task_hash", "data.task_hash", "task_hash")
	resultURL := start.s("links.result", "data.result", "result")
	if resultURL == "" {
		if hash == "" {
			return nil, errorf("snov.io", "search for %s did not start", domain)
		}
		resultURL = s.base() + "/v2/domain-search" + kind + "/result/" + hash
	}
	for attempt := 0; attempt < 12; attempt++ {
		var res obj
		if _, err := call(ctx, "snov.io", http.MethodGet, resultURL, h, nil, &res); err != nil {
			return nil, err
		}
		st := strings.ToLower(res.s("status", "meta.status", "data.status"))
		if st == "" || st == "completed" || st == "success" || st == "done" {
			return res, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1500 * time.Millisecond):
		}
	}
	return nil, errorf("snov.io", "search for %s is still running — try again in a minute", domain)
}

func (s *Snov) Search(ctx context.Context, c integrations.Credentials, p Params) ([]Candidate, error) {
	domains := p.Domains("domains", 10)
	if len(domains) == 0 {
		return nil, errorf("snov.io", "enter at least one company domain")
	}
	tok, err := s.token(ctx, c)
	if err != nil {
		return nil, err
	}
	var cands []Candidate
	for _, d := range domains {
		info, err := s.task(ctx, tok, "", d)
		if err != nil {
			return nil, err
		}
		o := info.o("data")
		if len(o) == 0 {
			o = info
		}
		cand := Candidate{
			CompanyName: firstNonEmpty(o.s("company_name", "name", "company"), d), Domain: d, Website: o.s("website"),
			Industry: o.s("industry"), Country: o.s("country"), State: o.s("region", "state"), City: o.s("city", "locality"),
			Phone: o.s("hq_phone", "phone"), EmployeeCount: intPtr(o.n("size", "employees")), FoundedYear: intPtr(yearOf(o.s("founded", "founded_year"))),
			SourceURL: "https://app.snov.io/domain-search?name=" + d,
		}
		if emails, err := s.task(ctx, tok, "/domain-emails", d); err == nil {
			list := emails.arr("data", "emails", "data.emails")
			for _, e := range list {
				cand.Contacts = append(cand.Contacts, Contact{Email: e.s("email", "value"), EmailStatus: strings.ToLower(e.s("status", "smtp_status"))})
			}
			if len(list) == 0 {
				for _, e := range emails.strs("data", "emails") {
					cand.Contacts = append(cand.Contacts, Contact{Email: e})
				}
			}
		}
		if prospects, err := s.task(ctx, tok, "/prospects", d); err == nil {
			for _, pr := range prospects.arr("data", "prospects", "data.prospects") {
				cand.Contacts = append(cand.Contacts, Contact{
					FirstName: pr.s("first_name"), LastName: pr.s("last_name"), Title: pr.s("position"), LinkedInURL: pr.s("source_page"),
				})
			}
		}
		cands = append(cands, cand)
	}
	return cands, nil
}
