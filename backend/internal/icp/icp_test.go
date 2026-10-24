package icp

import (
	"testing"
	"time"
)

func ip(i int) *int { return &i }

func TestFit(t *testing.T) {
	c := Criteria{
		Industries: []string{"Restaurant"}, Cities: []string{"Delhi", "Noida"},
		EmployeesMin: ip(5), EmployeesMax: ip(100), FoundedAfterYear: ip(2024), Website: "missing_or_poor",
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	perfect := Facts{Industry: "restaurant", City: "noida", EmployeeCount: ip(20), FoundedYear: ip(2025), Website: "https://x.test", WebsiteQuality: "poor"}
	if r := Fit(c, perfect, now); r.Fit != 100 || len(r.Missed) != 0 {
		t.Fatalf("perfect lead: %+v", r)
	}
	// Wrong city (20) and too old (15) out of 90 total weight → 61.
	partial := perfect
	partial.City, partial.FoundedYear = "Mumbai", ip(2010)
	r := Fit(c, partial, now)
	if r.Fit != 61 || len(r.Missed) != 2 {
		t.Fatalf("partial lead: %+v", r)
	}
	// Unknown employee count does not satisfy a size criterion.
	unknown := perfect
	unknown.EmployeeCount = nil
	if r := Fit(c, unknown, now); r.Fit == 100 {
		t.Fatal("unknown size should not match")
	}
	// Empty criteria: no fit at all rather than a misleading 100.
	if r := Fit(Criteria{}, perfect, now); r.Fit != 0 {
		t.Fatalf("empty criteria: %+v", r)
	}
}
