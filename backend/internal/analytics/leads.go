package analytics

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"leadline/internal/auth"
	"leadline/pkg/apperr"
	"leadline/pkg/geo"
	"leadline/pkg/httpx"
)

// Lead insights: the shape of the lead base (who you have, where they are,
// how far along they are) under an arbitrary filter, broken down by every
// dimension the UI charts, plus a comparison table for one chosen dimension.

// LeadFilter narrows the leads analysed. Empty fields don't filter.
type LeadFilter struct {
	Industries     []string `json:"industries"`
	Cities         []string `json:"cities"`
	States         []string `json:"states"`
	Countries      []string `json:"countries"`
	Sources        []string `json:"sources"`
	Tags           []string `json:"tags"`
	Statuses       []string `json:"statuses"`
	WebsiteQuality []string `json:"website_quality"` // good | ok | poor | none | unknown | missing
	FoundedFrom    *int     `json:"founded_from"`
	FoundedTo      *int     `json:"founded_to"`
	EmployeesMin   *int     `json:"employees_min"`
	EmployeesMax   *int     `json:"employees_max"`
	ScoreMin       *int     `json:"score_min"`
	ScoreMax       *int     `json:"score_max"`
	ICPFitMin      *int     `json:"icp_fit_min"`
	HasEmail       *bool    `json:"has_email"`
	HasPhone       *bool    `json:"has_phone"`
	HasWebsite     *bool    `json:"has_website"`
	CreatedFrom    string   `json:"created_from"` // YYYY-MM-DD, inclusive
	CreatedTo      string   `json:"created_to"`   // YYYY-MM-DD, inclusive
}

type LeadInsightsInput struct {
	Filter LeadFilter `json:"filter"`
	// Steps are further filters applied one after another (all must match),
	// e.g. a dashboard's step-by-step filters.
	Steps     []LeadFilter `json:"steps"`
	CompareBy string       `json:"compare_by"` // a dimension key; default industry
	Top       int          `json:"top"`        // categories per breakdown before "Other" (default 10)
}

type Bucket struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

type TrendPoint struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

type LeadTotals struct {
	Leads       int64    `json:"leads"`
	WithEmail   int64    `json:"with_email"`
	WithPhone   int64    `json:"with_phone"`
	WithWebsite int64    `json:"with_website"`
	Contacted   int64    `json:"contacted"`
	Engaged     int64    `json:"engaged"` // replied, interested, qualified, meeting, won
	Won         int64    `json:"won"`     // meeting or won
	AvgScore    float64  `json:"avg_score"`
	AvgICPFit   *float64 `json:"avg_icp_fit"`
}

// CompareRow is one category of the comparison dimension.
type CompareRow struct {
	Key       string           `json:"key"`
	Leads     int64            `json:"leads"`
	WithEmail int64            `json:"with_email"`
	Contacted int64            `json:"contacted"`
	Engaged   int64            `json:"engaged"`
	AvgScore  float64          `json:"avg_score"`
	AvgICPFit *float64         `json:"avg_icp_fit"`
	Stages    map[string]int64 `json:"stages"`
}

type LeadInsights struct {
	Totals      LeadTotals          `json:"totals"`
	Breakdowns  map[string][]Bucket `json:"breakdowns"`
	Stages      []Bucket            `json:"stages"` // pipeline stages, fixed order
	Trend       []TrendPoint        `json:"trend"`
	TrendUnit   string              `json:"trend_unit"` // day | week | month
	CompareBy   string              `json:"compare_by"`
	Comparison  []CompareRow        `json:"comparison"`
	GeneratedAt time.Time           `json:"generated_at"`
}

// Pipeline stages group the nine lead statuses into five, in funnel order.
var stageOf = `CASE l.status
	WHEN 'new' THEN 'new'
	WHEN 'contacted' THEN 'in_outreach'
	WHEN 'replied' THEN 'engaged' WHEN 'interested' THEN 'engaged' WHEN 'qualified' THEN 'engaged'
	WHEN 'meeting' THEN 'won' WHEN 'won' THEN 'won'
	ELSE 'closed' END`

var StageOrder = []string{"new", "in_outreach", "engaged", "won", "closed"}

const hasEmailExpr = `(l.business_email IS NOT NULL OR EXISTS (SELECT 1 FROM lead_contacts c WHERE c.lead_id = l.id AND c.email IS NOT NULL))`

