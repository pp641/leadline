package tests

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"leadline/internal/jobs"
)

// TestDealsAndWorkflows: an interested reply opens a deal with an owner and a
// discovery-call task; moving the deal through the pipeline fires stage, won
// and stale-deal workflows exactly once each.
func TestDealsAndWorkflows(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.app.Onboarding.SetMailer(e.provider)
	c := e.signup("deals")
	org := uuid.MustParse(c.org)

	var lead struct {
		ID string `json:"id"`
	}
	c.must("POST", "/api/v1/leads", map[string]any{"company_name": "Acme Cafe", "industry": "cafe", "city": "Noida"}, &lead, 201)

	type wf struct {
		ID       string `json:"id"`
		RunCount int    `json:"run_count"`
	}
	var interested, proposal, won wf
	c.must("POST", "/api/v1/workflows", map[string]any{"template": "interested_reply"}, &interested, 201)
	c.must("POST", "/api/v1/workflows", map[string]any{"template": "proposal_follow_up"}, &proposal, 201)
	c.must("POST", "/api/v1/workflows", map[string]any{"template": "won_onboarding"}, &won, 201)
	c.must("POST", "/api/v1/workflows", map[string]any{"name": "Bad", "trigger": "deal.won", "actions": []map[string]any{{"type": "launch_rocket"}}}, nil, 422)

	// Templates were added before the pipeline was first opened; stage names
	// still resolve against the default pipeline it creates.
	type stage struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	var pipes []struct {
		ID     string  `json:"id"`
		Stages []stage `json:"stages"`
	}
	c.must("GET", "/api/v1/pipelines", nil, &pipes, 200)
	if len(pipes) != 1 || len(pipes[0].Stages) != 7 {
		t.Fatalf("default pipeline: %+v", pipes)
	}
	stageID := map[string]string{}
	for _, s := range pipes[0].Stages {
		stageID[s.Name] = s.ID
	}

	// A "not now" reply doesn't match the interested workflow's trigger.
	leadID := uuid.MustParse(lead.ID)
	e.app.WorkflowEngine.OnReply(ctx, org, leadID, uuid.Nil, "not_now", uuid.New())
	e.runJobs(ctx, jobs.QueueDefault)
	if n := e.count(`SELECT count(*) FROM deals WHERE organization_id=$1`, c.org); n != 0 {
		t.Fatalf("not_now opened %d deals", n)
	}

	// An interested reply, delivered twice, opens one deal and one task.
	replyID := uuid.New()
	for range 2 {
		e.app.WorkflowEngine.OnReply(ctx, org, leadID, uuid.Nil, "interested", replyID)
		e.runJobs(ctx, jobs.QueueDefault)
	}
	type deal struct {
		ID        string  `json:"id"`
		StageName string  `json:"stage_name"`
		OwnerName *string `json:"owner_name"`
		Source    string  `json:"source"`
		Status    string  `json:"status"`
	}
	var ds []deal
	c.must("GET", "/api/v1/deals?lead_id="+lead.ID, nil, &ds, 200)
	if len(ds) != 1 || ds[0].StageName != "Engaged" || ds[0].OwnerName == nil || ds[0].Source != "workflow" {
		t.Fatalf("deals: %+v", ds)
	}
	taskCount := func(prefix string) int {
		return e.count(`SELECT count(*) FROM tasks WHERE organization_id=$1 AND title LIKE $2`, c.org, prefix+"%")
	}
	if taskCount("Book a discovery call with Acme Cafe") != 1 {
		t.Fatal("discovery task not created exactly once")
	}

	// Move into Proposal sent → follow-up task.
	d := ds[0]
	c.must("POST", "/api/v1/deals/"+d.ID+"/move", map[string]string{"stage_id": stageID["Proposal sent"]}, nil, 200)
	e.runJobs(ctx, jobs.QueueDefault)
	if taskCount("Follow up on the proposal for Acme Cafe") != 1 {
		t.Fatal("proposal follow-up task missing")
	}
	// Lost needs a reason.
	c.must("POST", "/api/v1/deals/"+d.ID+"/move", map[string]string{"stage_id": stageID["Lost"]}, nil, 422)
	// Won → onboarding tasks, customer tag, lead status won.
	c.must("POST", "/api/v1/deals/"+d.ID+"/move", map[string]string{"stage_id": stageID["Won"]}, nil, 200)
	e.runJobs(ctx, jobs.QueueDefault)
	if taskCount("Send the welcome pack") != 1 || taskCount("Schedule the kickoff call") != 1 {
		t.Fatal("onboarding tasks missing")
	}
	if taskCount("Follow up on the proposal") != 1 {
		t.Fatal("the proposal workflow must only fire when entering Proposal sent")
	}
	if e.count(`SELECT count(*) FROM lead_tags WHERE lead_id=$1 AND tag='customer'`, lead.ID) != 1 ||
		e.count(`SELECT count(*) FROM leads WHERE id=$1 AND status='won'`, lead.ID) != 1 {
		t.Fatal("won side effects missing")
	}
	var detail struct {
		Deal   deal `json:"deal"`
		Events []struct {
			EventType string `json:"event_type"`
		} `json:"events"`
	}
	c.must("GET", "/api/v1/deals/"+d.ID, nil, &detail, 200)
	if detail.Deal.Status != "won" || len(detail.Events) < 3 {
		t.Fatalf("deal detail: %+v", detail)
	}

	var runs []struct {
		Status  string `json:"status"`
		Results []struct {
			Type, Status string
		} `json:"results"`
	}
	c.must("GET", "/api/v1/workflows/"+won.ID+"/runs", nil, &runs, 200)
	if len(runs) != 1 || runs[0].Status != "succeeded" || runs[0].Results[3].Status != "skipped" { // sync_crm: no CRM connected
		t.Fatalf("won runs: %+v", runs)
	}
	c.must("GET", "/api/v1/workflows/"+interested.ID, nil, &interested, 200)
	if interested.RunCount != 1 {
		t.Fatalf("interested run_count %d", interested.RunCount)
	}

	var board struct {
		Totals struct {
			WonCount30d int `json:"won_count_30d"`
		} `json:"totals"`
		Columns []struct {
			Count int `json:"count"`
		} `json:"columns"`
	}
	c.must("GET", "/api/v1/deals/board", nil, &board, 200)
	if board.Totals.WonCount30d != 1 || len(board.Columns) != 7 {
		t.Fatalf("board: %+v", board)
	}

	// Dry run explains a mismatch without changing anything.
	var test struct {
		TriggerMatches bool `json:"trigger_matches"`
	}
	c.must("POST", "/api/v1/workflows/"+interested.ID+"/test", map[string]string{"lead_id": lead.ID, "classification": "not_now"}, &test, 200)
	if test.TriggerMatches {
		t.Fatal("dry run should not match not_now")
	}

	// Stuck deal: a deal idle in Qualified past its 10-day limit alerts the owner once.
	var stuck deal
	c.must("POST", "/api/v1/deals", map[string]any{"name": "Big Gym deal", "value": 50000, "stage_id": stageID["Qualified"]}, &stuck, 201)
	if _, err := e.app.Pool.Exec(ctx, `UPDATE deals SET stage_entered_at = now() - interval '12 days' WHERE id=$1`, stuck.ID); err != nil {
		t.Fatal(err)
	}
	c.must("POST", "/api/v1/workflows", map[string]any{"template": "stuck_deal_alert"}, nil, 201)
	sent := e.provider.count()
	e.app.WorkflowEngine.ScanOnce(ctx)
	e.app.WorkflowEngine.ScanOnce(ctx)
	if e.provider.count() != sent+1 {
		t.Fatalf("stale alert emails: %d", e.provider.count()-sent)
	}
	if !strings.Contains(e.provider.sent[len(e.provider.sent)-1].TextBody, "Big Gym deal has been in Qualified for 12 days") {
		t.Fatalf("alert body: %q", e.provider.sent[len(e.provider.sent)-1].TextBody)
	}
	if taskCount("Unblock Big Gym deal") != 1 {
		t.Fatal("unblock task missing")
	}

	// Pipelines must keep exactly one Won and one Lost stage.
	c.must("PUT", "/api/v1/pipelines/"+pipes[0].ID, map[string]any{"name": "Sales", "stages": []map[string]any{{"name": "Only", "kind": "open"}}}, nil, 422)
}
