package sources

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"leadline/internal/integrations"
)

// Hunter finds the emails behind company domains (Domain Search) or discovers
// companies from a description (Discover, which costs no credits).
type Hunter struct{ BaseURL string }

func (h *Hunter) base() string {
	if h.BaseURL != "" {
		return h.BaseURL
	}
	return "https://api.hunter.io/v2"
}

func (h *Hunter) Ping(ctx context.Context, c integrations.Credentials) error {
	_, err := call(ctx, "hunter", http.MethodGet, h.base()+"/account?"+url.Values{"api_key": {c.Get("api_key")}}.Encode(), nil, nil, nil)
	return err
}

func (h *Hunter) Search(ctx context.Context, c integrations.Credentials, p Params) ([]Candidate, error) {
	key := c.Get("api_key")
	if p.Str("mode") == "discover" {
		q := p.Str("query")
		if q == "" {
			return nil, errorf("hunter", "describe the companies to discover")
		}
		var out obj
		if _, err := call(ctx, "hunter", http.MethodPost, h.base()+"/discover?"+url.Values{"api_key": {key}}.Encode(), nil,
			map[string]any{"query": q}, &out); err != nil {
			return nil, err
		}
		var cands []Candidate
		for _, d := range out.arr("data") {
			cands = append(cands, Candidate{CompanyName: firstNonEmpty(d.s("organization"), d.s("domain")), Domain: d.s("domain"),
				SourceURL: "https://hunter.io/search/" + d.s("domain")})
		}
		return cands, nil
	}
	domains := p.Domains("domains", 25)
	if len(domains) == 0 {
		return nil, errorf("hunter", "enter at least one company domain")
	}
	limit := p.Int("limit", 10, 100)
	var cands []Candidate
	for _, d := range domains {
		var out obj
		u := h.base() + "/domain-search?" + url.Values{"domain": {d}, "limit": {strconv.Itoa(limit)}, "api_key": {key}}.Encode()
		if _, err := call(ctx, "hunter", http.MethodGet, u, nil, nil, &out); err != nil {
			return nil, err
		}
		data := out.o("data")
		cand := Candidate{
			CompanyName: firstNonEmpty(data.s("organization"), d), Domain: firstNonEmpty(data.s("domain"), d),
			Industry: data.s("industry"), Description: data.s("description"),
			Country: data.s("country"), State: data.s("state"), City: data.s("city"),
			Address:     strings.Join(nonEmptyStrs(data.s("street"), data.s("postal_code")), ", "),
			LinkedInURL: data.s("linkedin"), SocialURLs: nonEmptyStrs(data.s("twitter"), data.s("facebook")),
			EmployeeCount: intPtr(data.n("headcount")), TechStack: data.strs("technologies"),
			SourceURL: "https://hunter.io/search/" + d,
		}
		for _, e := range data.arr("emails") {
			status := strings.ToLower(e.s("verification.status"))
			switch status {
			case "accept_all":
				status = "risky"
			case "":
				status = "unknown"
			}
			cand.Contacts = append(cand.Contacts, Contact{
				FirstName: e.s("first_name"), LastName: e.s("last_name"), Title: e.s("position"),
				Email: e.s("value"), EmailStatus: status, Phone: e.s("phone_number"), LinkedInURL: e.s("linkedin"),
				Confidence: float32(e.n("confidence")) / 100,
			})
		}
		cands = append(cands, cand)
	}
	return cands, nil
}
