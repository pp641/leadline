// Package sources imports leads from external data providers (Apollo, People
// Data Labs, Google Places, Crunchbase, Lusha, Hunter, Snov.io). Each provider
// maps its own response format onto Candidate — the shape of a Leadline lead
// plus its contacts — so search results can be previewed, de-duplicated
// against existing leads and imported the same way whatever their source.
package sources

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"leadline/internal/integrations"
	"leadline/pkg/geo"
)

// Candidate is one company found by a provider, normalized to lead fields.
type Candidate struct {
	ExternalID    string         `json:"external_id"`
	CompanyName   string         `json:"company_name"`
	Website       string         `json:"website"`
	Domain        string         `json:"domain"`
	Industry      string         `json:"industry"`
	Description   string         `json:"description"`
	Country       string         `json:"country"`
	State         string         `json:"state"`
	City          string         `json:"city"`
	Address       string         `json:"address"`
	Phone         string         `json:"phone"`
	Email         string         `json:"email"`
	LinkedInURL   string         `json:"linkedin_url"`
	SocialURLs    []string       `json:"social_urls"`
	EmployeeCount *int           `json:"employee_count"`
	FoundedYear   *int           `json:"founded_year"`
	TechStack     []string       `json:"tech_stack"`
	SourceURL     string         `json:"source_url"`
	Contacts      []Contact      `json:"contacts"`
	Extra         map[string]any `json:"extra,omitempty"` // provider-specific facts (funding, rating, …)
}

// Contact is a person at the company.
type Contact struct {
	FirstName   string  `json:"first_name"`
	LastName    string  `json:"last_name"`
	Title       string  `json:"title"`
	Email       string  `json:"email"`
	EmailStatus string  `json:"email_status"` // provider's verdict: valid | risky | invalid | unknown | ""
	Phone       string  `json:"phone"`
	LinkedInURL string  `json:"linkedin_url"`
	Confidence  float32 `json:"confidence"` // 0–1
}

// Params are the values of a provider's search form.
type Params map[string]any

func (p Params) Str(k string) string {
	switch v := p[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func (p Params) Bool(k string) bool {
	v, ok := p[k].(bool)
	return (ok && v) || p.Str(k) == "true"
}

func (p Params) Int(k string, def, max int) int {
	n, err := strconv.Atoi(p.Str(k))
	if err != nil || n <= 0 {
		n = def
	}
	if n > max {
		n = max
	}
	return n
}

// List reads a list field: a JSON array, or text separated by commas/newlines.
func (p Params) List(k string) []string {
	var raw []string
	switch v := p[k].(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				raw = append(raw, s)
			}
		}
	case string:
		raw = strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '\n' || r == ';' })
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s != "" && !seen[strings.ToLower(s)] {
			seen[strings.ToLower(s)] = true
			out = append(out, s)
		}
	}
	return out
}

// Domains reads a list of domains, accepting URLs and emails too.
func (p Params) Domains(k string, max int) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range p.List(k) {
		s = strings.ToLower(s)
		if at := strings.LastIndex(s, "@"); at >= 0 {
			s = s[at+1:]
		}
		s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
		s = strings.TrimPrefix(s, "www.")
		if i := strings.IndexAny(s, "/?#"); i >= 0 {
			s = s[:i]
		}
		if strings.Contains(s, ".") && !strings.ContainsAny(s, " \t") && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
		if len(out) == max {
			break
		}
	}
	return out
}

// Source is implemented by every importable provider.
type Source interface {
	Ping(ctx context.Context, c integrations.Credentials) error
	Search(ctx context.Context, c integrations.Credentials, p Params) ([]Candidate, error)
}

// Registry of importable providers, keyed by integration kind.
var Registry = map[string]Source{
	integrations.KindApollo:     &Apollo{},
	integrations.KindPDL:        &PDL{},
	integrations.KindPlaces:     &Places{},
	integrations.KindCrunchbase: &Crunchbase{},
	integrations.KindLusha:      &Lusha{},
	integrations.KindHunter:     &Hunter{},
	integrations.KindSnov:       &Snov{},
}

