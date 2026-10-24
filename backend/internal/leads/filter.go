package leads

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"leadline/pkg/httpx"
)

// Filter selects leads. It is shared by the list endpoint, CSV export, bulk
// actions and "add matching leads to campaign".
type Filter struct {
	Q               string      `json:"q,omitempty"`
	Industries      []string    `json:"industries,omitempty"`
	Cities          []string    `json:"cities,omitempty"`
	States          []string    `json:"states,omitempty"`
	Countries       []string    `json:"countries,omitempty"`
	Statuses        []string    `json:"statuses,omitempty"`
	Sources         []string    `json:"sources,omitempty"`
	Tags            []string    `json:"tags,omitempty"`
	EmailStatuses   []string    `json:"email_statuses,omitempty"`
	MinScore        *int        `json:"min_score,omitempty"`
	MaxScore        *int        `json:"max_score,omitempty"`
	HasWebsite      *bool       `json:"has_website,omitempty"`
	HasEmail        *bool       `json:"has_email,omitempty"`
	ImportID        *uuid.UUID  `json:"import_id,omitempty"`
	CampaignID      *uuid.UUID  `json:"campaign_id,omitempty"`
	NotInCampaignID *uuid.UUID  `json:"not_in_campaign_id,omitempty"`
	IDs             []uuid.UUID `json:"ids,omitempty"`
	OwnerID         *uuid.UUID  `json:"owner_id,omitempty"`
	Unassigned      bool        `json:"unassigned,omitempty"`
	WebsiteQuality  []string    `json:"website_quality,omitempty"`
	MinICPFit       *int        `json:"min_icp_fit,omitempty"`
	Sort            string      `json:"sort,omitempty"`
}

// FilterFromQuery parses list/export query parameters.
func FilterFromQuery(r *http.Request) Filter {
	q := r.URL.Query()
	f := Filter{
		Q:              strings.TrimSpace(q.Get("q")),
		Industries:     httpx.QueryList(r, "industry"),
		Cities:         httpx.QueryList(r, "city"),
		States:         httpx.QueryList(r, "state"),
		Countries:      httpx.QueryList(r, "country"),
		Statuses:       httpx.QueryList(r, "status"),
		Sources:        httpx.QueryList(r, "source"),
		Tags:           httpx.QueryList(r, "tag"),
		EmailStatuses:  httpx.QueryList(r, "email_status"),
		Sort:           q.Get("sort"),
		WebsiteQuality: httpx.QueryList(r, "website_quality"),
		Unassigned:     q.Get("owner") == "none",
	}
	if v, err := strconv.Atoi(q.Get("min_icp_fit")); err == nil {
		f.MinICPFit = &v
	}
	if v, err := strconv.Atoi(q.Get("min_score")); err == nil {
		f.MinScore = &v
	}
	if v, err := strconv.Atoi(q.Get("max_score")); err == nil {
		f.MaxScore = &v
	}
	if v, err := strconv.ParseBool(q.Get("has_website")); err == nil {
		f.HasWebsite = &v
	}
	if v, err := strconv.ParseBool(q.Get("has_email")); err == nil {
		f.HasEmail = &v
	}
	for key, dst := range map[string]**uuid.UUID{"import_id": &f.ImportID, "campaign_id": &f.CampaignID, "not_in_campaign_id": &f.NotInCampaignID, "owner_id": &f.OwnerID} {
		if v, err := uuid.Parse(q.Get(key)); err == nil {
			*dst = &v
		}
	}
	return f
}

