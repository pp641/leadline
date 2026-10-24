// Package tests holds end-to-end tests that run the real HTTP router against
// PostgreSQL and Redis/Valkey. They are skipped unless TEST_DATABASE_URL and
// TEST_REDIS_URL are set (see `make test-integration`).
package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"leadline/config"
	"leadline/internal/app"
	"leadline/internal/campaigns"
	"leadline/internal/email"
	"leadline/internal/jobs"
	"leadline/pkg/logger"
	"leadline/pkg/queue"
	"leadline/pkg/secure"
)

// ───────────────────────── harness ─────────────────────────

type fakeProvider struct {
	mu   sync.Mutex
	sent []email.EmailMessage
	fail map[string]error // recipient → error
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Send(_ context.Context, m email.EmailMessage) (email.SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail[m.ToEmail]; err != nil {
		return email.SendResult{}, err
	}
	f.sent = append(f.sent, m)
	return email.SendResult{Provider: "fake", ProviderMessageID: "fake-" + m.MessageID}, nil
}

func (f *fakeProvider) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

type env struct {
	t        *testing.T
	app      *app.App
	srv      *httptest.Server
	provider *fakeProvider
	// personalize overrides the AI personalization handler (fake LLM).
	personalize queue.Handler
}

func setup(t *testing.T) *env {
	t.Helper()
	dbURL, redisURL := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_URL")
	if dbURL == "" || redisURL == "" {
		t.Skip("TEST_DATABASE_URL / TEST_REDIS_URL not set")
	}
	for k, v := range map[string]string{
		"DATABASE_URL": dbURL, "REDIS_URL": redisURL, "APP_ENV": "test",
		"JWT_SECRET": "test-jwt-secret-test-jwt-secret-1234", "SESSION_SECRET": "test-session-secret-test-session-1234",
		"WEBHOOK_SECRET": "whsec-test", "EMAIL_PROVIDER": "log", "VERIFIER_MX_LOOKUP": "false",
		"VERIFIER_ALLOW_TEST_DOMAINS": "true", "STORAGE_DIR": t.TempDir(), "APP_URL": "http://app.test",
		"ENCRYPTION_KEY": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
	} {
		t.Setenv(k, v)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.New(context.Background(), cfg, logger.New("error"))
	if err != nil {
		t.Fatal(err)
	}
	// Isolate each test run: unique rate-limit buckets are not needed because
	// every test registers fresh users, but flush queues so jobs don't leak.
	a.Redis.FlushDB(context.Background())
	// Each test starts from an empty database: the scheduler acts on every
	// running campaign, so leftovers from earlier tests or runs would interfere.
	if _, err := a.Pool.Exec(context.Background(), `TRUNCATE organizations, users CASCADE`); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Router())
	t.Cleanup(func() { srv.Close(); a.Close() })
	return &env{t: t, app: a, srv: srv, provider: &fakeProvider{fail: map[string]error{}}}
}

type client struct {
	e     *env
	token string
	org   string
}

type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields"`
	} `json:"error"`
	Meta map[string]any `json:"meta"`
}

func (c *client) do(method, path string, body any, out any) int {
	c.e.t.Helper()
	var r io.Reader
	ctype := "application/json"
	switch b := body.(type) {
	case nil:
	case *bytes.Buffer:
		r = b
	case multipartBody:
		r, ctype = b.buf, b.ctype
	default:
		raw, _ := json.Marshal(b)
		r = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, c.e.srv.URL+path, r)
	req.Header.Set("Content-Type", ctype)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.org != "" {
		req.Header.Set("X-Organization-ID", c.org)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			c.e.t.Fatalf("%s %s: bad JSON %q", method, path, raw)
		}
		if env.Error != nil && resp.StatusCode < 400 {
			c.e.t.Fatalf("unexpected error %+v", env.Error)
		}
		if resp.StatusCode < 400 {
			if err := json.Unmarshal(env.Data, out); err != nil {
				c.e.t.Fatalf("%s %s: decode data: %v (%s)", method, path, err, env.Data)
			}
		}
	}
	return resp.StatusCode
}