// RegisterTesters lets Integrations → Test check lead-source credentials.
func RegisterTesters(r *integrations.Registry) {
	for kind, src := range Registry {
		r.Testers[kind] = src.Ping
	}
}

// ───────────── normalization ─────────────

var placeholderEmailDomains = map[string]bool{
	"example.com": true, "example.org": true, "domain.com": true, "yourdomain.com": true, "yoursite.com": true,
	"mysite.com": true, "email.com": true, "company.com": true, "website.com": true, "sentry.io": true,
}

// cleanEmail lower-cases an address and drops obvious placeholders and
// Apollo's "email_not_unlocked@domain.com" markers.
func cleanEmail(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	at := strings.LastIndex(s, "@")
	if at <= 0 || strings.Contains(s, "not_unlocked") || placeholderEmailDomains[s[at+1:]] {
		return ""
	}
	return s
}

// titleIfLower capitalizes all-lower-case place names ("greater noida" → "Greater Noida").
func titleIfLower(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s != strings.ToLower(s) {
		return s
	}
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// country returns the English country name for a name or ISO code.
func country(s string) string {
	if code := geo.Code(s); code != "" {
		return geo.Name(code)
	}
	return titleIfLower(s)
}

func websiteFrom(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		s = "https://" + s
	}
	return s
}

func intPtr(n int) *int {
	if n <= 0 {
		return nil
	}
	return &n
}

// splitName splits "Jane van Doe" into ("Jane", "van Doe").
func splitName(full string) (string, string) {
	full = strings.TrimSpace(full)
	first, rest, _ := strings.Cut(full, " ")
	return first, strings.TrimSpace(rest)
}

// Normalize tidies a candidate in place so every provider's output looks alike.
func Normalize(c *Candidate) {
	c.CompanyName = strings.TrimSpace(c.CompanyName)
	c.Website = websiteFrom(c.Website)
	if c.Website == "" && c.Domain != "" {
		c.Website = websiteFrom(c.Domain)
	}
	c.Domain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(c.Domain)), "www.")
	c.Industry = strings.ToLower(strings.TrimSpace(c.Industry))
	c.Country = country(c.Country)
	c.State = titleIfLower(c.State)
	c.City = titleIfLower(c.City)
	c.Email = cleanEmail(c.Email)
	c.LinkedInURL = websiteFrom(c.LinkedInURL)
	var social []string
	for _, u := range c.SocialURLs {
		if u = websiteFrom(u); u != "" && u != c.LinkedInURL {
			social = append(social, u)
		}
	}
	c.SocialURLs = social
	var contacts []Contact
	seen := map[string]bool{}
	for _, p := range c.Contacts {
		p.FirstName, p.LastName, p.Title = strings.TrimSpace(p.FirstName), strings.TrimSpace(p.LastName), strings.TrimSpace(p.Title)
		p.Email = cleanEmail(p.Email)
		p.LinkedInURL = websiteFrom(p.LinkedInURL)
		key := p.Email
		if key == "" {
			key = strings.ToLower(p.FirstName + " " + p.LastName + "|" + p.Title)
		}
		if (p.Email == "" && p.FirstName == "" && p.LastName == "") || seen[key] {
			continue
		}
		seen[key] = true
		if p.Confidence <= 0 || p.Confidence > 1 {
			p.Confidence = 0.8
		}
		contacts = append(contacts, p)
	}
	c.Contacts = contacts
	if c.Email == "" {
		// A generic address found for the company becomes the business email.
		for _, p := range contacts {
			if p.FirstName == "" && p.LastName == "" && p.Email != "" {
				c.Email = p.Email
				break
			}
		}
	}
}

func errorf(provider, format string, a ...any) error {
	return fmt.Errorf(provider+": "+format, a...)
}