// Where builds a parameterized WHERE clause over table alias "l". The first
// argument is always the organization ID, so the result is tenant-scoped by
// construction. User input only ever travels as bind parameters.
func (f Filter) Where(orgID uuid.UUID) (string, []any) {
	args := []any{orgID}
	conds := []string{"l.organization_id = $1"}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	lowerAll := func(in []string) []string {
		out := make([]string, len(in))
		for i, s := range in {
			out[i] = strings.ToLower(s)
		}
		return out
	}

	if f.Q != "" {
		p := arg("%" + escapeLike(f.Q) + "%")
		conds = append(conds, fmt.Sprintf(`(l.company_name ILIKE %[1]s OR l.domain ILIKE %[1]s OR l.business_email::text ILIKE %[1]s
			OR EXISTS (SELECT 1 FROM lead_contacts c WHERE c.lead_id = l.id AND
				(c.email::text ILIKE %[1]s OR (c.first_name || ' ' || c.last_name) ILIKE %[1]s)))`, p))
	}
	if len(f.Industries) > 0 {
		conds = append(conds, "lower(l.industry) = ANY("+arg(lowerAll(f.Industries))+")")
	}
	if len(f.Cities) > 0 {
		conds = append(conds, "lower(l.city) = ANY("+arg(lowerAll(f.Cities))+")")
	}
	if len(f.States) > 0 {
		conds = append(conds, "lower(l.state) = ANY("+arg(lowerAll(f.States))+")")
	}
	if len(f.Countries) > 0 {
		conds = append(conds, "lower(l.country) = ANY("+arg(lowerAll(f.Countries))+")")
	}
	if len(f.Statuses) > 0 {
		conds = append(conds, "l.status = ANY("+arg(f.Statuses)+")")
	}
	if len(f.Sources) > 0 {
		conds = append(conds, "l.source = ANY("+arg(f.Sources)+")")
	}
	if len(f.Tags) > 0 {
		conds = append(conds, "EXISTS (SELECT 1 FROM lead_tags t WHERE t.lead_id = l.id AND t.tag = ANY("+arg(lowerAll(f.Tags))+"))")
	}
	if len(f.EmailStatuses) > 0 {
		conds = append(conds, "EXISTS (SELECT 1 FROM lead_contacts c WHERE c.lead_id = l.id AND c.email IS NOT NULL AND c.email_status = ANY("+arg(f.EmailStatuses)+"))")
	}
	if f.MinScore != nil {
		conds = append(conds, "l.lead_score >= "+arg(*f.MinScore))
	}
	if f.MaxScore != nil {
		conds = append(conds, "l.lead_score <= "+arg(*f.MaxScore))
	}
	if f.HasWebsite != nil {
		if *f.HasWebsite {
			conds = append(conds, "l.website <> ''")
		} else {
			conds = append(conds, "l.website = ''")
		}
	}
	if f.HasEmail != nil {
		has := "(l.business_email IS NOT NULL OR EXISTS (SELECT 1 FROM lead_contacts c WHERE c.lead_id = l.id AND c.email IS NOT NULL))"
		if *f.HasEmail {
			conds = append(conds, has)
		} else {
			conds = append(conds, "NOT "+has)
		}
	}
	if f.ImportID != nil {
		conds = append(conds, "l.import_id = "+arg(*f.ImportID))
	}
	if f.CampaignID != nil {
		conds = append(conds, "EXISTS (SELECT 1 FROM campaign_leads cl WHERE cl.lead_id = l.id AND cl.campaign_id = "+arg(*f.CampaignID)+")")
	}
	if f.NotInCampaignID != nil {
		conds = append(conds, "NOT EXISTS (SELECT 1 FROM campaign_leads cl WHERE cl.lead_id = l.id AND cl.campaign_id = "+arg(*f.NotInCampaignID)+")")
	}
	if len(f.IDs) > 0 {
		conds = append(conds, "l.id = ANY("+arg(f.IDs)+")")
	}
	if f.OwnerID != nil {
		conds = append(conds, "l.owner_id = "+arg(*f.OwnerID))
	}
	if f.Unassigned {
		conds = append(conds, "l.owner_id IS NULL")
	}
	if len(f.WebsiteQuality) > 0 {
		conds = append(conds, "l.website_quality = ANY("+arg(f.WebsiteQuality)+")")
	}
	if f.MinICPFit != nil {
		conds = append(conds, "l.icp_fit >= "+arg(*f.MinICPFit))
	}
	return strings.Join(conds, " AND "), args
}

// OrderBy maps the sort key to a whitelisted ORDER BY clause.
func (f Filter) OrderBy() string {
	switch f.Sort {
	case "score":
		return "l.lead_score ASC, l.id"
	case "-score":
		return "l.lead_score DESC, l.id"
	case "company_name":
		return "lower(l.company_name) ASC, l.id"
	case "-company_name":
		return "lower(l.company_name) DESC, l.id"
	case "created_at":
		return "l.created_at ASC, l.id"
	case "-icp_fit":
		return "l.icp_fit DESC NULLS LAST, l.id"
	default:
		return "l.created_at DESC, l.id"
	}
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
