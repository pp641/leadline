package sources

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"leadline/internal/integrations"
)

// fake serves canned JSON per path and records request bodies.
func fake(t *testing.T, routes map[string]string, seen map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if seen != nil {
			seen[r.URL.Path] = string(b)
			seen[r.URL.Path+"#headers"] = r.Header.Get("X-Goog-Api-Key") + r.Header.Get("x-api-key") + r.Header.Get("X-Goog-FieldMask")
			seen[r.URL.Path+"#query"] = r.URL.RawQuery
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The test servers listen on 127.0.0.1, which the production client refuses.
func allowLocal(t *testing.T) {
	t.Helper()
	old := integrations.SetHTTPClientForTest(http.DefaultClient)
	t.Cleanup(func() { integrations.SetHTTPClientForTest(old) })
}

func TestPlacesSearch(t *testing.T) {
	allowLocal(t)
	seen := map[string]string{}
	srv := fake(t, map[string]string{"/places:searchText": `{"places":[
		{"id":"p1","displayName":{"text":"Spice Route"},"websiteUri":"https://spiceroute.in","internationalPhoneNumber":"+91 98765 43210",
		 "formattedAddress":"Sector 18, Noida","googleMapsUri":"https://maps.google.com/?cid=1","primaryTypeDisplayName":{"text":"Indian Restaurant"},
		 "addressComponents":[{"longText":"Noida","types":["locality"]},{"longText":"Uttar Pradesh","types":["administrative_area_level_1"]},{"longText":"India","shortText":"IN","types":["country"]}],
		 "rating":4.4,"userRatingCount":210},
		{"id":"p2","displayName":{"text":"Closed Cafe"},"businessStatus":"CLOSED_PERMANENTLY"},
		{"id":"p3","displayName":{"text":"No Site Dhaba"}}]}`}, seen)
	g := &Places{BaseURL: srv.URL}
	got, err := g.Search(context.Background(), integrations.Credentials{"api_key": "k"}, Params{"query": "restaurants", "location": "Noida", "only_with_website": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 open place with a website, got %d", len(got))
	}
	c := got[0]
	Normalize(&c)
	if c.CompanyName != "Spice Route" || c.City != "Noida" || c.State != "Uttar Pradesh" || c.Country != "India" ||
		c.Industry != "indian restaurant" || c.Domain != "" || c.SourceURL != "https://maps.google.com/?cid=1" || c.Extra["rating"] != 4.4 {
		t.Errorf("mapped %+v", c)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(seen["/places:searchText"]), &body)
	if body["textQuery"] != "restaurants in Noida" || !strings.HasPrefix(seen["/places:searchText#headers"], "k") ||
		!strings.Contains(seen["/places:searchText#headers"], "places.websiteUri") {
		t.Errorf("request body=%v headers=%q", body, seen["/places:searchText#headers"])
	}
}

func TestHunterDomainSearch(t *testing.T) {
	allowLocal(t)
	seen := map[string]string{}
	srv := fake(t, map[string]string{"/domain-search": `{"data":{"domain":"acme.in","organization":"Acme","industry":"Software",
		"country":"IN","city":"bengaluru","headcount":"11-50","linkedin":"https://linkedin.com/company/acme","technologies":["wordpress"],
		"emails":[{"value":"Priya@Acme.in","type":"personal","confidence":94,"first_name":"Priya","last_name":"S","position":"CTO","verification":{"status":"valid"}},
		          {"value":"info@acme.in","type":"generic","confidence":80,"verification":{"status":"accept_all"}},
		          {"value":"test@example.com","type":"generic","confidence":10}]}}`}, seen)
	h := &Hunter{BaseURL: srv.URL}
	got, err := h.Search(context.Background(), integrations.Credentials{"api_key": "k"}, Params{"domains": "https://www.acme.in/about\nbad", "limit": "10"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d candidates", len(got))
	}
	c := got[0]
	Normalize(&c)
	if !strings.Contains(seen["/domain-search#query"], "domain=acme.in") {
		t.Errorf("query %q", seen["/domain-search#query"])
	}
	if c.CompanyName != "Acme" || c.Country != "India" || c.City != "Bengaluru" || c.Email != "info@acme.in" ||
		c.EmployeeCount == nil || *c.EmployeeCount != 11 || !reflect.DeepEqual(c.TechStack, []string{"wordpress"}) {
		t.Errorf("company %+v", c)
	}
	if len(c.Contacts) != 2 || c.Contacts[0].Email != "priya@acme.in" || c.Contacts[0].Confidence != 0.94 ||
		c.Contacts[0].EmailStatus != "valid" || c.Contacts[1].EmailStatus != "risky" {
		t.Errorf("contacts %+v", c.Contacts)
	}
}

func TestApolloCompanies(t *testing.T) {
	allowLocal(t)
	seen := map[string]string{}
	srv := fake(t, map[string]string{"/mixed_companies/search": `{"organizations":[{"id":"o1","name":"Zeta Labs",
		"website_url":"http://www.zetalabs.io","primary_domain":"zetalabs.io","industry":"Information Technology",
		"estimated_num_employees":42,"founded_year":2021,"city":"Noida","state":"Uttar Pradesh","country":"India",
		"phone":"+91 120 000 0000","linkedin_url":"http://www.linkedin.com/company/zeta"}]}`}, seen)
	a := &Apollo{BaseURL: srv.URL}
	got, err := a.Search(context.Background(), integrations.Credentials{"api_key": "key1"},
		Params{"keywords": "saas, ai", "locations": []any{"Noida"}, "size": "11,50", "limit": "25"})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(seen["/mixed_companies/search"]), &body)
	if !reflect.DeepEqual(body["q_organization_keyword_tags"], []any{"saas", "ai"}) ||
		!reflect.DeepEqual(body["organization_num_employees_ranges"], []any{"11,50"}) || seen["/mixed_companies/search#headers"] != "key1" {
		t.Errorf("request %v headers %q", body, seen["/mixed_companies/search#headers"])
	}
	if len(got) != 1 || got[0].CompanyName != "Zeta Labs" || *got[0].EmployeeCount != 42 || got[0].SourceURL == "" {
		t.Errorf("got %+v", got)
	}
}

func TestNormalize(t *testing.T) {
	c := Candidate{CompanyName: " Acme ", Domain: "WWW.Acme.IN", Industry: " Software ", Country: "in", City: "greater noida",
		Email:    "email_not_unlocked@domain.com",
		Contacts: []Contact{{Email: "info@mysite.com"}, {FirstName: "A", Email: "A@acme.in"}, {FirstName: "A", Email: "a@acme.in"}, {}}}
	Normalize(&c)
	if c.CompanyName != "Acme" || c.Website != "https://WWW.Acme.IN" || c.Domain != "acme.in" || c.Industry != "software" ||
		c.Country != "India" || c.City != "Greater Noida" || c.Email != "" {
		t.Errorf("normalized %+v", c)
	}
	if len(c.Contacts) != 1 || c.Contacts[0].Email != "a@acme.in" {
		t.Errorf("contacts %+v", c.Contacts)
	}
}

func TestParams(t *testing.T) {
	p := Params{"d": "https://www.Acme.in/x\njohn@example.org, not a domain\nacme.in", "n": "500", "b": true}
	if got := p.Domains("d", 10); !reflect.DeepEqual(got, []string{"acme.in", "example.org"}) {
		t.Errorf("domains %v", got)
	}
	if p.Int("n", 25, 100) != 100 || p.Int("x", 25, 100) != 25 || !p.Bool("b") {
		t.Error("int/bool")
	}
	if employeesFromEnum("c_00011_00050") != 30 || employeesFromEnum("c_10001_max") != 10001 {
		t.Error("crunchbase enum")
	}
}