func (c *client) must(method, path string, body any, out any, want int) {
	c.e.t.Helper()
	if got := c.do(method, path, body, out); got != want {
		c.e.t.Fatalf("%s %s: status %d, want %d", method, path, got, want)
	}
}

type multipartBody struct {
	buf   *bytes.Buffer
	ctype string
}

func csvUpload(name, content string) multipartBody {
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	fw, _ := w.CreateFormFile("file", name)
	_, _ = fw.Write([]byte(content))
	_ = w.Close()
	return multipartBody{buf: buf, ctype: w.FormDataContentType()}
}

// signup registers a user and creates an organization, returning a client
// scoped to that organization.
func (e *env) signup(name string) *client {
	c := &client{e: e}
	var auth struct {
		AccessToken string `json:"access_token"`
	}
	c.must("POST", "/api/v1/auth/register", map[string]any{
		"email": fmt.Sprintf("%s-%s@users.test", name, uuid.NewString()[:8]), "password": "correct-horse-battery", "full_name": name, "accept_terms": true,
	}, &auth, 201)
	c.token = auth.AccessToken
	var org struct {
		ID string `json:"id"`
	}
	c.must("POST", "/api/v1/organizations", map[string]string{
		"name": name + " Org", "physical_address": "1 Test Street, Testville",
	}, &org, 201)
	c.org = org.ID
	return c
}

func (e *env) runJobs(ctx context.Context, queueName string) {
	e.t.Helper()
	// Drain the queue synchronously by invoking handlers directly.
	handlers := map[string]queue.Handler{
		jobs.TypeProcessImport:  e.app.Imports.HandleProcess,
		jobs.TypeVerifyContacts: e.app.Verification.HandleVerifyContacts,
		jobs.TypeScoreLeads:     e.app.Scoring.HandleScoreLeads,
		jobs.TypeRescoreOrg:     e.app.Scoring.HandleRescoreOrg,
		jobs.TypeSendEmail:      campaigns.NewDelivery(e.app.Pool, e.provider, e.app.Unsub, e.app.Log, e.app.Vault).Handle,
		jobs.TypePersonalize:    e.app.Personalization.HandleGenerate,
		jobs.TypeCRMSync:        e.app.CRMSync.HandleSync,
		jobs.TypeInboxSync:      e.app.Inbox.HandleSync,
		jobs.TypeWorkflowEvent:  e.app.WorkflowEngine.HandleEvent,
	}
	if e.personalize != nil {
		handlers[jobs.TypePersonalize] = e.personalize
	}
	for i := 0; i < 10000; i++ {
		raw, err := e.app.Redis.RPop(ctx, "q:"+queueName+":ready").Result()
		if err != nil {
			return
		}
		var job queue.Job
		_ = json.Unmarshal([]byte(raw), &job)
		job.Attempt++
		if err := handlers[job.Type](ctx, job); err != nil && !queue.IsPermanent(err) {
			e.t.Fatalf("job %s failed: %v", job.Type, err)
		}
	}
}

// ───────────────────────── tests ─────────────────────────

