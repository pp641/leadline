package tests

import (
	"slices"
	"testing"
)

// TestCampaignAudience: leads join a campaign from an ICP (by fit), and from
// pasted emails that are matched to existing contacts or created as leads;
// dry runs preview the result without changing anything.
func TestCampaignAudience(t *testing.T) {
	e := setup(t)
	c := e.signup("audience")
	var camp struct {
		ID string `json:"id"`
	}
	c.must("POST", "/api/v1/campaigns", map[string]any{"name": "Audience test"}, &camp, 201)
	for _, l := range []map[string]any{
		{"company_name": "Cafe One", "industry": "cafe", "city": "Noida", "contact": map[string]any{"email": "owner@cafeone.test"}},
		{"company_name": "Cafe Two", "industry": "cafe", "city": "Noida"}, // no email: skipped
		{"company_name": "Iron Gym", "industry": "gym", "city": "Pune", "contact": map[string]any{"email": "boss@irongym.test"}},
	} {
		c.must("POST", "/api/v1/leads", l, nil, 201)
	}
	var icp struct {
		ID string `json:"id"`
	}
	c.must("POST", "/api/v1/icps", map[string]any{"name": "Noida cafes", "criteria": map[string]any{"industries": []string{"cafe"}, "cities": []string{"Noida"}}}, &icp, 201)

	type result struct {
		Requested, Added, Skipped, NoEmail, Created int
		AlreadyInCampaign                           int      `json:"already_in_campaign"`
		NoEmailJSON                                 int      `json:"no_email"`
		InvalidEmails                               []string `json:"invalid_emails"`
	}
	var r result
	path := "/api/v1/campaigns/" + camp.ID + "/leads"
	c.must("POST", path, map[string]any{"icp_id": icp.ID, "min_fit": 70, "dry_run": true}, &r, 200)
	if r.Requested != 2 || r.Added != 1 || r.NoEmailJSON != 1 {
		t.Fatalf("icp dry run: %+v", r)
	}
	c.must("POST", path, map[string]any{"icp_id": icp.ID, "min_fit": 70}, &r, 200)
	if r.Added != 1 {
		t.Fatalf("icp add: %+v", r)
	}

	contacts := []map[string]string{{"email": "owner@cafeone.test"}, {"email": "Asha@NewStudio.test", "company": "New Studio"}, {"email": "not-an-email"}}
	c.must("POST", path, map[string]any{"contacts": contacts, "dry_run": true}, &r, 200)
	if r.Created != 1 || r.Added != 1 || r.AlreadyInCampaign != 1 || !slices.Equal(r.InvalidEmails, []string{"not-an-email"}) {
		t.Fatalf("contacts dry run: %+v", r)
	}
	var leads []struct {
		CompanyName string   `json:"company_name"`
		Tags        []string `json:"tags"`
	}
	c.must("GET", "/api/v1/leads?q=New+Studio", nil, &leads, 200)
	if len(leads) != 0 {
		t.Fatal("dry run must not create leads")
	}
	c.must("POST", path, map[string]any{"contacts": contacts}, &r, 200)
	if r.Created != 1 || r.Added != 1 || r.AlreadyInCampaign != 1 {
		t.Fatalf("contacts add: %+v", r)
	}
	c.must("GET", "/api/v1/leads?q=New+Studio", nil, &leads, 200)
	if len(leads) != 1 || !slices.Contains(leads[0].Tags, "added-to-campaign") {
		t.Fatalf("created lead: %+v", leads)
	}
	var aud []struct {
		Email string `json:"email"`
	}
	c.must("GET", path, nil, &aud, 200)
	if len(aud) != 2 {
		t.Fatalf("audience: %+v", aud)
	}
}
