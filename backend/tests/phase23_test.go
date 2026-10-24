package tests

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"leadline/internal/campaigns"
	"leadline/internal/catalog"
	"leadline/internal/integrations"
	"leadline/internal/jobs"
	"leadline/internal/personalization"
	"leadline/pkg/safehttp"
	"leadline/pkg/secure"
)

// ───────────────────────── helpers ─────────────────────────

func (e *env) scheduler() *campaigns.Scheduler {
	return campaigns.NewScheduler(e.app.Pool, e.app.Redis, e.app.Queue, e.app.Unsub, e.app.Audit, e.app.Log, 24*time.Hour).
		WithClock(func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) })
}

type idOnly struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// campaignWithLeads creates a mailbox, n leads with valid contacts and a
// running one-step campaign. extra overrides campaign fields.
func (e *env) campaignWithLeads(c *client, n int, mailboxes []string, extra map[string]any, body string) (campaignID string, emails []string) {
	ctx := context.Background()
	var leadIDs []string
	for i := 0; i < n; i++ {
		addr := fmt.Sprintf("owner%d-%s@shop%d.example", i, uuid.NewString()[:6], i)
		var l idOnly
		c.must("POST", "/api/v1/leads", map[string]any{"company_name": fmt.Sprintf("Shop %d", i), "city": "Delhi",
			"contact": map[string]string{"first_name": fmt.Sprintf("Owner%d", i), "email": addr}}, &l, 201)
		leadIDs, emails = append(leadIDs, l.ID), append(emails, addr)
	}
	e.runJobs(ctx, jobs.QueueDefault) // verify + score
	e.runJobs(ctx, jobs.QueueDefault)
	body0 := map[string]any{"name": "C", "timezone": "UTC", "send_window_start": "00:00", "send_window_end": "23:59",
		"send_days": []int{1, 2, 3, 4, 5, 6, 7}, "mailbox_id": mailboxes[0], "mailbox_ids": mailboxes, "daily_limit": 100}
	for k, v := range extra {
		body0[k] = v
	}
	var camp idOnly
	c.must("POST", "/api/v1/campaigns", body0, &camp, 201)
	c.must("PUT", "/api/v1/campaigns/"+camp.ID+"/steps", map[string]any{"steps": []map[string]any{
		{"subject_template": "Hello {{company_name}}", "body_template": body},
		{"delay_days": 2, "body_template": "Following up."},
	}}, nil, 200)
	c.must("POST", "/api/v1/campaigns/"+camp.ID+"/leads", map[string]any{"lead_ids": leadIDs}, nil, 200)
	c.must("POST", "/api/v1/campaigns/"+camp.ID+"/launch", nil, &camp, 200)
	return camp.ID, emails
}

func (c *client) mailbox(email string, limit int) string {
	var mb idOnly
	c.must("POST", "/api/v1/mailboxes", map[string]any{"name": email, "from_email": email, "from_name": "Sam", "daily_limit": limit}, &mb, 201)
	return mb.ID
}