func TestMilestoneOneFlow(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c := e.signup("alice")

	// Mailbox
	var mb struct {
		ID string `json:"id"`
	}
	c.must("POST", "/api/v1/mailboxes", map[string]any{
		"name": "Main", "from_email": "alice@sender.test", "from_name": "Alice", "daily_limit": 100,
	}, &mb, 201)

	// CSV import: 6 good rows, 1 invalid, 1 in-file duplicate.
	csv := "Company,Website,Email,First Name,Industry,City\n" +
		"Saffron Table,saffron.example,diya@saffron.example,Diya,Restaurant,Noida\n" +
		"Copper Grill,,karan@copper.example,Karan,Restaurant,Delhi\n" +
		"Urban Dhaba,urbandhaba.example,info@urbandhaba.example,,Restaurant,Gurgaon\n" +
		"Lotus Cafe,lotus.example,meera@lotus.example,Meera,Cafe,Delhi\n" +
		"Monsoon Bakery,,riya@monsoon.example,Riya,Bakery,Delhi\n" +
		"Blue Bistro,bluebistro.example,arjun@bluebistro.example,Arjun,Restaurant,Delhi\n" +
		",broken.example,x@broken.example,X,Restaurant,Delhi\n" +
		"Saffron Table Again,https://www.saffron.example,,,Restaurant,Noida\n"
	var imp struct {
		ID            string `json:"id"`
		Status        string `json:"status"`
		TotalRows     int    `json:"total_rows"`
		ValidRows     int    `json:"valid_rows"`
		InvalidRows   int    `json:"invalid_rows"`
		DuplicateRows int    `json:"duplicate_rows"`
		ImportedRows  int    `json:"imported_rows"`
	}
	c.must("POST", "/api/v1/imports", csvUpload("leads.csv", csv), &imp, 201)
	if imp.Status != "previewed" || imp.TotalRows != 8 || imp.InvalidRows != 1 || imp.DuplicateRows != 1 {
		t.Fatalf("preview: %+v", imp)
	}
	c.must("POST", "/api/v1/imports/"+imp.ID+"/commit", nil, &imp, 202)
	e.runJobs(ctx, jobs.QueueDefault) // import → verify → score
	e.runJobs(ctx, jobs.QueueDefault)
	c.must("GET", "/api/v1/imports/"+imp.ID, nil, &imp, 200)
	if imp.Status != "completed" || imp.ImportedRows != 6 {
		t.Fatalf("import result: %+v", imp)
	}

	// Filtering
	type leadRow struct {
		ID             string `json:"id"`
		CompanyName    string `json:"company_name"`
		LeadScore      int    `json:"lead_score"`
		PrimaryContact *struct {
			EmailStatus string `json:"email_status"`
		} `json:"primary_contact"`
	}
	var list []leadRow
	c.must("GET", "/api/v1/leads?city=delhi&industry=restaurant", nil, &list, 200)
	if len(list) != 2 {
		t.Fatalf("filter city=delhi industry=restaurant: got %d leads", len(list))
	}
	c.must("GET", "/api/v1/leads?has_website=false&sort=-score", nil, &list, 200)
	if len(list) != 2 || list[0].LeadScore < 20 {
		t.Fatalf("no-website leads should be found and scored: %+v", list)
	}
	c.must("GET", "/api/v1/leads?email_status=valid", nil, &list, 200)
	if len(list) != 5 { // info@ is role_based
		t.Fatalf("valid-email leads: got %d", len(list))
	}

	// Campaign: two steps, add leads by filter, launch.
	var camp struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	c.must("POST", "/api/v1/campaigns", map[string]any{
		"name": "Test campaign", "mailbox_id": mb.ID, "daily_limit": 100, "timezone": "UTC",
		"send_window_start": "00:00", "send_window_end": "23:59", "send_days": []int{1, 2, 3, 4, 5, 6, 7},
	}, &camp, 201)
	c.must("PUT", "/api/v1/campaigns/"+camp.ID+"/steps", map[string]any{"steps": []map[string]any{
		{"delay_days": 0, "subject_template": "Idea for {{company_name}}", "body_template": "Hi {{first_name|there}}, about {{company_name}} in {{city}}."},
		{"delay_days": 2, "subject_template": "", "body_template": "Following up, {{first_name|there}}."},
	}}, nil, 200)
	if code := c.do("PUT", "/api/v1/campaigns/"+camp.ID+"/steps", map[string]any{"steps": []map[string]any{
		{"subject_template": "{{secret}}", "body_template": "x"}}}, nil); code != 422 {
		t.Fatalf("unknown template variable accepted: %d", code)
	}
	var aud campaigns.AudienceResult
	c.must("POST", "/api/v1/campaigns/"+camp.ID+"/leads", map[string]any{"filter": map[string]any{"cities": []string{"Delhi", "Noida"}}}, &aud, 200)
	if aud.Added != 5 {
		t.Fatalf("audience: %+v", aud)
	}
	c.must("POST", "/api/v1/campaigns/"+camp.ID+"/launch", nil, &camp, 200)
	if camp.Status != "running" {
		t.Fatalf("status after launch = %s", camp.Status)
	}

	// Make one recipient hard-bounce at SMTP time.
	e.provider.fail["riya@monsoon.example"] = &email.SendError{Permanent: true, Bounce: true, Code: "550", Err: fmt.Errorf("no such user")}

	sched := campaigns.NewScheduler(e.app.Pool, e.app.Redis, e.app.Queue, e.app.Unsub, e.app.Audit, e.app.Log, 24*time.Hour).
		WithClock(func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) })
	if err := sched.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	e.runJobs(ctx, jobs.QueueEmail)
	if n := e.provider.count(); n != 4 {
		t.Fatalf("sent %d emails, want 4 (one bounced)", n)
	}
	first := e.provider.sent[0]
	if !strings.HasPrefix(first.Subject, "Idea for ") || first.UnsubscribeURL == "" ||
		!strings.Contains(first.TextBody, "1 Test Street") || !strings.Contains(first.TextBody, "/api/v1/public/unsubscribe/") {
		t.Fatalf("message missing content/compliance footer: %+v", first)
	}

	// Idempotency: re-running a send job for an already-sent message is a no-op,
	// and another scheduler pass does not re-queue step 1.
	var sentID uuid.UUID
	if err := e.app.Pool.QueryRow(ctx, `SELECT id FROM email_messages WHERE status='sent' LIMIT 1`).Scan(&sentID); err != nil {
		t.Fatal(err)
	}
	d := campaigns.NewDelivery(e.app.Pool, e.provider, e.app.Unsub, e.app.Log, e.app.Vault)
	payload, _ := json.Marshal(jobs.SendEmail{MessageID: sentID})
	if err := d.Handle(ctx, queue.Job{Type: jobs.TypeSendEmail, Payload: payload, Attempt: 1, MaxAttempts: 6}); err != nil {
		t.Fatal(err)
	}
	if err := sched.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	e.runJobs(ctx, jobs.QueueEmail)
	if n := e.provider.count(); n != 4 {
		t.Fatalf("duplicate send: %d emails", n)
	}

	// Bounce suppression happened synchronously.
	var supp []struct {
		Email  string `json:"email"`
		Reason string `json:"reason"`
	}
	c.must("GET", "/api/v1/suppressions", nil, &supp, 200)
	if len(supp) != 1 || supp[0].Email != "riya@monsoon.example" || supp[0].Reason != "hard_bounce" {
		t.Fatalf("suppressions: %+v", supp)
	}

	// Provider webhook: a reply (signed) and a bad signature.
	byTo := map[string]email.EmailMessage{}
	for _, m := range e.provider.sent {
		byTo[m.ToEmail] = m
	}
	body, _ := json.Marshal(map[string]any{"events": []map[string]any{{
		"message_id": byTo["diya@saffron.example"].MessageID, "event_type": "replied", "provider_event_id": "evt-1",
		"metadata": map[string]any{"classification": "interested"},
	}}})
	for _, sig := range []string{"bad", secure.HMACHex([]byte("whsec-test"), body), secure.HMACHex([]byte("whsec-test"), body)} {
		req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/webhooks/email/generic", bytes.NewReader(body))
		req.Header.Set("X-Webhook-Signature", sig)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if want := map[bool]int{true: 401, false: 200}[sig == "bad"]; resp.StatusCode != want {
			t.Fatalf("webhook sig=%s: status %d want %d", sig, resp.StatusCode, want)
		}
	}

	// Unsubscribe via the public one-click link.
	unsubURL := strings.Replace(byTo["karan@copper.example"].UnsubscribeURL, "http://app.test", e.srv.URL, 1)
	resp, err := http.Post(unsubURL, "application/x-www-form-urlencoded", strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("unsubscribe: %v %v", resp.StatusCode, err)
	}
	resp.Body.Close()
	if resp, _ := http.Get(e.srv.URL + "/api/v1/public/unsubscribe/forged.token"); resp.StatusCode != 400 {
		t.Fatalf("forged unsubscribe token accepted: %d", resp.StatusCode)
	}

	// Step 2 becomes due: only leads that neither replied, unsubscribed nor bounced get it.
	if _, err := e.app.Pool.Exec(ctx, `UPDATE campaign_leads SET next_send_at = now() - interval '1 minute' WHERE status='active'`); err != nil {
		t.Fatal(err)
	}
	if err := sched.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	e.runJobs(ctx, jobs.QueueEmail)
	var step2 []string
	for _, m := range e.provider.sent[4:] {
		step2 = append(step2, m.ToEmail)
		if !strings.HasPrefix(m.Subject, "Re: Idea for") || m.InReplyTo == "" {
			t.Errorf("follow-up not threaded: %q in-reply-to %q", m.Subject, m.InReplyTo)
		}
	}
	if len(step2) != 2 || strings.Contains(strings.Join(step2, ","), "karan@") || strings.Contains(strings.Join(step2, ","), "diya@") {
		t.Fatalf("step 2 recipients = %v, want meera@ and arjun@ only", step2)
	}

	// Analytics
	var ov struct {
		TotalLeads      int `json:"total_leads"`
		ActiveCampaigns int `json:"active_campaigns"`
		InterestedLeads int `json:"interested_leads"`
		Period          struct {
			Sent            int     `json:"sent"`
			Bounced         int     `json:"bounced"`
			RepliedLeads    int     `json:"replied_leads"`
			PositiveReplies int     `json:"positive_replies"`
			Unsubscribed    int     `json:"unsubscribed"`
			BounceRate      float64 `json:"bounce_rate"`
		} `json:"period"`
	}
	c.must("GET", "/api/v1/analytics/overview?days=30", nil, &ov, 200)
	p := ov.Period
	if ov.TotalLeads != 6 || p.Sent != 6 || p.Bounced != 1 || p.RepliedLeads != 1 || p.PositiveReplies != 1 || p.Unsubscribed != 1 || ov.InterestedLeads != 1 {
		t.Fatalf("analytics: %+v", ov)
	}
	var cd struct {
		Funnel []struct {
			Status string `json:"status"`
			Count  int    `json:"count"`
		} `json:"funnel"`
	}
	c.must("GET", "/api/v1/analytics/campaigns/"+camp.ID, nil, &cd, 200)
	funnel := map[string]int{}
	for _, f := range cd.Funnel {
		funnel[f.Status] = f.Count
	}
	if funnel["completed"] != 2 || funnel["replied"] != 1 || funnel["unsubscribed"] != 1 || funnel["bounced"] != 1 {
		t.Fatalf("funnel: %v", funnel)
	}

	// A provider-side failure (bad credentials) pauses the campaign instead of
	// failing the lead, and the step is retried after resuming.
	var leadsBefore int
	_ = e.app.Pool.QueryRow(ctx, `SELECT count(*) FROM campaign_leads WHERE status='failed'`).Scan(&leadsBefore)
	if leadsBefore != 0 {
		t.Fatalf("unexpected failed leads: %d", leadsBefore)
	}

	// All sequences are finished, so the next pass completes the campaign.
	if err := sched.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	c.must("GET", "/api/v1/campaigns/"+camp.ID, nil, &camp, 200)
	if camp.Status != "completed" {
		t.Fatalf("campaign status = %s, want completed", camp.Status)
	}
}

