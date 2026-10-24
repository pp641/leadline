package scoring

import (
	"slices"
	"testing"
	"time"
)

func intp(i int) *int { return &i }

func TestScoreDefaultRules(t *testing.T) {
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	f := Facts{
		Website: "", FoundedYear: intp(2025), EmployeeCount: intp(20), BestEmailStatus: "valid",
		SocialCount: 2, HasPhone: true,
	}
	res := Score(DefaultConfig(), f, now)
	if res.Score != 75 {
		t.Fatalf("score = %d, want 75 (reasons %v)", res.Score, res.Reasons)
	}
	for _, want := range []string{"No website", "Business opened recently", "Verified contact email", "Active social presence", "Company size match", "Has a phone number"} {
		if !slices.Contains(res.Reasons, want) {
			t.Errorf("missing reason %q in %v", want, res.Reasons)
		}
	}
	if res.Version != 1 {
		t.Errorf("version = %d", res.Version)
	}
}

func TestScoreOldBusinessWithWebsite(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	res := Score(DefaultConfig(), Facts{Website: "https://x.com", FoundedYear: intp(2001)}, now)
	if res.Score != 0 || len(res.Reasons) != 0 {
		t.Fatalf("got %+v, want zero", res)
	}
}

func TestScoreClampsAndDisabledRules(t *testing.T) {
	cfg := Config{Version: 3, Rules: []Rule{
		{Field: "industry", Operator: "in", Values: []string{"Restaurant"}, Points: 80, Reason: "industry", Enabled: true},
		{Field: "city", Operator: "eq", Values: []string{"delhi"}, Points: 80, Reason: "city", Enabled: true},
		{Field: "city", Operator: "eq", Values: []string{"delhi"}, Points: 80, Reason: "disabled", Enabled: false},
		{Field: "description", Operator: "contains", Values: []string{"closed"}, Points: -50, Reason: "closed", Enabled: true},
	}}
	res := Score(cfg, Facts{Industry: "restaurant", City: "Delhi"}, time.Now())
	if res.Score != 100 {
		t.Fatalf("score = %d, want clamp to 100", res.Score)
	}
	if slices.Contains(res.Reasons, "disabled") {
		t.Fatal("disabled rule applied")
	}
	res = Score(cfg, Facts{Description: "Permanently CLOSED"}, time.Now())
	if res.Score != 0 {
		t.Fatalf("score = %d, want clamp to 0", res.Score)
	}
}

func TestValidate(t *testing.T) {
	bad := Config{Rules: []Rule{
		{Field: "nope", Operator: "eq", Points: 1, Reason: "x"},
		{Field: "industry", Operator: "gte", Points: 1, Reason: "x"},
		{Field: "employee_count", Operator: "between", Min: nil, Points: 1, Reason: "x"},
		{Field: "has_email", Operator: "is_true", Points: 500, Reason: "x"},
		{Field: "city", Operator: "eq", Points: 1, Reason: ""},
	}}
	if errs := bad.Validate(); len(errs) != 5 {
		t.Fatalf("got %d errors, want 5: %v", len(errs), errs)
	}
	if errs := DefaultConfig().Validate(); len(errs) != 0 {
		t.Fatalf("default config invalid: %v", errs)
	}
}