// dimensions maps a breakdown key to its SQL expression over leads l.
var dimensions = map[string]string{
	"industry": `NULLIF(lower(trim(l.industry)), '')`,
	"city":     `NULLIF(trim(l.city), '')`,
	"state":    `NULLIF(trim(l.state), '')`,
	"country":  `NULLIF(trim(l.country), '')`,
	"source":   `NULLIF(l.source, '')`,
	"status":   `l.status`,
	"stage":    stageOf,
	"website": `CASE WHEN l.website = '' THEN 'no website' WHEN l.website_quality IN ('', 'none') THEN 'not checked'
		WHEN l.website_quality = 'ok' THEN 'good' ELSE l.website_quality END`,
	"founded_year": `l.founded_year::text`,
	"size": `CASE WHEN l.employee_count IS NULL THEN NULL WHEN l.employee_count <= 10 THEN '1–10'
		WHEN l.employee_count <= 50 THEN '11–50' WHEN l.employee_count <= 200 THEN '51–200'
		WHEN l.employee_count <= 1000 THEN '201–1,000' ELSE '1,000+' END`,
	"score": `CASE WHEN l.lead_score >= 90 THEN '90–100' ELSE (l.lead_score / 10 * 10)::text || '–' || (l.lead_score / 10 * 10 + 9)::text END`,
	"icp_fit": `CASE WHEN l.icp_fit IS NULL THEN NULL WHEN l.icp_fit >= 90 THEN '90–100'
		ELSE (l.icp_fit / 10 * 10)::text || '–' || (l.icp_fit / 10 * 10 + 9)::text END`,
}

// Ordered (non-ranked) dimensions keep their natural order instead of top-N.
var orderedDims = map[string]bool{"founded_year": true, "size": true, "score": true, "icp_fit": true}

var sizeOrder = []string{"1–10", "11–50", "51–200", "201–1,000", "1,000+"}

type sqlArgs struct{ vals []any }

func (a *sqlArgs) add(v any) string {
	a.vals = append(a.vals, v)
	return fmt.Sprintf("$%d", len(a.vals))
}