func TestTenantIsolationAndRoles(t *testing.T) {
	e := setup(t)
	alice := e.signup("alice")
	bob := e.signup("bob")

	var lead struct {
		ID string `json:"id"`
	}
	alice.must("POST", "/api/v1/leads", map[string]any{"company_name": "Alice Secret Co", "city": "Delhi"}, &lead, 201)

	// Bob, in his own org, cannot see Alice's lead...
	if code := bob.do("GET", "/api/v1/leads/"+lead.ID, nil, nil); code != 404 {
		t.Fatalf("cross-tenant read: %d", code)
	}
	var bobsLeads []any
	bob.must("GET", "/api/v1/leads", nil, &bobsLeads, 200)
	if len(bobsLeads) != 0 {
		t.Fatalf("bob sees %d leads", len(bobsLeads))
	}
	// ...or delete/update it...
	if code := bob.do("DELETE", "/api/v1/leads/"+lead.ID, nil, nil); code != 404 {
		t.Fatalf("cross-tenant delete: %d", code)
	}
	// ...and cannot act inside Alice's org by sending her org ID.
	spoof := &client{e: e, token: bob.token, org: alice.org}
	if code := spoof.do("GET", "/api/v1/leads", nil, nil); code != 403 {
		t.Fatalf("org header spoof: %d", code)
	}
	// Unauthenticated requests are rejected.
	anon := &client{e: e, org: alice.org}
	if code := anon.do("GET", "/api/v1/leads", nil, nil); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}

	// Add Bob to Alice's org as a viewer: read OK, write forbidden.
	var me struct {
		Email string `json:"email"`
	}
	bob.must("GET", "/api/v1/auth/me", nil, &me, 200)
	alice.must("POST", "/api/v1/organizations/current/members", map[string]string{"email": me.Email, "role": "viewer"}, nil, 204)
	viewer := &client{e: e, token: bob.token, org: alice.org}
	viewer.must("GET", "/api/v1/leads/"+lead.ID, nil, &lead, 200)
	if code := viewer.do("POST", "/api/v1/leads", map[string]any{"company_name": "Nope"}, nil); code != 403 {
		t.Fatalf("viewer write: %d", code)
	}

	// The last owner cannot be demoted.
	var members []struct {
		UserID string `json:"user_id"`
		Role   string `json:"role"`
	}
	alice.must("GET", "/api/v1/organizations/current/members", nil, &members, 200)
	for _, m := range members {
		if m.Role == "owner" {
			if code := alice.do("PATCH", "/api/v1/organizations/current/members/"+m.UserID, map[string]string{"role": "admin"}, nil); code != 409 {
				t.Fatalf("demoting last owner: %d", code)
			}
		}
	}
}

