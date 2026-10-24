package leads

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"leadline/pkg/apperr"
)

func TestNormalizeWebsite(t *testing.T) {
	cases := []struct{ in, site, domain string }{
		{"Example.com", "https://example.com", "example.com"},
		{"http://WWW.Example.com/menu/", "http://www.example.com/menu", "example.com"},
		{"https://shop.example.co.in#top", "https://shop.example.co.in", "shop.example.co.in"},
		{"localhost", "", ""},
		{"ftp://example.com", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		site, domain := NormalizeWebsite(c.in)
		if site != c.site || domain != c.domain {
			t.Errorf("NormalizeWebsite(%q) = %q, %q; want %q, %q", c.in, site, domain, c.site, c.domain)
		}
	}
}

func TestFilterWhereIsTenantScopedAndParameterized(t *testing.T) {
	org := uuid.New()
	minScore := 50
	hasWebsite := false
	f := Filter{
		Q: "o'brien%_", Industries: []string{"Restaurant"}, Cities: []string{"Delhi", "Noida"},
		MinScore: &minScore, HasWebsite: &hasWebsite, Tags: []string{"VIP"},
	}
	where, args := f.Where(org)
	if !strings.HasPrefix(where, "l.organization_id = $1") || args[0] != org {
		t.Fatalf("filter must start with the tenant condition: %s", where)
	}
	if strings.Contains(where, "o'brien") || strings.Contains(where, "Restaurant") {
		t.Fatalf("user input leaked into SQL: %s", where)
	}
	if args[1] != `%o'brien\%\_%` {
		t.Errorf("LIKE pattern not escaped: %q", args[1])
	}
	if got := args[2].([]string)[0]; got != "restaurant" {
		t.Errorf("industry not lowercased: %q", got)
	}
	if !strings.Contains(where, "l.website = ''") {
		t.Errorf("has_website=false not applied: %s", where)
	}
}

func TestOrderByWhitelist(t *testing.T) {
	if got := (Filter{Sort: "-score"}).OrderBy(); got != "l.lead_score DESC, l.id" {
		t.Errorf("got %q", got)
	}
	if got := (Filter{Sort: "1; DROP TABLE leads"}).OrderBy(); got != "l.created_at DESC, l.id" {
		t.Errorf("unknown sort should fall back to default, got %q", got)
	}
}

func TestPrepareValidation(t *testing.T) {
	year := 1500
	in := Input{CompanyName: "  ", Website: "not a url", BusinessEmail: "x", FoundedYear: &year, Status: "bogus"}
	_, err := Prepare(&in)
	e, ok := apperr.As(err)
	if !ok {
		t.Fatalf("want validation error, got %v", err)
	}
	for _, f := range []string{"company_name", "website", "business_email", "founded_year", "status"} {
		if _, ok := e.Fields[f]; !ok {
			t.Errorf("missing field error for %s: %v", f, e.Fields)
		}
	}
	good := Input{CompanyName: " Saffron   Table ", Website: "saffron.test", Tags: []string{"VIP", "vip", " new "}}
	domain, err := Prepare(&good)
	if err != nil {
		t.Fatal(err)
	}
	if good.CompanyName != "Saffron Table" || domain != "saffron.test" || len(good.Tags) != 2 || good.Status != "new" {
		t.Errorf("normalization: %+v domain=%s", good, domain)
	}
}
