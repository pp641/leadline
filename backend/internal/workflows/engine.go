package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"leadline/internal/deals"
	"leadline/internal/jobs"
	"leadline/internal/leads"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/queue"
)

// MaxDepth caps cascades (workflow → deal event → workflow → …).
const MaxDepth = 3

// Notifier sends system emails for the notify action.
type Notifier interface {
	Notify(ctx context.Context, to, subject, message, path string) error
}

type Engine struct {
	svc    *Service
	deals  *deals.Service
	queue  jobs.Enqueuer
	notify Notifier
	log    *slog.Logger
}

func NewEngine(svc *Service, d *deals.Service, q jobs.Enqueuer, n Notifier, log *slog.Logger) *Engine {
	return &Engine{svc: svc, deals: d, queue: q, notify: n, log: log}
}

type chainKey struct{}

type chain struct {
	ids   []uuid.UUID
	depth int
}

func chainFrom(ctx context.Context) chain {
	c, _ := ctx.Value(chainKey{}).(chain)
	return c
}

// Emit queues a workflow event when the organization has an enabled workflow
// for the trigger. It is called after the triggering change commits.
func (e *Engine) Emit(ctx context.Context, ev jobs.WorkflowEvent) {
	c := chainFrom(ctx)
	ev.Chain, ev.Depth = c.ids, c.depth+1
	if ev.Depth > MaxDepth {
		e.log.Warn("workflow cascade depth reached", "org_id", ev.OrgID, "trigger", ev.Trigger)
		return
	}
	var any bool
	if err := e.svc.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflows WHERE organization_id=$1 AND trigger=$2 AND enabled)`, ev.OrgID, ev.Trigger).Scan(&any); err != nil || !any {
		return
	}
	if err := e.queue.Enqueue(context.WithoutCancel(ctx), jobs.QueueDefault, jobs.TypeWorkflowEvent, ev); err != nil {
		e.log.Error("enqueue workflow event", "err", err, "trigger", ev.Trigger)
	}
}

// OnDealEvent adapts deal lifecycle events (deals.Service.OnEvent).
func (e *Engine) OnDealEvent(ctx context.Context, orgID uuid.UUID, trigger string, d *deals.Deal) {
	key := trigger + ":" + d.ID.String()
	if trigger != deals.EvCreated {
		key += ":" + d.StageID.String() + ":" + strconv.FormatInt(d.StageEnteredAt.UnixMicro(), 10)
	}
	id := d.ID
	e.Emit(ctx, jobs.WorkflowEvent{OrgID: orgID, Trigger: trigger, EventKey: key, LeadID: d.LeadID, DealID: &id,
		Extra: map[string]string{"stage_id": d.StageID.String()}})
}

// OnReply adapts classified replies (email.Events.OnReply).
func (e *Engine) OnReply(ctx context.Context, orgID, leadID, campaignID uuid.UUID, classification string, eventID uuid.UUID) {
	if classification == "" {
		return
	}
	lid := leadID
	e.Emit(ctx, jobs.WorkflowEvent{OrgID: orgID, Trigger: TrReply, EventKey: "reply:" + eventID.String(), LeadID: &lid,
		Extra: map[string]string{"classification": classification, "campaign_id": campaignID.String()}})
}

func (e *Engine) HandleEvent(ctx context.Context, job queue.Job) error {
	var ev jobs.WorkflowEvent
	if err := job.Decode(&ev); err != nil {
		return queue.Permanent(err)
	}
	wfs, err := e.svc.enabledFor(ctx, ev.OrgID, ev.Trigger)
	if err != nil {
		return err
	}
	for i := range wfs {
		if slices.Contains(ev.Chain, wfs[i].ID) {
			continue // a workflow never re-triggers itself
		}
		if err := e.run(ctx, &wfs[i], ev); err != nil {
			e.log.Error("workflow run", "err", err, "workflow_id", wfs[i].ID)
		}
	}
	return nil
}

// ───────────── facts ─────────────

// subject is what a run is about: the lead, its deal and the event extras.
type subject struct {
	orgID  uuid.UUID
	leadID *uuid.UUID
	deal   *deals.Deal
	facts  map[string]any
	vars   map[string]string
}

func (e *Engine) load(ctx context.Context, orgID uuid.UUID, leadID, dealID *uuid.UUID, extra map[string]string) (*subject, error) {
	s := &subject{orgID: orgID, leadID: leadID, facts: map[string]any{}, vars: map[string]string{}}
	if dealID != nil {
		d, err := e.deals.Get(ctx, orgID, *dealID)
		if err != nil {
			return nil, err
		}
		s.deal = d
		if s.leadID == nil {
			s.leadID = d.LeadID
		}
	}
	if s.leadID != nil {
		var industry, city, state, country, source, status, company string
		var score int
		var fit *int
		var owner *uuid.UUID
		var ownerName *string
		var tags []string
		err := e.svc.pool.QueryRow(ctx, `SELECT l.company_name, l.industry, l.city, l.state, l.country, l.source, l.status, l.lead_score, l.icp_fit, l.owner_id, u.full_name,
			COALESCE((SELECT array_agg(tag) FROM lead_tags t WHERE t.lead_id = l.id), '{}')
			FROM leads l LEFT JOIN users u ON u.id = l.owner_id WHERE l.organization_id=$1 AND l.id=$2`, orgID, *s.leadID).
			Scan(&company, &industry, &city, &state, &country, &source, &status, &score, &fit, &owner, &ownerName, &tags)
		if err != nil {
			if database.IsNoRows(err) {
				s.leadID = nil
			} else {
				return nil, err
			}
		} else {
			s.facts["lead.industry"], s.facts["lead.city"], s.facts["lead.state"], s.facts["lead.country"] = industry, city, state, country
			s.facts["lead.source"], s.facts["lead.status"], s.facts["lead.tags"], s.facts["lead.lead_score"] = source, status, tags, score
			if fit != nil {
				s.facts["lead.icp_fit"] = *fit
			}
			if owner != nil {
				s.facts["lead.owner_id"] = owner.String()
			}
			s.vars["company"] = company
			if ownerName != nil {
				s.vars["owner"] = *ownerName
			}
		}
	}
	if d := s.deal; d != nil {
		s.facts["deal.value"], s.facts["deal.stage_id"], s.facts["deal.status"] = d.Value, d.StageID.String(), d.Status
		s.facts["deal.lost_reason"], s.facts["deal.source"], s.facts["deal.days_in_stage"] = d.LostReason, d.Source, d.DaysInStage
		if d.OwnerID != nil {
			s.facts["deal.owner_id"] = d.OwnerID.String()
		}
		s.vars["deal"], s.vars["stage"], s.vars["days"] = d.Name, d.StageName, strconv.Itoa(d.DaysInStage)
		s.vars["value"] = d.Currency + " " + strconv.FormatFloat(d.Value, 'f', -1, 64)
		if d.OwnerName != nil {
			s.vars["owner"] = *d.OwnerName
		}
		if s.vars["company"] == "" {
			s.vars["company"] = d.CompanyName
		}
		if s.vars["company"] == "" {
			s.vars["company"] = d.Name
		}
	}
	if c := extra["classification"]; c != "" {
		s.facts["reply.classification"] = c
		s.vars["classification"] = strings.ReplaceAll(c, "_", " ")
	}
	if d := extra["days"]; d != "" {
		s.vars["days"] = d
	}
	return s, nil
}

func (s *subject) label() string {
	if s.deal != nil {
		return s.deal.Name
	}
	return s.vars["company"]
}

func (s *subject) render(t string) string {
	for k, v := range s.vars {
		t = strings.ReplaceAll(t, "{{"+k+"}}", v)
	}
	return t
}

// ───────────── matching ─────────────

// triggerMatches applies the trigger's own options (e.g. only replies
// classified "interested", only deals entering a given stage).
func triggerMatches(w *Workflow, extra map[string]string) bool {
	switch w.Trigger {
	case TrReply:
		if want := strList(w.TriggerConfig["classifications"]); len(want) > 0 {
			return slices.Contains(want, extra["classification"])
		}
	case TrStageChanged:
		if id := str(w.TriggerConfig, "to_stage_id"); id != "" {
			return id == extra["stage_id"]
		}
	}
	if pid := str(w.TriggerConfig, "pipeline_id"); pid != "" && extra["pipeline_id"] != "" {
		return pid == extra["pipeline_id"]
	}
	return true
}

type CondResult struct {
	Condition
	Actual any  `json:"actual"`
	Passed bool `json:"passed"`
}

func evaluate(conds []Condition, facts map[string]any) (bool, []CondResult) {
	ok := true
	out := make([]CondResult, 0, len(conds))
	for _, c := range conds {
		actual := facts[c.Field]
		passed := check(c, actual)
		ok = ok && passed
		out = append(out, CondResult{Condition: c, Actual: actual, Passed: passed})
	}
	return ok, out
}

func check(c Condition, actual any) bool {
	vals := factStrings(actual)
	switch c.Op {
	case "is_set":
		return len(vals) > 0 && vals[0] != ""
	case "is_empty":
		return len(vals) == 0 || vals[0] == ""
	case "eq", "neq", "in", "not_in":
		want := strList(c.Value)
		hit := false
		for _, v := range vals {
			for _, w := range want {
				if strings.EqualFold(strings.TrimSpace(v), strings.TrimSpace(w)) {
					hit = true
				}
			}
		}
		if c.Op == "neq" || c.Op == "not_in" {
			return !hit
		}
		return hit
	case "contains":
		w := strings.ToLower(fmt.Sprint(c.Value))
		for _, v := range vals {
			if strings.Contains(strings.ToLower(v), w) {
				return true
			}
		}
		return false
	case "gte", "lte":
		a, ok1 := num(actual)
		b, ok2 := num(c.Value)
		if !ok1 || !ok2 {
			return false
		}
		if c.Op == "gte" {
			return a >= b
		}
		return a <= b
	}
	return false
}

func factStrings(v any) []string {
	switch x := v.(type) {
	case nil:
		return nil
	case []string:
		return x
	case string:
		return []string{x}
	default:
		return []string{fmt.Sprint(x)}
	}
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

func str(m map[string]any, k string) string {
	if v, ok := m[k]; ok && v != nil {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

// strList accepts a JSON array or a comma-separated string.
func strList(v any) []string {
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, i := range x {
			out = append(out, fmt.Sprint(i))
		}
		return out
	case []string:
		return x
	case string:
		var out []string
		for _, p := range strings.Split(x, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	case nil:
		return nil
	}
	return []string{fmt.Sprint(v)}
}

func intParam(m map[string]any, k string, def int) int {
	if f, ok := num(m[k]); ok {
		return int(math.Round(f))
	}
	return def
}

// ───────────── running ─────────────

func (e *Engine) run(ctx context.Context, w *Workflow, ev jobs.WorkflowEvent) error {
	extra := ev.Extra
	if extra == nil {
		extra = map[string]string{}
	}
	if !triggerMatches(w, extra) {
		return nil
	}
	s, err := e.load(ctx, ev.OrgID, ev.LeadID, ev.DealID, extra)
	if err != nil {
		if apperr.IsNotFound(err) {
			return nil // the deal was deleted before the job ran
		}
		return err
	}
	if s.deal != nil {
		extra["pipeline_id"] = s.deal.PipelineID.String()
		if !triggerMatches(w, extra) {
			return nil
		}
	}
	matched, _ := evaluate(w.Conditions, s.facts)
	var dealID *uuid.UUID
	if s.deal != nil {
		dealID = &s.deal.ID
	}
	// Claim the (workflow, event) pair; a duplicate delivery stops here.
	var runID int64
	status := "skipped"
	err = e.svc.pool.QueryRow(ctx, `INSERT INTO workflow_runs (organization_id, workflow_id, event_key, trigger, lead_id, deal_id, subject, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (workflow_id, event_key) DO NOTHING RETURNING id`,
		ev.OrgID, w.ID, ev.EventKey, ev.Trigger, s.leadID, dealID, s.label(), status).Scan(&runID)
	if database.IsNoRows(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !matched {
		_, err = e.svc.pool.Exec(ctx, `UPDATE workflow_runs SET results=$2 WHERE id=$1`, runID,
			mustJSON([]ActionResult{{Type: "conditions", Status: "skipped", Message: "conditions didn't match"}}))
		return err
	}
	c := chainFrom(ctx)
	runCtx := context.WithValue(ctx, chainKey{}, chain{ids: append(slices.Clone(ev.Chain), w.ID), depth: max(c.depth, ev.Depth)})
	results := make([]ActionResult, 0, len(w.Actions))
	ok, failed := 0, 0
	for _, a := range w.Actions {
		msg, err := e.do(runCtx, w, s, a)
		r := ActionResult{Type: a.Type, Status: "ok", Message: msg}
		switch {
		case err == errSkip:
			r.Status = "skipped"
		case err != nil:
			r.Status, r.Message = "failed", err.Error()
			failed++
		default:
			ok++
		}
		results = append(results, r)
	}
	status = "succeeded"
	if failed > 0 {
		status = "partial"
		if ok == 0 {
			status = "failed"
		}
	}
	if s.deal != nil {
		dealID = &s.deal.ID // create_deal may have opened one
	}
	if _, err := e.svc.pool.Exec(ctx, `UPDATE workflow_runs SET status=$2, results=$3, deal_id=$4 WHERE id=$1`, runID, status, mustJSON(results), dealID); err != nil {
		return err
	}
	_, err = e.svc.pool.Exec(ctx, `UPDATE workflows SET run_count = run_count + 1, last_run_at = now() WHERE id=$1`, w.ID)
	return err
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

type skipErr struct{}

func (skipErr) Error() string { return "skipped" }

var errSkip error = skipErr{}

func (e *Engine) do(ctx context.Context, w *Workflow, s *subject, a Action) (string, error) {
	p := a.Params
	actor := deals.Actor{Workflow: w.Name}
	needLead := func() error {
		if s.leadID == nil {
			return fmt.Errorf("this event has no lead")
		}
		return nil
	}
	ensureDeal := func() error {
		if s.deal != nil {
			return nil
		}
		if s.leadID != nil {
			id, err := e.deals.OpenDealForLead(ctx, s.orgID, *s.leadID)
			if err != nil {
				return err
			}
			if id != nil {
				if s.deal, err = e.deals.Get(ctx, s.orgID, *id); err != nil {
					return err
				}
				return nil
			}
		}
		return fmt.Errorf("the lead has no open deal")
	}
	pool := e.svc.pool
	switch a.Type {
	case "create_deal":
		if err := needLead(); err != nil {
			return "", err
		}
		if id, err := e.deals.OpenDealForLead(ctx, s.orgID, *s.leadID); err != nil {
			return "", err
		} else if id != nil {
			if s.deal == nil {
				s.deal, _ = e.deals.Get(ctx, s.orgID, *id)
			}
			return "the lead already has an open deal", errSkip
		}
		in := deals.Input{LeadID: s.leadID, Source: "workflow", Name: s.render(str(p, "name"))}
		if v, ok := num(p["value"]); ok {
			in.Value = &v
		}
		if id, err := uuid.Parse(str(p, "stage_id")); err == nil {
			in.StageID = &id
		} else if id, err := uuid.Parse(str(p, "pipeline_id")); err == nil {
			in.PipelineID = &id
		}
		switch str(p, "owner") {
		case "round_robin":
			if u, err := e.roundRobin(ctx, s.orgID); err != nil {
				return "", err
			} else if u != nil {
				in.OwnerID = u
			}
		case "user":
			if u, err := uuid.Parse(str(p, "user_id")); err == nil {
				in.OwnerID = &u
			}
		}
		d, err := e.deals.Create(ctx, s.orgID, actor, in)
		if err != nil {
			return "", err
		}
		s.deal = d
		if d.OwnerName != nil {
			s.vars["owner"] = *d.OwnerName
			if in.OwnerID != nil && str(p, "owner") != "" {
				if _, err := pool.Exec(ctx, `UPDATE leads SET owner_id=$3, updated_at=now() WHERE organization_id=$1 AND id=$2 AND owner_id IS NULL`, s.orgID, *s.leadID, *in.OwnerID); err != nil {
					return "", err
				}
			}
		}
		s.vars["deal"], s.vars["stage"] = d.Name, d.StageName
		return fmt.Sprintf("opened deal %q in %s", d.Name, d.StageName), nil

	case "move_deal":
		if err := ensureDeal(); err != nil {
			return "", err
		}
		sid, err := uuid.Parse(str(p, "stage_id"))
		if err != nil {
			return "", fmt.Errorf("invalid stage")
		}
		if s.deal.StageID == sid {
			return "already in that stage", errSkip
		}
		d, err := e.deals.Move(ctx, s.orgID, s.deal.ID, actor, sid, str(p, "lost_reason"))
		if err != nil {
			return "", err
		}
		s.deal = d
		return "moved to " + d.StageName, nil

	case "assign_owner":
		var owner *uuid.UUID
		if str(p, "mode") == "user" {
			u, err := uuid.Parse(str(p, "user_id"))
			if err != nil {
				return "", fmt.Errorf("invalid teammate")
			}
			var member bool
			if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_members WHERE organization_id=$1 AND user_id=$2)`, s.orgID, u).Scan(&member); err != nil {
				return "", err
			}
			if !member {
				return "", fmt.Errorf("that teammate is no longer in the workspace")
			}
			owner = &u
		} else {
			u, err := e.roundRobin(ctx, s.orgID)
			if err != nil {
				return "", err
			}
			owner = u
		}
		if owner == nil {
			return "no teammate to assign", errSkip
		}
		if s.leadID != nil {
			if _, err := pool.Exec(ctx, `UPDATE leads SET owner_id=$3, updated_at=now() WHERE organization_id=$1 AND id=$2`, s.orgID, *s.leadID, *owner); err != nil {
				return "", err
			}
		}
		if s.deal != nil {
			d, err := e.deals.Update(ctx, s.orgID, s.deal.ID, actor, deals.Input{OwnerID: owner})
			if err != nil {
				return "", err
			}
			s.deal = d
		}
		var name string
		_ = pool.QueryRow(ctx, `SELECT full_name FROM users WHERE id=$1`, *owner).Scan(&name)
		s.vars["owner"] = name
		return "assigned to " + name, nil

	case "create_task":
		title := s.render(str(p, "title"))
		due := time.Now().AddDate(0, 0, intParam(p, "due_in_days", 1))
		assignee := s.ownerID()
		if str(p, "assignee") == "user" {
			if u, err := uuid.Parse(str(p, "user_id")); err == nil {
				assignee = &u
			}
		}
		if _, err := pool.Exec(ctx, `INSERT INTO tasks (organization_id, lead_id, assignee_id, title, notes, due_at) VALUES ($1,$2,$3,$4,$5,$6)`,
			s.orgID, s.leadID, assignee, title, "Created by the workflow “"+w.Name+"”.", due); err != nil {
			return "", err
		}
		return fmt.Sprintf("task %q due %s", title, due.Format("2 Jan")), nil

	case "add_note":
		body := s.render(str(p, "body"))
		if s.leadID != nil {
			if _, err := pool.Exec(ctx, `INSERT INTO lead_notes (organization_id, lead_id, body) VALUES ($1,$2,$3)`, s.orgID, *s.leadID, body); err != nil {
				return "", err
			}
		}
		if s.deal != nil {
			if err := e.deals.AddNote(ctx, s.orgID, s.deal.ID, actor, body); err != nil {
				return "", err
			}
		}
		if s.leadID == nil && s.deal == nil {
			return "", fmt.Errorf("nothing to note on")
		}
		return "note added", nil

	case "add_tag":
		if err := needLead(); err != nil {
			return "", err
		}
		tag := strings.ToLower(strings.TrimSpace(s.render(str(p, "tag"))))
		if _, err := pool.Exec(ctx, `INSERT INTO lead_tags (organization_id, lead_id, tag) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, s.orgID, *s.leadID, tag); err != nil {
			return "", err
		}
		return "tagged " + tag, nil

	case "set_lead_status":
		if err := needLead(); err != nil {
			return "", err
		}
		st := str(p, "status")
		if _, err := pool.Exec(ctx, `UPDATE leads SET status=$3, updated_at=now() WHERE organization_id=$1 AND id=$2`, s.orgID, *s.leadID, st); err != nil {
			return "", err
		}
		_ = leads.RecordEvent(ctx, pool, s.orgID, *s.leadID, "status_changed", nil, map[string]any{"status": st, "workflow": w.Name})
		return "status set to " + st, nil

	case "stop_sequences":
		if err := needLead(); err != nil {
			return "", err
		}
		tag, err := pool.Exec(ctx, `UPDATE campaign_leads SET status='stopped', stop_reason='workflow', next_send_at=NULL, updated_at=now()
			WHERE organization_id=$1 AND lead_id=$2 AND status IN ('pending','active','sending')`, s.orgID, *s.leadID)
		if err != nil {
			return "", err
		}
		if tag.RowsAffected() == 0 {
			return "no active sequences", errSkip
		}
		return fmt.Sprintf("stopped %d sequence(s)", tag.RowsAffected()), nil

	case "notify":
		msg := s.render(str(p, "message"))
		to, err := e.recipients(ctx, s, str(p, "to"))
		if err != nil {
			return "", err
		}
		if len(to) == 0 {
			return "no one to notify", errSkip
		}
		path := "/crm"
		if s.deal != nil {
			path = "/crm?deal=" + s.deal.ID.String()
		} else if s.leadID != nil {
			path = "/leads/" + s.leadID.String()
		}
		for _, addr := range to {
			if err := e.notify.Notify(ctx, addr, w.Name+": "+s.label(), msg, path); err != nil {
				return "", err
			}
		}
		return "emailed " + strings.Join(to, ", "), nil

	case "sync_crm":
		if err := needLead(); err != nil {
			return "", err
		}
		var connected bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM integrations WHERE organization_id=$1 AND kind IN ('hubspot','salesforce','pipedrive','zoho_crm') AND status <> 'disabled')`, s.orgID).Scan(&connected); err != nil || !connected {
			return "no CRM connected", errSkip
		}
		if err := e.queue.Enqueue(ctx, jobs.QueueDefault, jobs.TypeCRMSync, jobs.CRMSync{OrgID: s.orgID, LeadIDs: []uuid.UUID{*s.leadID}}); err != nil {
			return "", err
		}
		return "queued CRM sync", nil
	}
	return "", fmt.Errorf("unknown action %s", a.Type)
}

