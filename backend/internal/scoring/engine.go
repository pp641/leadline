// Package scoring implements configurable, explainable lead scoring.
//
// An organization's scoring config is a versioned list of rules. Each rule
// tests one lead fact and contributes points plus a human-readable reason.
// The engine is pure (no I/O) so it is easy to test and reason about.
package scoring

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

type Rule struct {
	ID       string   `json:"id"`
	Field    string   `json:"field"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"` // for eq / in / contains
	Min      *float64 `json:"min,omitempty"`    // for gte / between / within_years
	Max      *float64 `json:"max,omitempty"`    // for lte / between
	Points   int      `json:"points"`
	Reason   string   `json:"reason"`
	Enabled  bool     `json:"enabled"`
}

type Config struct {
	Version int    `json:"version"`
	Rules   []Rule `json:"rules"`
}

// Facts are the lead attributes rules can test.
type Facts struct {
	Website         string
	Industry        string
	City            string
	State           string
	Country         string
	Description     string
	Source          string
	EmployeeCount   *int
	FoundedYear     *int
	HasEmail        bool
	BestEmailStatus string // best status across contacts: valid > role_based > risky > unknown > ...
	SocialCount     int
	HasPhone        bool
	Tags            []string
	WebsiteQuality  string // "", none, poor, ok, good (from enrichment)
	TechStack       []string
	ICPFit          *int // 0–100 against the default ICP
	// Inputs for ICP fit (not used by rules directly).
	CompanyName string
}

type Result struct {
	Score   int      `json:"score"`
	Reasons []string `json:"reasons"`
	Version int      `json:"score_version"`
}

// Fields and operators accepted in rules, used for validation and the UI.
var (
	TextFields    = []string{"website", "website_quality", "tech_stack", "industry", "city", "state", "country", "description", "source", "email_status", "tags"}
	NumberFields  = []string{"employee_count", "founded_year", "social_count", "icp_fit"}
	BoolFields    = []string{"has_email", "has_phone"}
	TextOperators = []string{"is_empty", "not_empty", "eq", "in", "contains"}
	NumOperators  = []string{"is_empty", "not_empty", "gte", "lte", "between", "within_years"}
	BoolOperators = []string{"is_true", "is_false"}
)

func ptr(f float64) *float64 { return &f }

// DefaultConfig is applied to organizations that have not customized scoring.
func DefaultConfig() Config {
	return Config{Version: 1, Rules: []Rule{
		{ID: "recent", Field: "founded_year", Operator: "within_years", Min: ptr(2), Points: 20, Reason: "Business opened recently", Enabled: true},
		{ID: "no-website", Field: "website", Operator: "is_empty", Points: 20, Reason: "No website", Enabled: true},
		{ID: "poor-website", Field: "website_quality", Operator: "eq", Values: []string{"poor"}, Points: 15, Reason: "Website is outdated", Enabled: true},
		{ID: "icp-fit", Field: "icp_fit", Operator: "gte", Min: ptr(70), Points: 15, Reason: "Strong ICP fit", Enabled: true},
		{ID: "verified-email", Field: "email_status", Operator: "eq", Values: []string{"valid"}, Points: 15, Reason: "Verified contact email", Enabled: true},
		{ID: "social", Field: "social_count", Operator: "gte", Min: ptr(1), Points: 10, Reason: "Active social presence", Enabled: true},
		{ID: "size", Field: "employee_count", Operator: "between", Min: ptr(5), Max: ptr(100), Points: 5, Reason: "Company size match", Enabled: true},
		{ID: "phone", Field: "has_phone", Operator: "is_true", Points: 5, Reason: "Has a phone number", Enabled: true},
	}}
}

// Validate checks rule structure.
func (c Config) Validate() map[string]string {
	errs := map[string]string{}
	if len(c.Rules) > 100 {
		errs["rules"] = "at most 100 rules"
	}
	for i, r := range c.Rules {
		key := fmt.Sprintf("rules[%d]", i)
		switch {
		case slices.Contains(TextFields, r.Field):
			if !slices.Contains(TextOperators, r.Operator) {
				errs[key] = "invalid operator for text field"
			}
			if (r.Operator == "eq" || r.Operator == "in" || r.Operator == "contains") && len(r.Values) == 0 {
				errs[key] = "values are required"
			}
		case slices.Contains(NumberFields, r.Field):
			if !slices.Contains(NumOperators, r.Operator) {
				errs[key] = "invalid operator for numeric field"
			}
			if (r.Operator == "gte" || r.Operator == "between" || r.Operator == "within_years") && r.Min == nil {
				errs[key] = "min is required"
			}
			if (r.Operator == "lte" || r.Operator == "between") && r.Max == nil {
				errs[key] = "max is required"
			}
		case slices.Contains(BoolFields, r.Field):
			if !slices.Contains(BoolOperators, r.Operator) {
				errs[key] = "invalid operator for boolean field"
			}
		default:
			errs[key] = "unknown field " + r.Field
		}
		if r.Points < -100 || r.Points > 100 {
			errs[key] = "points must be between -100 and 100"
		}
		if strings.TrimSpace(r.Reason) == "" {
			errs[key] = "reason is required"
		}
	}
	return errs
}