func lowerAll(vals []string) []string {
	var out []string
	for _, v := range vals {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// where compiles the filter; $1 is the organization.
func (f LeadFilter) where(args *sqlArgs) (string, error) {
	conds := []string{"l.organization_id = $1"}
	in := func(expr string, vals []string) {
		if v := lowerAll(vals); len(v) > 0 {
			conds = append(conds, "lower("+expr+") = ANY("+args.add(v)+")")
		}
	}
	in("trim(l.industry)", f.Industries)
	in("trim(l.city)", f.Cities)
	in("trim(l.state)", f.States)
	in("trim(l.country)", geo.Variants(f.Countries))
	in("l.source", f.Sources)
	in("l.status", f.Statuses)
	if v := lowerAll(f.Tags); len(v) > 0 {
		conds = append(conds, "EXISTS (SELECT 1 FROM lead_tags t WHERE t.lead_id = l.id AND lower(t.tag) = ANY("+args.add(v)+"))")
	}
	if v := lowerAll(f.WebsiteQuality); len(v) > 0 {
		conds = append(conds, "("+dimensions["website"]+") = ANY("+args.add(v)+")")
	}
	rng := func(col string, lo, hi *int) {
		if lo != nil {
			conds = append(conds, col+" >= "+args.add(*lo))
		}
		if hi != nil {
			conds = append(conds, col+" <= "+args.add(*hi))
		}
	}
	rng("l.founded_year", f.FoundedFrom, f.FoundedTo)
	rng("l.employee_count", f.EmployeesMin, f.EmployeesMax)
	rng("l.lead_score", f.ScoreMin, f.ScoreMax)
	rng("l.icp_fit", f.ICPFitMin, nil)
	flag := func(expr string, want *bool) {
		if want != nil {
			if *want {
				conds = append(conds, expr)
			} else {
				conds = append(conds, "NOT "+expr)
			}
		}
	}
	flag(hasEmailExpr, f.HasEmail)
	flag("(l.phone <> '')", f.HasPhone)
	flag("(l.website <> '')", f.HasWebsite)
	for _, d := range []struct {
		v, op string
		add   int
	}{{f.CreatedFrom, ">=", 0}, {f.CreatedTo, "<", 1}} {
		if d.v == "" {
			continue
		}
		t, err := time.Parse("2006-01-02", d.v)
		if err != nil {
			return "", apperr.Invalid("validation failed", map[string]string{"filter.created": "dates must be YYYY-MM-DD"})
		}
		conds = append(conds, "l.created_at "+d.op+" "+args.add(t.AddDate(0, 0, d.add)))
	}
	return strings.Join(conds, " AND "), nil
}

func (s *Service) LeadInsights(ctx context.Context, p auth.Principal, in LeadInsightsInput) (*LeadInsights, error) {
	if in.CompareBy == "" {
		in.CompareBy = "industry"
	}
	compareExpr, ok := dimensions[in.CompareBy]
	if in.CompareBy == "tag" {
		ok = true
	}
	if !ok {
		return nil, apperr.Invalid("validation failed", map[string]string{"compare_by": "unknown dimension"})
	}
	if in.Top <= 0 || in.Top > 25 {
		in.Top = 10
	}
	args := &sqlArgs{vals: []any{p.OrgID}}
	where, err := in.Filter.where(args)
	if err != nil {
		return nil, err
	}
	if len(in.Steps) > 12 {
		return nil, apperr.Invalid("validation failed", map[string]string{"steps": "at most 12 steps"})
	}
	for _, st := range in.Steps {
		w, err := st.where(args)
		if err != nil {
			return nil, err
		}
		where += " AND (" + w + ")"
	}

	// Trend granularity follows the span of the filtered leads.
	var first, last *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT min(l.created_at), max(l.created_at) FROM leads l WHERE `+where, args.vals...).Scan(&first, &last); err != nil {
		return nil, err
	}
	unit := "day"
	if first != nil && last != nil {
		switch span := last.Sub(*first); {
		case span > 400*24*time.Hour:
			unit = "month"
		case span > 90*24*time.Hour:
			unit = "week"
		}
	}

	out := &LeadInsights{Breakdowns: map[string][]Bucket{}, TrendUnit: unit, CompareBy: in.CompareBy, GeneratedAt: time.Now().UTC()}
	batch := &pgx.Batch{}
	batch.Queue(`SELECT count(*), count(*) FILTER (WHERE `+hasEmailExpr+`), count(*) FILTER (WHERE l.phone <> ''),
		count(*) FILTER (WHERE l.website <> ''), count(*) FILTER (WHERE l.status <> 'new'),
		count(*) FILTER (WHERE l.status IN ('replied','interested','qualified','meeting','won')),
		count(*) FILTER (WHERE l.status IN ('meeting','won')),
		COALESCE(avg(l.lead_score), 0), avg(l.icp_fit)
		FROM leads l WHERE `+where, args.vals...).QueryRow(func(row pgx.Row) error {
		t := &out.Totals
		return row.Scan(&t.Leads, &t.WithEmail, &t.WithPhone, &t.WithWebsite, &t.Contacted, &t.Engaged, &t.Won, &t.AvgScore, &t.AvgICPFit)
	})
	raw := map[string][]Bucket{}
	keys := []string{"industry", "city", "state", "country", "source", "stage", "website", "founded_year", "size", "score", "icp_fit"}
	for _, k := range keys {
		k := k
		batch.Queue(`SELECT d, count(*) FROM (SELECT `+dimensions[k]+` AS d FROM leads l WHERE `+where+`) x
			WHERE d IS NOT NULL GROUP BY d ORDER BY count(*) DESC, d LIMIT 400`, args.vals...).Query(func(rows pgx.Rows) error {
			return scanBuckets(rows, raw, k)
		})
	}
	batch.Queue(`SELECT t.tag, count(*) FROM lead_tags t JOIN leads l ON l.id = t.lead_id WHERE `+where+`
		GROUP BY t.tag ORDER BY count(*) DESC, t.tag LIMIT 400`, args.vals...).Query(func(rows pgx.Rows) error {
		return scanBuckets(rows, raw, "tag")
	})
	batch.Queue(`SELECT to_char(date_trunc('`+unit+`', l.created_at AT TIME ZONE 'UTC'), 'YYYY-MM-DD'), count(*)
		FROM leads l WHERE `+where+` GROUP BY 1 ORDER BY 1`, args.vals...).Query(func(rows pgx.Rows) error {
		for rows.Next() {
			var tp TrendPoint
			if err := rows.Scan(&tp.Date, &tp.Count); err != nil {
				return err
			}
			out.Trend = append(out.Trend, tp)
		}
		return rows.Err()
	})

	// Comparison: the top categories of the chosen dimension with metrics and stage mix.
	keyExpr, from := compareExpr, `leads l`
	if in.CompareBy == "tag" {
		keyExpr, from = `t.tag`, `leads l JOIN lead_tags t ON t.lead_id = l.id`
	}
	cmp := map[string]*CompareRow{}
	batch.Queue(`SELECT `+keyExpr+` AS k, `+stageOf+` AS stage, count(*), count(*) FILTER (WHERE `+hasEmailExpr+`),
		count(*) FILTER (WHERE l.status <> 'new'), count(*) FILTER (WHERE l.status IN ('replied','interested','qualified','meeting','won')),
		sum(l.lead_score), sum(l.icp_fit), count(l.icp_fit)
		FROM `+from+` WHERE `+where+` AND `+keyExpr+` IS NOT NULL GROUP BY 1, 2`, args.vals...).Query(func(rows pgx.Rows) error {
		type acc struct{ score, fit, fitN int64 }
		sums := map[string]*acc{}
		for rows.Next() {
			var k, stage string
			var n, email, contacted, engaged int64
			var score, fit *int64
			var fitN int64
			if err := rows.Scan(&k, &stage, &n, &email, &contacted, &engaged, &score, &fit, &fitN); err != nil {
				return err
			}
			r := cmp[k]
			if r == nil {
				r = &CompareRow{Key: k, Stages: map[string]int64{}}
				cmp[k], sums[k] = r, &acc{}
			}
			r.Leads += n
			r.WithEmail += email
			r.Contacted += contacted
			r.Engaged += engaged
			r.Stages[stage] += n
			if score != nil {
				sums[k].score += *score
			}
			if fit != nil {
				sums[k].fit += *fit
				sums[k].fitN += fitN
			}
		}
		for k, r := range cmp {
			r.AvgScore = float64(sums[k].score) / float64(max(r.Leads, 1))
			if sums[k].fitN > 0 {
				v := float64(sums[k].fit) / float64(sums[k].fitN)
				r.AvgICPFit = &v
			}
		}
		return rows.Err()
	})

	if err := s.pool.SendBatch(ctx, batch).Close(); err != nil {
		return nil, err
	}

	raw["country"] = mergeCountries(raw["country"])
	if in.CompareBy == "country" {
		merged := map[string]*CompareRow{}
		for _, r := range cmp {
			name := countryName(r.Key)
			m := merged[name]
			if m == nil {
				m = &CompareRow{Key: name, Stages: map[string]int64{}}
				merged[name] = m
			}
			// Weighted re-average before summing counts.
			if r.AvgICPFit != nil {
				prev := 0.0
				if m.AvgICPFit != nil {
					prev = *m.AvgICPFit * float64(m.Leads)
				}
				v := (prev + *r.AvgICPFit*float64(r.Leads)) / float64(m.Leads+r.Leads)
				m.AvgICPFit = &v
			}
			m.AvgScore = (m.AvgScore*float64(m.Leads) + r.AvgScore*float64(r.Leads)) / float64(max(m.Leads+r.Leads, 1))
			m.Leads += r.Leads
			m.WithEmail += r.WithEmail
			m.Contacted += r.Contacted
			m.Engaged += r.Engaged
			for k, v := range r.Stages {
				m.Stages[k] += v
			}
		}
		cmp = merged
	}
	for k, b := range raw {
		switch {
		case orderedDims[k]:
			out.Breakdowns[k] = orderBuckets(k, b)
		case k == "stage":
			counts := map[string]int64{}
			for _, x := range b {
				counts[x.Key] = x.Count
			}
			for _, st := range StageOrder {
				out.Stages = append(out.Stages, Bucket{Key: st, Count: counts[st]})
			}
		default:
			out.Breakdowns[k] = foldOther(b, in.Top)
		}
	}
	for _, k := range append(keys, "tag") {
		if out.Breakdowns[k] == nil && k != "stage" {
			out.Breakdowns[k] = []Bucket{}
		}
	}
	if out.Trend == nil {
		out.Trend = []TrendPoint{}
	}
	out.Trend = fillTrend(out.Trend, unit)

	rows := make([]CompareRow, 0, len(cmp))
	for _, r := range cmp {
		rows = append(rows, *r)
	}
	if orderedDims[in.CompareBy] {
		order := map[string]int{}
		for i, b := range orderBuckets(in.CompareBy, toBuckets(rows)) {
			order[b.Key] = i
		}
		sort.Slice(rows, func(i, j int) bool { return order[rows[i].Key] < order[rows[j].Key] })
		if len(rows) > 30 {
			rows = rows[len(rows)-30:]
		}
	} else {
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Leads != rows[j].Leads {
				return rows[i].Leads > rows[j].Leads
			}
			return rows[i].Key < rows[j].Key
		})
		if len(rows) > in.Top {
			rows = rows[:in.Top]
		}
	}
	out.Comparison = rows
	return out, nil
}

func countryName(s string) string {
	if c := geo.Code(s); c != "" {
		return geo.Name(c)
	}
	return s
}

// mergeCountries folds spellings of one country ("IN", "India") together.
func mergeCountries(b []Bucket) []Bucket {
	idx := map[string]int{}
	var out []Bucket
	for _, x := range b {
		name := countryName(x.Key)
		if i, ok := idx[name]; ok {
			out[i].Count += x.Count
			continue
		}
		idx[name] = len(out)
		out = append(out, Bucket{Key: name, Count: x.Count})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

func toBuckets(rows []CompareRow) []Bucket {
	out := make([]Bucket, len(rows))
	for i, r := range rows {
		out[i] = Bucket{Key: r.Key, Count: r.Leads}
	}
	return out
}

func scanBuckets(rows pgx.Rows, into map[string][]Bucket, key string) error {
	list := []Bucket{}
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.Key, &b.Count); err != nil {
			return err
		}
		list = append(list, b)
	}
	into[key] = list
	return rows.Err()
}

// foldOther keeps the top n buckets and sums the rest into "Other".
func foldOther(b []Bucket, n int) []Bucket {
	if len(b) <= n+1 {
		return b
	}
	var other int64
	for _, x := range b[n:] {
		other += x.Count
	}
	return append(append([]Bucket{}, b[:n]...), Bucket{Key: "Other", Count: other})
}

// orderBuckets puts ordinal buckets in their natural order.
func orderBuckets(dim string, b []Bucket) []Bucket {
	out := append([]Bucket{}, b...)
	rank := func(k string) int {
		switch dim {
		case "size":
			for i, s := range sizeOrder {
				if s == k {
					return i
				}
			}
			return 99
		default: // numeric prefix: years, score ranges
			var n int
			_, _ = fmt.Sscanf(k, "%d", &n)
			return n
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i].Key) < rank(out[j].Key) })
	return out
}

// fillTrend inserts zero points for empty periods so the time axis is even.
func fillTrend(points []TrendPoint, unit string) []TrendPoint {
	if len(points) < 2 {
		return points
	}
	have := map[string]int64{}
	for _, p := range points {
		have[p.Date] = p.Count
	}
	start, err1 := time.Parse("2006-01-02", points[0].Date)
	end, err2 := time.Parse("2006-01-02", points[len(points)-1].Date)
	if err1 != nil || err2 != nil {
		return points
	}
	var out []TrendPoint
	for t := start; !t.After(end) && len(out) < 1000; {
		k := t.Format("2006-01-02")
		out = append(out, TrendPoint{Date: k, Count: have[k]})
		switch unit {
		case "month":
			t = t.AddDate(0, 1, 0)
		case "week":
			t = t.AddDate(0, 0, 7)
		default:
			t = t.AddDate(0, 0, 1)
		}
	}
	return out
}

// StepCount is how many leads remain after applying steps 1..n.
type StepCount struct {
	Step  int   `json:"step"`
	Count int64 `json:"count"`
}

// StepCounts returns the total (step 0) and the leads left after each
// cumulative step, in one scan.
func (s *Service) StepCounts(ctx context.Context, p auth.Principal, steps []LeadFilter) ([]StepCount, error) {
	if len(steps) > 12 {
		return nil, apperr.Invalid("validation failed", map[string]string{"steps": "at most 12 steps"})
	}
	args := &sqlArgs{vals: []any{p.OrgID}}
	sel := []string{"count(*)"}
	cum := "true"
	for _, st := range steps {
		w, err := st.where(args)
		if err != nil {
			return nil, err
		}
		cum += " AND (" + w + ")"
		sel = append(sel, "count(*) FILTER (WHERE "+cum+")")
	}
	counts := make([]int64, len(sel))
	dest := make([]any, len(sel))
	for i := range counts {
		dest[i] = &counts[i]
	}
	if err := s.pool.QueryRow(ctx, `SELECT `+strings.Join(sel, ", ")+` FROM leads l WHERE l.organization_id = $1`, args.vals...).Scan(dest...); err != nil {
		return nil, err
	}
	out := make([]StepCount, len(counts))
	for i, n := range counts {
		out[i] = StepCount{Step: i, Count: n}
	}
	return out, nil
}

func (h *Handler) stepCounts(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Steps []LeadFilter `json:"steps"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.StepCounts(r.Context(), auth.MustPrincipal(r.Context()), in.Steps)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) leadInsights(w http.ResponseWriter, r *http.Request) {
	var in LeadInsightsInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.LeadInsights(r.Context(), auth.MustPrincipal(r.Context()), in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}