func signedPost(t *testing.T, url string, body []byte, secret string) *http.Response {
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("X-Webhook-Signature", secure.HMACHex([]byte(secret), body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func (e *env) count(q string, args ...any) int {
	var n int
	if err := e.app.Pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// ───────────────────────── API keys ─────────────────────────

func TestAPIKeys(t *testing.T) {
	e := setup(t)
	alice := e.signup("alice")
	var member, viewer struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	alice.must("POST", "/api/v1/api-keys", map[string]string{"name": "zapier", "role": "member"}, &member, 201)
	alice.must("POST", "/api/v1/api-keys", map[string]string{"name": "bi", "role": "viewer"}, &viewer, 201)
	if !strings.HasPrefix(member.Token, "ll_") {
		t.Fatalf("token format %q", member.Token)
	}
	if code := alice.do("POST", "/api/v1/api-keys", map[string]string{"name": "x", "role": "owner"}, nil); code != 422 {
		t.Fatalf("owner-role key allowed: %d", code)
	}

	key := &client{e: e, token: member.Token} // no X-Organization-ID needed
	key.must("POST", "/api/v1/leads", map[string]any{"company_name": "Via API"}, nil, 201)
	var list []any
	key.must("GET", "/api/v1/leads", nil, &list, 200)
	if len(list) != 1 {
		t.Fatalf("leads via key: %d", len(list))
	}
	ro := &client{e: e, token: viewer.Token}
	if code := ro.do("POST", "/api/v1/leads", map[string]any{"company_name": "Nope"}, nil); code != 403 {
		t.Fatalf("viewer key wrote: %d", code)
	}
	bob := e.signup("bob")
	wrong := &client{e: e, token: member.Token, org: bob.org}
	if code := wrong.do("GET", "/api/v1/leads", nil, nil); code != 403 {
		t.Fatalf("key used against another org: %d", code)
	}
	if code := key.do("POST", "/api/v1/api-keys", map[string]string{"name": "x", "role": "viewer"}, nil); code != 403 {
		t.Fatalf("member key created a key: %d", code)
	}
	if code := key.do("GET", "/api/v1/auth/me", nil, nil); code != 401 {
		t.Fatalf("API key accepted on a user route: %d", code)
	}
	alice.must("DELETE", "/api/v1/api-keys/"+member.ID, nil, nil, 204)
	if code := key.do("GET", "/api/v1/leads", nil, nil); code != 401 {
		t.Fatalf("revoked key accepted: %d", code)
	}
}

// ───────────────────────── Agency mode ─────────────────────────

func TestAgencyMode(t *testing.T) {
	e := setup(t)
	alice := e.signup("agency")
	if code := alice.do("POST", "/api/v1/agency/clients", map[string]string{"name": "Client"}, nil); code != 409 {
		t.Fatalf("client created without agency mode: %d", code)
	}
	alice.must("PUT", "/api/v1/agency", map[string]bool{"enabled": true}, nil, 204)
	var cl idOnly
	alice.must("POST", "/api/v1/agency/clients", map[string]string{"name": "Spice Group", "physical_address": "1 Road"}, &cl, 201)

	asClient := &client{e: e, token: alice.token, org: cl.ID}
	asClient.must("POST", "/api/v1/leads", map[string]any{"company_name": "Client lead"}, nil, 201)
	var orgs []struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	}
	alice.must("GET", "/api/v1/organizations", nil, &orgs, 200)
	roles := map[string]string{}
	for _, o := range orgs {
		roles[o.ID] = o.Role
	}
	if roles[cl.ID] != "admin" || roles[alice.org] != "owner" {
		t.Fatalf("roles: %v", roles)
	}

	// Agency data and client data stay separate.
	var agencyLeads []any
	alice.must("GET", "/api/v1/leads", nil, &agencyLeads, 200)
	if len(agencyLeads) != 0 {
		t.Fatal("client lead leaked into the agency org")
	}

	bob := e.signup("outsider")
	if code := (&client{e: e, token: bob.token, org: cl.ID}).do("GET", "/api/v1/leads", nil, nil); code != 403 {
		t.Fatalf("outsider reached client: %d", code)
	}
	var me struct {
		Email string `json:"email"`
	}
	bob.must("GET", "/api/v1/auth/me", nil, &me, 200)
	alice.must("POST", "/api/v1/organizations/current/members", map[string]string{"email": me.Email, "role": "viewer"}, nil, 204)
	bobClient := &client{e: e, token: bob.token, org: cl.ID}
	bobClient.must("GET", "/api/v1/leads", nil, nil, 200)
	if code := bobClient.do("POST", "/api/v1/leads", map[string]any{"company_name": "x"}, nil); code != 403 {
		t.Fatalf("agency viewer wrote to client: %d", code)
	}

	var clients []struct {
		Name  string `json:"name"`
		Leads int    `json:"leads"`
	}
	alice.must("GET", "/api/v1/agency/clients", nil, &clients, 200)
	if len(clients) != 1 || clients[0].Leads != 1 {
		t.Fatalf("clients: %+v", clients)
	}
}

// ───────────────────────── Inbound replies ─────────────────────────

func rawEmail(from, subject, inReplyTo, body string, headers ...string) string {
	h := fmt.Sprintf("From: %s\r\nTo: hello@sender.test\r\nSubject: %s\r\nMessage-ID: <%s@mail.example>\r\nDate: %s\r\n",
		from, subject, uuid.NewString(), time.Now().Format(time.RFC1123Z))
	if inReplyTo != "" {
		h += "In-Reply-To: <" + inReplyTo + ">\r\n"
	}
	for _, x := range headers {
		h += x + "\r\n"
	}
	return h + "Content-Type: text/plain; charset=utf-8\r\n\r\n" + body + "\r\n"
}

func TestInboundReplyPipeline(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c := e.signup("inbox")
	mb := c.mailbox("hello@sender.test", 50)
	campID, emails := e.campaignWithLeads(c, 4, []string{mb}, nil, "Hi {{first_name}}")
	if err := e.scheduler().Tick(ctx); err != nil {
		t.Fatal(err)
	}
	e.runJobs(ctx, jobs.QueueEmail)
	if e.provider.count() != 4 {
		t.Fatalf("sent %d", e.provider.count())
	}
	msgID := map[string]string{}
	for _, m := range e.provider.sent {
		msgID[m.ToEmail] = m.MessageID
	}
	c.must("POST", "/api/v1/workflows", map[string]any{"template": "interested_reply"}, nil, 201)
	inbound := func(raw string) map[string]any {
		body, _ := json.Marshal(map[string]string{"mailbox_email": "hello@sender.test", "raw": base64.StdEncoding.EncodeToString([]byte(raw))})
		resp := signedPost(t, e.srv.URL+"/api/v1/webhooks/inbound/generic", body, "whsec-test")
		defer resp.Body.Close()
		var env struct {
			Data map[string]any `json:"data"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&env)
		if resp.StatusCode != 200 {
			t.Fatalf("inbound webhook: %d", resp.StatusCode)
		}
		return env.Data
	}

	// 1. Threaded positive reply.
	r := inbound(rawEmail(emails[0], "Re: Hello", msgID[emails[0]], "Sounds good, let's talk next week.\n\nOn Mon, Sam wrote:\n> Hi"))
	if r["classification"] != "interested" || r["kind"] != "reply" {
		t.Fatalf("reply: %v", r)
	}
	e.runJobs(ctx, jobs.QueueDefault) // the interested reply runs the workflow
	if n := e.count(`SELECT count(*) FROM deals WHERE organization_id=$1 AND source='workflow'`, c.org); n != 1 {
		t.Fatalf("interested reply opened %d deals, want 1", n)
	}
	// 2. Unthreaded opt-out, matched by sender address.
	if r = inbound(rawEmail(emails[1], "stop", "", "Please remove me from your list.")); r["classification"] != "unsubscribe" {
		t.Fatalf("unsubscribe: %v", r)
	}
	// 3. Out-of-office: recorded, sequence continues.
	if r = inbound(rawEmail(emails[2], "Automatic reply: Hello", msgID[emails[2]], "I'm away until Monday.", "Auto-Submitted: auto-replied")); r["kind"] != "auto_reply" {
		t.Fatalf("auto-reply: %v", r)
	}
	// 4. Bounce report referencing the original Message-ID.
	dsn := "From: MAILER-DAEMON@mx.example\r\nSubject: Undeliverable: Hello\r\nMessage-ID: <dsn-" + uuid.NewString() + "@mx>\r\n" +
		"Content-Type: text/plain\r\n\r\nStatus: 5.1.1\r\nOriginal-Message-ID: <" + msgID[emails[3]] + ">\r\n"
	if r = inbound(dsn); r["kind"] != "bounce" {
		t.Fatalf("bounce: %v", r)
	}
	// Unrelated mail is never stored.
	if r = inbound(rawEmail("friend@elsewhere.example", "lunch?", "", "Lunch tomorrow?")); r["stored"] == true {
		t.Fatalf("unrelated mail stored: %v", r)
	}
	// Duplicate delivery is idempotent.
	dup := rawEmail(emails[0], "Re: Hello", msgID[emails[0]], "dup")
	inbound(dup)
	if r = inbound(dup); r["reason"] != "already processed" {
		t.Fatalf("duplicate: %v", r)
	}

	statusOf := func(addr string) string {
		var st string
		_ = e.app.Pool.QueryRow(ctx, `SELECT status FROM campaign_leads WHERE campaign_id=$1 AND email=$2`, campID, addr).Scan(&st)
		return st
	}
	if statusOf(emails[0]) != "replied" || statusOf(emails[1]) != "unsubscribed" || statusOf(emails[2]) != "active" || statusOf(emails[3]) != "bounced" {
		t.Fatalf("statuses: %s %s %s %s", statusOf(emails[0]), statusOf(emails[1]), statusOf(emails[2]), statusOf(emails[3]))
	}
	if n := e.count(`SELECT count(*) FROM leads WHERE status='interested' AND organization_id=$1`, c.org); n != 1 {
		t.Fatalf("interested leads: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM suppression_list WHERE organization_id=$1`, c.org); n != 2 {
		t.Fatalf("suppressions: %d (want unsubscribe + hard bounce)", n)
	}

	var msgs []struct {
		ID             string `json:"id"`
		Kind           string `json:"kind"`
		Classification string `json:"classification"`
	}
	c.must("GET", "/api/v1/inbox?kind=reply", nil, &msgs, 200)
	if len(msgs) != 3 { // 2 replies + the duplicate-test reply
		t.Fatalf("inbox: %+v", msgs)
	}
	// Manual reclassification to an opt-out suppresses the address.
	for _, m := range msgs {
		if m.Classification == "interested" {
			c.must("PATCH", "/api/v1/inbox/"+m.ID, map[string]string{"classification": "unsubscribe", "status": "handled"}, nil, 204)
		}
	}
	if n := e.count(`SELECT count(*) FROM suppression_list WHERE organization_id=$1`, c.org); n != 3 {
		t.Fatalf("reclassified opt-out not suppressed: %d", n)
	}
}

// ───────────────────────── Mailbox rotation + AI personalization ─────────────

type fakeLLM struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeLLM) Generate(_ context.Context, req integrations.LLMRequest) (integrations.LLMResponse, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if !strings.Contains(req.Messages[1].Content, "<lead_facts>") {
		return integrations.LLMResponse{}, fmt.Errorf("facts not delimited")
	}
	return integrations.LLMResponse{Content: `{"opener":"Saw your new place in Delhi.","insufficient_facts":false}`}, nil
}

func TestMailboxRotationAndPersonalization(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	llm := &fakeLLM{}
	ps := personalization.NewService(e.app.Pool, llm, e.app.Queue, e.app.Log)
	e.personalize = ps.HandleGenerate

	c := e.signup("rotate")
	mb1, mb2 := c.mailbox("a@sender.test", 1), c.mailbox("b@sender.test", 1)
	campID, _ := e.campaignWithLeads(c, 3, []string{mb1, mb2},
		map[string]any{"ai_personalization": true, "personalization_brief": "We build restaurant websites"},
		"Hi {{first_name}}, {{personalized_line|hope all is well}}")

	sched := e.scheduler()
	if err := sched.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	e.runJobs(ctx, jobs.QueueEmail)
	if e.provider.count() != 0 {
		t.Fatal("sent before openers were generated")
	}
	e.runJobs(ctx, jobs.QueueDefault) // generate openers with the fake LLM
	if llm.calls != 3 {
		t.Fatalf("llm calls %d", llm.calls)
	}
	if err := sched.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	e.runJobs(ctx, jobs.QueueEmail)
	if e.provider.count() != 0 {
		t.Fatal("sent before openers were reviewed")
	}
	var res map[string]int
	c.must("POST", "/api/v1/campaigns/"+campID+"/personalizations/approve-all", nil, &res, 200)
	if res["approved"] != 3 {
		t.Fatalf("approved %v", res)
	}
	if _, err := e.app.Pool.Exec(ctx, `UPDATE campaign_leads SET next_send_at=now() WHERE campaign_id=$1`, campID); err != nil {
		t.Fatal(err)
	}
	if err := sched.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	e.runJobs(ctx, jobs.QueueEmail)

	// Two mailboxes with a limit of 1 each → exactly 2 sends, one per mailbox.
	if e.provider.count() != 2 {
		t.Fatalf("sent %d, want 2 (mailbox capacity)", e.provider.count())
	}
	from := map[string]bool{}
	for _, m := range e.provider.sent {
		from[m.FromEmail] = true
		if !strings.Contains(m.TextBody, "Saw your new place in Delhi.") {
			t.Fatalf("opener missing: %q", m.TextBody)
		}
	}
	if len(from) != 2 {
		t.Fatalf("mailboxes not rotated: %v", from)
	}
	if n := e.count(`SELECT count(DISTINCT mailbox_id) FROM campaign_leads WHERE campaign_id=$1 AND mailbox_id IS NOT NULL`, campID); n != 2 {
		t.Fatalf("leads not pinned to mailboxes: %d", n)
	}
}

// ───────────────────────── Webhooks, company database, CRM, ICP ──────────────

func TestWebhooksCatalogCRMandICP(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	safehttp.AllowPrivate = true // fake external services listen on loopback
	t.Cleanup(func() { safehttp.AllowPrivate = false })
	c := e.signup("ops")

	// Outbound webhooks.
	var got []*http.Request
	var bodies [][]byte
	var mu sync.Mutex
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got, bodies = append(got, r), append(bodies, b)
		mu.Unlock()
	}))
	defer hook.Close()
	var ep struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	c.must("POST", "/api/v1/webhook-endpoints", map[string]any{"url": hook.URL, "events": []string{"lead.created"}}, &ep, 201)
	var existing idOnly
	c.must("POST", "/api/v1/leads", map[string]any{"company_name": "Existing", "website": "spiceroute.test", "city": "Delhi"}, &existing, 201)
	if _, err := e.app.WebhookDispatch.DeliverDue(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Header.Get("X-Leadline-Event") != "lead.created" {
		t.Fatalf("webhook deliveries: %d", len(got))
	}
	sig := got[0].Header.Get("X-Leadline-Signature")
	parts := strings.SplitN(sig, ",v1=", 2)
	m := hmac.New(sha256.New, []byte(ep.Secret))
	fmt.Fprintf(m, "%s.%s", strings.TrimPrefix(parts[0], "t="), bodies[0])
	if parts[1] != hex.EncodeToString(m.Sum(nil)) {
		t.Fatal("webhook signature does not verify with the endpoint secret")
	}

	// Company database (company-scraper) search and import; dedupe against the existing lead.
	c.must("POST", "/api/v1/catalog/search", map[string]any{"query": map[string]any{"industries": []string{"restaurant"}}}, nil, 409)
	e.app.Catalog.Source = fakeCatalog{
		{ID: uuid.New(), Name: "Spice Route", Website: "https://spiceroute.test", City: "Delhi", Industry: "restaurants", Source: "openstreetmap", SourceURL: "https://www.openstreetmap.org/node/1"},
		{ID: uuid.New(), Name: "Lotus Kitchen", Website: "https://lotuskitchen.test", City: "Delhi", Industry: "restaurants", FoundedYear: ip(2025),
			Source: "openstreetmap", SourceURL: "https://www.openstreetmap.org/node/2", VerificationStatus: "verified", WebsiteQuality: "good"},
	}
	t.Cleanup(func() { e.app.Catalog.Source = nil })
	var results []struct {
		Company         struct{ ID string } `json:"company"`
		DuplicateLeadID *string            `json:"duplicate_lead_id"`
	}
	c.must("POST", "/api/v1/catalog/search", map[string]any{"query": map[string]any{"industries": []string{"restaurant"}, "cities": []string{"Delhi"}}}, &results, 200)
	if len(results) != 2 || results[0].DuplicateLeadID == nil || *results[0].DuplicateLeadID != existing.ID || results[1].DuplicateLeadID != nil {
		t.Fatalf("results/dedupe: %+v", results)
	}
	var imported struct {
		Imported, Duplicates int
	}
	c.must("POST", "/api/v1/catalog/import", map[string]any{"company_ids": []string{results[0].Company.ID, results[1].Company.ID}, "tags": []string{"delhi"}}, &imported, 200)
	if imported.Imported != 1 || imported.Duplicates != 1 {
		t.Fatalf("imported %+v", imported)
	}
	e.runJobs(ctx, jobs.QueueDefault) // scoring for the new lead
	if n := e.count(`SELECT count(*) FROM leads WHERE organization_id=$1 AND source='openstreetmap' AND catalog_company_id IS NOT NULL AND website_quality='good'`, c.org); n != 1 {
		t.Fatalf("imported lead missing provenance or website signals: %d", n)
	}

	// ICP drives an automated fit score.
	c.must("POST", "/api/v1/icps", map[string]any{"name": "Delhi restaurants", "criteria": map[string]any{
		"industries": []string{"restaurants"}, "cities": []string{"Delhi"}, "founded_after_year": 2024}}, nil, 201)
	e.runJobs(ctx, jobs.QueueDefault)
	var detail struct {
		ICPFit       *int `json:"icp_fit"`
		ICPFitDetail struct {
			Fit     int      `json:"fit"`
			Matched []string `json:"matched"`
		} `json:"icp_fit_detail"`
	}
	var lotus string
	_ = e.app.Pool.QueryRow(ctx, `SELECT id FROM leads WHERE organization_id=$1 AND company_name='Lotus Kitchen'`, c.org).Scan(&lotus)
	c.must("GET", "/api/v1/leads/"+lotus, nil, &detail, 200)
	if detail.ICPFit == nil || *detail.ICPFit != 100 || detail.ICPFitDetail.Fit != 100 {
		t.Fatalf("icp fit: %+v", detail)
	}
	var suggestions []any
	c.must("GET", "/api/v1/scoring/suggestions", nil, &suggestions, 200)

	// CRM: owner, notes, tasks, pipeline.
	var me struct {
		ID string `json:"id"`
	}
	c.must("GET", "/api/v1/auth/me", nil, &me, 200)
	c.must("POST", "/api/v1/crm/assign", map[string]any{"lead_ids": []string{lotus}, "owner_id": me.ID}, nil, 200)
	c.must("POST", "/api/v1/leads/"+lotus+"/notes", map[string]string{"body": "Called — wants a demo"}, nil, 201)
	var task idOnly
	c.must("POST", "/api/v1/tasks", map[string]any{"lead_id": lotus, "title": "Send proposal", "due_at": time.Now().Add(48 * time.Hour)}, &task, 201)
	c.must("PUT", "/api/v1/tasks/"+task.ID, map[string]any{"lead_id": lotus, "title": "Send proposal", "completed": true}, nil, 204)
	var open []any
	c.must("GET", "/api/v1/tasks?assignee=me&status=open", nil, &open, 200)
	if len(open) != 0 {
		t.Fatalf("completed task still open: %d", len(open))
	}
	c.must("PUT", "/api/v1/leads/"+lotus, map[string]any{"company_name": "Lotus Kitchen", "city": "Delhi", "status": "interested"}, nil, 200)
	var board []struct {
		Status string `json:"status"`
		Count  int    `json:"count"`
	}
	c.must("GET", "/api/v1/crm/pipeline?owner=me", nil, &board, 200)
	for _, col := range board {
		if col.Status == "interested" && col.Count != 1 {
			t.Fatalf("pipeline: %+v", board)
		}
	}

	// HubSpot sync through a fake API.
	var hsCalls []string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hsCalls = append(hsCalls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/search"):
			_, _ = w.Write([]byte(`{"results":[]}`))
		case r.Method == "POST":
			_, _ = w.Write([]byte(`{"id":"hs-` + strings.Split(r.URL.Path, "/")[4] + `"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer hs.Close()
	e.app.Integrations.BaseURLs[integrations.KindHubSpot] = hs.URL
	c.must("PUT", "/api/v1/integrations/hubspot", map[string]any{"secret": "pat-123"}, nil, 204)
	var sealed []byte
	_ = e.app.Pool.QueryRow(ctx, `SELECT secret FROM integrations WHERE organization_id=$1`, c.org).Scan(&sealed)
	if len(sealed) == 0 || bytes.Contains(sealed, []byte("pat-123")) {
		t.Fatal("integration token stored in plaintext")
	}
	c.must("POST", "/api/v1/crm/sync", map[string]any{"lead_ids": []string{lotus}}, nil, 202)
	e.runJobs(ctx, jobs.QueueDefault)
	var companyID *string
	_ = e.app.Pool.QueryRow(ctx, `SELECT external_company_id FROM crm_links WHERE lead_id=$1`, lotus).Scan(&companyID)
	if companyID == nil || *companyID != "hs-companies" {
		t.Fatalf("hubspot link: %v calls=%v", companyID, hsCalls)
	}

	var adv struct {
		Segments map[string]any `json:"segments"`
	}
	c.must("GET", "/api/v1/analytics/advanced?days=30", nil, &adv, 200)
	if adv.Segments == nil {
		t.Fatal("advanced analytics missing segments")
	}
}

// TestCatalogImport: companies from the company database become leads with
// their provenance, website signals and verification report; undeliverable
// emails are not copied, and re-importing a company is a no-op.
func TestCatalogImport(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c := e.signup("catalog")
	var status map[string]bool
	c.must("GET", "/api/v1/catalog/status", nil, &status, 200)
	if status["configured"] {
		t.Fatalf("status without source: %v", status)
	}

	good, bad := "info@masala.test", "nobody@gone.test"
	verifiedAt := time.Now()
	masala, gone := uuid.New(), uuid.New()
	e.app.Catalog.Source = fakeCatalog{
		{ID: masala, Name: "Masala House", City: "Noida", Industry: "restaurant", Email: &good, EmailStatus: "role_based", Phone: "+911204000000",
			Source: "openstreetmap", SourceURL: "https://www.openstreetmap.org/node/7", Attribution: "© OpenStreetMap contributors (ODbL)",
			VerificationStatus: "verified", Verification: json.RawMessage(`{"checks":{}}`), VerifiedAt: &verifiedAt, TechStack: []string{"wordpress"}},
		{ID: gone, Name: "Gone Cafe", City: "Noida", Email: &bad, EmailStatus: "invalid", Source: "website", SourceURL: "https://gone.test"},
	}
	t.Cleanup(func() { e.app.Catalog.Source = nil })
	c.must("GET", "/api/v1/catalog/status", nil, &status, 200)
	if !status["configured"] || !status["reachable"] {
		t.Fatalf("status: %v", status)
	}
	var imported struct{ Imported, Duplicates int }
	c.must("POST", "/api/v1/catalog/import", map[string]any{"company_ids": []uuid.UUID{masala, gone}}, &imported, 200)
	if imported.Imported != 2 {
		t.Fatalf("imported %+v", imported)
	}
	var src, basis string
	var email *string
	_ = e.app.Pool.QueryRow(ctx, `SELECT l.source_url, s.consent_basis, l.business_email::text FROM leads l JOIN lead_sources s ON s.lead_id=l.id
		WHERE l.organization_id=$1 AND l.catalog_company_id=$2`, c.org, masala).Scan(&src, &basis, &email)
	if src != "https://www.openstreetmap.org/node/7" || basis != "public_business_contact" || email == nil || *email != good {
		t.Fatalf("provenance %q %q %v", src, basis, email)
	}
	if n := e.count(`SELECT count(*) FROM leads WHERE organization_id=$1 AND company_name='Gone Cafe' AND business_email IS NULL`, c.org); n != 1 {
		t.Fatal("invalid email was copied onto the lead")
	}
	var leadID string
	_ = e.app.Pool.QueryRow(ctx, `SELECT id FROM leads WHERE catalog_company_id=$1`, masala).Scan(&leadID)
	var report map[string]map[string]any
	c.must("GET", "/api/v1/leads/"+leadID+"/enrichment", nil, &report, 200)
	if report["company_db"]["verification_status"] != "verified" {
		t.Fatalf("enrichment report: %v", report)
	}
	c.must("POST", "/api/v1/catalog/import", map[string]any{"company_ids": []uuid.UUID{masala}}, &imported, 200)
	if imported.Imported != 0 || imported.Duplicates != 1 {
		t.Fatalf("re-import: %+v", imported)
	}
}

type fakeCatalog []catalog.Company

func (f fakeCatalog) Search(context.Context, catalog.Query) ([]catalog.Company, error) { return f, nil }
func (f fakeCatalog) Ping(context.Context) error                                       { return nil }
func (f fakeCatalog) Get(_ context.Context, ids []uuid.UUID) ([]catalog.Company, error) {
	var out []catalog.Company
	for _, c := range f {
		if slices.Contains(ids, c.ID) {
			out = append(out, c)
		}
	}
	return out, nil
}

func ip(i int) *int { return &i }