func TestAuthSessions(t *testing.T) {
	e := setup(t)
	emailAddr := "carol-" + uuid.NewString()[:8] + "@users.test"
	body, _ := json.Marshal(map[string]any{"email": emailAddr, "password": "correct-horse-battery", "full_name": "Carol", "accept_terms": true})
	resp, err := http.Post(e.srv.URL+"/api/v1/auth/register", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != 201 {
		t.Fatalf("register: %v %d", err, resp.StatusCode)
	}
	resp.Body.Close()
	var cookie *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == "ll_refresh" {
			cookie = ck
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("refresh cookie missing or insecure: %+v", cookie)
	}

	refresh := func(ck *http.Cookie, csrfHeader bool) (*http.Response, *http.Cookie) {
		req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/auth/refresh", nil)
		req.AddCookie(ck)
		if csrfHeader {
			req.Header.Set("X-Requested-With", "fetch")
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		for _, c := range r.Cookies() {
			if c.Name == "ll_refresh" && c.Value != "" {
				return r, c
			}
		}
		return r, nil
	}
	if r, _ := refresh(cookie, false); r.StatusCode != 403 {
		t.Fatalf("refresh without CSRF header: %d", r.StatusCode)
	}
	r, rotated := refresh(cookie, true)
	if r.StatusCode != 200 || rotated == nil || rotated.Value == cookie.Value {
		t.Fatalf("rotation failed: %d", r.StatusCode)
	}
	// Reusing the old token is detected and revokes the whole session family.
	if r, _ := refresh(cookie, true); r.StatusCode != 401 {
		t.Fatalf("reused refresh token accepted: %d", r.StatusCode)
	}
	if r, _ := refresh(rotated, true); r.StatusCode != 401 {
		t.Fatalf("session not revoked after reuse: %d", r.StatusCode)
	}

	// Wrong password
	body, _ = json.Marshal(map[string]string{"email": emailAddr, "password": "wrong-password-here"})
	resp, _ = http.Post(e.srv.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("wrong password: %d", resp.StatusCode)
	}
}

// TestLargeImport covers the Milestone 1 target of importing 10,000 leads.
func TestLargeImport(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	e := setup(t)
	c := e.signup("bulk")
	var b strings.Builder
	b.WriteString("company_name,website,email,first_name,industry,city\n")
	for i := 0; i < 10000; i++ {
		fmt.Fprintf(&b, "Company %d,company%d.example,owner@company%d.example,Owner%d,restaurant,Delhi\n", i, i, i, i)
	}
	start := time.Now()
	var imp struct {
		ID           string `json:"id"`
		ValidRows    int    `json:"valid_rows"`
		ImportedRows int    `json:"imported_rows"`
		Status       string `json:"status"`
	}
	c.must("POST", "/api/v1/imports", csvUpload("big.csv", b.String()), &imp, 201)
	previewDur := time.Since(start)
	c.must("POST", "/api/v1/imports/"+imp.ID+"/commit", nil, &imp, 202)
	e.runJobs(context.Background(), jobs.QueueDefault) // import, then verification and scoring fan-out
	e.runJobs(context.Background(), jobs.QueueDefault)
	c.must("GET", "/api/v1/imports/"+imp.ID, nil, &imp, 200)
	if imp.Status != "completed" || imp.ImportedRows != 10000 {
		t.Fatalf("import: %+v", imp)
	}
	var page []any
	c.must("GET", "/api/v1/leads?email_status=valid&city=Delhi&per_page=200", nil, &page, 200)
	if len(page) != 200 {
		t.Fatalf("filtered page size %d", len(page))
	}
	t.Logf("10k rows: preview %v, total %v", previewDur, time.Since(start))
}

func TestProviderFaultPausesCampaign(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c := e.signup("prov")
	var mb, camp, lead struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	c.must("POST", "/api/v1/mailboxes", map[string]any{"name": "M", "from_email": "x@sender.test", "from_name": "X", "daily_limit": 10}, &mb, 201)
	c.must("POST", "/api/v1/leads", map[string]any{"company_name": "Acme", "contact": map[string]string{"first_name": "Jo", "email": "jo@acme.example"}}, &lead, 201)
	e.runJobs(ctx, jobs.QueueDefault)
	e.runJobs(ctx, jobs.QueueDefault)
	c.must("POST", "/api/v1/campaigns", map[string]any{"name": "P", "mailbox_id": mb.ID, "timezone": "UTC",
		"send_window_start": "00:00", "send_window_end": "23:59", "send_days": []int{1, 2, 3, 4, 5, 6, 7}}, &camp, 201)
	c.must("PUT", "/api/v1/campaigns/"+camp.ID+"/steps", map[string]any{"steps": []map[string]any{{"subject_template": "Hi", "body_template": "Hello"}}}, nil, 200)
	c.must("POST", "/api/v1/campaigns/"+camp.ID+"/leads", map[string]any{"lead_ids": []string{lead.ID}}, nil, 200)
	c.must("POST", "/api/v1/campaigns/"+camp.ID+"/launch", nil, &camp, 200)

	e.provider.fail["jo@acme.example"] = &email.SendError{ProviderFault: true, Code: "530", Err: fmt.Errorf("authentication required")}
	sched := campaigns.NewScheduler(e.app.Pool, e.app.Redis, e.app.Queue, e.app.Unsub, e.app.Audit, e.app.Log, 24*time.Hour).
		WithClock(func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) })
	if err := sched.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	e.runJobs(ctx, jobs.QueueEmail)
	c.must("GET", "/api/v1/campaigns/"+camp.ID, nil, &camp, 200)
	if camp.Status != "paused" {
		t.Fatalf("campaign status %s, want paused", camp.Status)
	}
	var clStatus string
	_ = e.app.Pool.QueryRow(ctx, `SELECT status FROM campaign_leads WHERE campaign_id=$1`, camp.ID).Scan(&clStatus)
	if clStatus != "active" {
		t.Fatalf("lead status %s, want active (untouched)", clStatus)
	}

	// Fix the provider, resume: the same step is sent exactly once.
	delete(e.provider.fail, "jo@acme.example")
	c.must("POST", "/api/v1/campaigns/"+camp.ID+"/resume", nil, &camp, 200)
	if err := sched.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	e.runJobs(ctx, jobs.QueueEmail)
	if e.provider.count() != 1 {
		t.Fatalf("sent %d, want 1", e.provider.count())
	}
}