// Score evaluates every enabled rule. The total is clamped to 0–100.
func Score(cfg Config, f Facts, now time.Time) Result {
	res := Result{Reasons: []string{}, Version: cfg.Version}
	for _, r := range cfg.Rules {
		if r.Enabled && matches(r, f, now) {
			res.Score += r.Points
			res.Reasons = append(res.Reasons, r.Reason)
		}
	}
	res.Score = max(0, min(100, res.Score))
	return res
}

func matches(r Rule, f Facts, now time.Time) bool {
	switch r.Field {
	case "has_email":
		return boolOp(r.Operator, f.HasEmail)
	case "has_phone":
		return boolOp(r.Operator, f.HasPhone)
	case "employee_count":
		return numOp(r, intPtrToFloat(f.EmployeeCount), now)
	case "founded_year":
		return numOp(r, intPtrToFloat(f.FoundedYear), now)
	case "social_count":
		v := float64(f.SocialCount)
		return numOp(r, &v, now)
	case "icp_fit":
		return numOp(r, intPtrToFloat(f.ICPFit), now)
	case "tags":
		return listOp(r, f.Tags)
	case "tech_stack":
		return listOp(r, f.TechStack)
	}
	return textOp(r, textField(r.Field, f))
}

func textField(field string, f Facts) string {
	switch field {
	case "website":
		return f.Website
	case "industry":
		return f.Industry
	case "city":
		return f.City
	case "state":
		return f.State
	case "country":
		return f.Country
	case "description":
		return f.Description
	case "source":
		return f.Source
	case "email_status":
		return f.BestEmailStatus
	case "website_quality":
		return f.WebsiteQuality
	}
	return ""
}

func boolOp(op string, v bool) bool {
	return (op == "is_true" && v) || (op == "is_false" && !v)
}

func textOp(r Rule, v string) bool {
	v = strings.TrimSpace(v)
	lv := strings.ToLower(v)
	switch r.Operator {
	case "is_empty":
		return v == ""
	case "not_empty":
		return v != ""
	case "eq", "in":
		for _, want := range r.Values {
			if strings.EqualFold(strings.TrimSpace(want), v) {
				return true
			}
		}
	case "contains":
		for _, want := range r.Values {
			if w := strings.ToLower(strings.TrimSpace(want)); w != "" && strings.Contains(lv, w) {
				return true
			}
		}
	}
	return false
}

func listOp(r Rule, vals []string) bool {
	switch r.Operator {
	case "is_empty":
		return len(vals) == 0
	case "not_empty":
		return len(vals) > 0
	}
	for _, v := range vals {
		if textOp(r, v) {
			return true
		}
	}
	return false
}

func numOp(r Rule, v *float64, now time.Time) bool {
	switch r.Operator {
	case "is_empty":
		return v == nil
	case "not_empty":
		return v != nil
	}
	if v == nil {
		return false
	}
	switch r.Operator {
	case "gte":
		return r.Min != nil && *v >= *r.Min
	case "lte":
		return r.Max != nil && *v <= *r.Max
	case "between":
		return r.Min != nil && r.Max != nil && *v >= *r.Min && *v <= *r.Max
	case "within_years":
		return r.Min != nil && *v >= float64(now.Year())-*r.Min
	}
	return false
}

func intPtrToFloat(p *int) *float64 {
	if p == nil {
		return nil
	}
	f := float64(*p)
	return &f
}

// EmailStatusRank orders verification statuses from best to worst; used to
// pick a lead's best contact status.
var EmailStatusRank = map[string]int{
	"valid": 6, "role_based": 5, "risky": 4, "unknown": 3, "unverified": 2, "disposable": 1, "invalid": 0,
}