func (s *subject) ownerID() *uuid.UUID {
	if s.deal != nil && s.deal.OwnerID != nil {
		return s.deal.OwnerID
	}
	if v, ok := s.facts["lead.owner_id"].(string); ok {
		if u, err := uuid.Parse(v); err == nil {
			return &u
		}
	}
	return nil
}

// roundRobin picks the member (not viewer) with the fewest open deals.
func (e *Engine) roundRobin(ctx context.Context, orgID uuid.UUID) (*uuid.UUID, error) {
	var id uuid.UUID
	err := e.svc.pool.QueryRow(ctx, `SELECT m.user_id FROM organization_members m
		LEFT JOIN deals d ON d.organization_id = m.organization_id AND d.owner_id = m.user_id AND d.status = 'open'
		WHERE m.organization_id=$1 AND m.role IN ('owner','admin','member')
		GROUP BY m.user_id, m.created_at ORDER BY count(d.id), m.created_at LIMIT 1`, orgID).Scan(&id)
	if database.IsNoRows(err) {
		return nil, nil
	}
	return &id, err
}

func (e *Engine) recipients(ctx context.Context, s *subject, to string) ([]string, error) {
	var q string
	var args []any
	if owner := s.ownerID(); to != "admins" && owner != nil {
		q, args = `SELECT u.email::text FROM users u JOIN organization_members m ON m.user_id=u.id AND m.organization_id=$1 WHERE u.id=$2`, []any{s.orgID, *owner}
	} else {
		q, args = `SELECT u.email::text FROM users u JOIN organization_members m ON m.user_id=u.id WHERE m.organization_id=$1 AND m.role IN ('owner','admin') ORDER BY u.email LIMIT 10`, []any{s.orgID}
	}
	rows, err := e.svc.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ───────────── time-based triggers ─────────────

// Scan fires deal.stale and deal.inactive workflows on a ticker. Event keys
// include when the deal entered its stage (or was last touched), so each
// workflow fires once per stall rather than on every scan.
func (e *Engine) Scan(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		e.ScanOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ScanOnce runs one pass of the time-based triggers.
func (e *Engine) ScanOnce(ctx context.Context) {
	rows, err := e.svc.pool.Query(ctx, `SELECT organization_id, `+wfCols+` FROM workflows WHERE enabled AND trigger IN ($1,$2)`, TrDealStale, TrDealInactive)
	if err != nil {
		e.log.Error("workflow scan", "err", err)
		return
	}
	type item struct {
		org uuid.UUID
		wf  Workflow
	}
	var items []item
	for rows.Next() {
		var it item
		var cfg, conds, acts []byte
		w := &it.wf
		if err := rows.Scan(&it.org, &w.ID, &w.Name, &w.Description, &w.Enabled, &w.Trigger, &cfg, &conds, &acts, &w.Template, &w.RunCount, &w.LastRunAt, &w.CreatedAt, &w.UpdatedAt); err != nil {
			break
		}
		_ = json.Unmarshal(cfg, &w.TriggerConfig)
		_ = json.Unmarshal(conds, &w.Conditions)
		_ = json.Unmarshal(acts, &w.Actions)
		items = append(items, it)
	}
	rows.Close()
	for _, it := range items {
		days := intParam(it.wf.TriggerConfig, "days", 0)
		var q string
		if it.wf.Trigger == TrDealStale {
			q = `SELECT d.id, d.lead_id, d.stage_entered_at, d.stage_id, floor(extract(epoch FROM now() - d.stage_entered_at) / 86400)::int
				FROM deals d JOIN pipeline_stages st ON st.id = d.stage_id
				WHERE d.organization_id=$1 AND d.status='open'
				  AND COALESCE(NULLIF($2::int, 0), st.stale_days) IS NOT NULL
				  AND d.stage_entered_at <= now() - make_interval(days => COALESCE(NULLIF($2::int, 0), st.stale_days))
				ORDER BY d.stage_entered_at LIMIT 200`
		} else {
			if days <= 0 {
				days = 14
			}
			q = `SELECT d.id, d.lead_id, d.last_activity_at, d.stage_id, floor(extract(epoch FROM now() - d.last_activity_at) / 86400)::int
				FROM deals d WHERE d.organization_id=$1 AND d.status='open' AND d.last_activity_at <= now() - make_interval(days => $2::int)
				ORDER BY d.last_activity_at LIMIT 200`
		}
		drows, err := e.svc.pool.Query(ctx, q, it.org, days)
		if err != nil {
			e.log.Error("workflow scan deals", "err", err)
			continue
		}
		var evs []jobs.WorkflowEvent
		for drows.Next() {
			var id, stage uuid.UUID
			var lead *uuid.UUID
			var since time.Time
			var n int
			if err := drows.Scan(&id, &lead, &since, &stage, &n); err != nil {
				break
			}
			dealID := id
			evs = append(evs, jobs.WorkflowEvent{OrgID: it.org, Trigger: it.wf.Trigger, LeadID: lead, DealID: &dealID,
				EventKey: it.wf.Trigger + ":" + id.String() + ":" + strconv.FormatInt(since.UnixMicro(), 10),
				Extra:    map[string]string{"stage_id": stage.String(), "days": strconv.Itoa(n)}})
		}
		drows.Close()
		wf := it.wf
		for _, ev := range evs {
			if err := e.run(ctx, &wf, ev); err != nil {
				e.log.Error("workflow scan run", "err", err, "workflow_id", wf.ID)
			}
		}
	}
}

// ───────────── dry run ─────────────

type TestResult struct {
	TriggerMatches bool           `json:"trigger_matches"`
	Matched        bool           `json:"matched"`
	Subject        string         `json:"subject"`
	Conditions     []CondResult   `json:"conditions"`
	Actions        []ActionResult `json:"actions"`
}

// Test evaluates a workflow against a lead or deal without running anything.
func (e *Engine) Test(ctx context.Context, orgID uuid.UUID, w *Workflow, leadID, dealID *uuid.UUID, classification string) (*TestResult, error) {
	extra := map[string]string{"classification": classification}
	s, err := e.load(ctx, orgID, leadID, dealID, extra)
	if err != nil {
		return nil, err
	}
	if s.deal != nil {
		extra["stage_id"], extra["pipeline_id"] = s.deal.StageID.String(), s.deal.PipelineID.String()
		extra["days"] = strconv.Itoa(s.deal.DaysInStage)
	}
	out := &TestResult{TriggerMatches: triggerMatches(w, extra), Subject: s.label()}
	out.Matched, out.Conditions = evaluate(w.Conditions, s.facts)
	out.Matched = out.Matched && out.TriggerMatches
	for _, a := range w.Actions {
		msg := ""
		for _, k := range []string{"title", "body", "message", "tag", "name"} {
			if v := str(a.Params, k); v != "" {
				msg = s.render(v)
				break
			}
		}
		out.Actions = append(out.Actions, ActionResult{Type: a.Type, Status: "would_run", Message: msg})
	}
	return out, nil
}
