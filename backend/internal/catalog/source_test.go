package catalog

import (
	"strings"
	"testing"

	"leadline/internal/icp"
)

func TestQueryWhere(t *testing.T) {
	q := FromCriteria(icp.Criteria{Industries: []string{"Restaurants"}, Cities: []string{"Delhi"}, States: []string{"Haryana"},
		Keywords: []string{"50%_off"}, Website: "missing_or_poor", RequireEmail: true})
	q.VerifiedOnly = true
	where, args := q.where()
	for _, want := range []string{
		"lower(industry) LIKE ANY($1)",
		"lower(trim(state)) = ANY($2) AND lower(trim(city)) = ANY($3)",
		"lower(name) LIKE ANY($4)",
		"website = '' OR website_quality IN ('none','poor')",
		"verification_status = 'verified'",
		"email IS NOT NULL",
	} {
		if !strings.Contains(where, want) {
			t.Errorf("where %q missing %q", where, want)
		}
	}
	if got := args[0].([]string)[0]; got != "%restaurant%" {
		t.Errorf("industry pattern %q", got)
	}
	if got := args[3].([]string)[0]; got != `%50\%\_off%` {
		t.Errorf("keyword pattern not escaped: %q", got)
	}
	if w, a := (Query{}).where(); w != "true" || len(a) != 0 {
		t.Errorf("empty query: %q %v", w, a)
	}
}
