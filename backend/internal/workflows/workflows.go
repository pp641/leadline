// Package workflows is "when → if → then" sales automation. A workflow
// listens for one trigger (a classified reply, a deal event, a stale deal),
// checks its conditions against the lead and deal, then runs its actions in
// order. Every run is recorded once per event, so retries and rescans never
// repeat an automation.
package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
)

type Condition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}

type Action struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params"`
}

type Workflow struct {
	ID            uuid.UUID      `json:"id"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Enabled       bool           `json:"enabled"`
	Trigger       string         `json:"trigger"`
	TriggerConfig map[string]any `json:"trigger_config"`
	Conditions    []Condition    `json:"conditions"`
	Actions       []Action       `json:"actions"`
	Template      string         `json:"template"`
	RunCount      int            `json:"run_count"`
	LastRunAt     *time.Time     `json:"last_run_at"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type ActionResult struct {
	Type    string `json:"type"`
	Status  string `json:"status"` // ok | skipped | failed
	Message string `json:"message"`
}

type Run struct {
	ID          int64          `json:"id"`
	Trigger     string         `json:"trigger"`
	LeadID      *uuid.UUID     `json:"lead_id"`
	DealID      *uuid.UUID     `json:"deal_id"`
	Subject     string         `json:"subject"`
	Status      string         `json:"status"`
	Results     []ActionResult `json:"results"`
	CreatedAt   time.Time      `json:"created_at"`
	CompanyName *string        `json:"company_name"`
}

// ───────────── vocabulary ─────────────

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type FieldDef struct {
	Field   string   `json:"field"`
	Label   string   `json:"label"`
	Type    string   `json:"type"` // text | number | enum | tags | user | stage
	Options []Option `json:"options,omitempty"`
}

type TriggerDef struct {
	Trigger     string `json:"trigger"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Group       string `json:"group"`
}

type ActionDef struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	Description string `json:"description"`
	NeedsDeal   bool   `json:"needs_deal"`
}

const (
	TrReply        = "reply.classified"
	TrDealCreated  = "deal.created"
	TrStageChanged = "deal.stage_changed"
	TrDealWon      = "deal.won"
	TrDealLost     = "deal.lost"
	TrDealStale    = "deal.stale"
	TrDealInactive = "deal.inactive"
)

var Triggers = []TriggerDef{
	{TrReply, "A reply is classified", "A prospect replies to a campaign and the reply gets a classification (interested, not now, …).", "Engagement"},
	{TrDealCreated, "A deal is created", "A deal is added to a pipeline, by a person or another workflow.", "Deals"},
	{TrStageChanged, "A deal changes stage", "A deal moves to another stage. Optionally only when it enters a specific stage.", "Deals"},
	{TrDealWon, "A deal is won", "A deal enters a Won stage.", "Deals"},
	{TrDealLost, "A deal is lost", "A deal enters a Lost stage.", "Deals"},
	{TrDealStale, "A deal is stuck in a stage", "An open deal has sat in one stage longer than the stage's limit (or a number of days you set).", "Time-based"},
	{TrDealInactive, "A deal has no activity", "Nobody has touched an open deal for a number of days.", "Time-based"},
}

var classificationOptions = []Option{
	{"interested", "Interested"}, {"positive", "Positive"}, {"question", "Question"}, {"not_now", "Not now"},
	{"not_interested", "Not interested"}, {"unsubscribe", "Unsubscribe"}, {"wrong_person", "Wrong person"},
	{"out_of_office", "Out of office"}, {"spam", "Spam"}, {"unknown", "Unknown"},
}

var leadStatusOptions = []Option{
	{"new", "New"}, {"contacted", "Contacted"}, {"replied", "Replied"}, {"interested", "Interested"},
	{"qualified", "Qualified"}, {"meeting", "Meeting"}, {"won", "Won"}, {"lost", "Lost"}, {"unsubscribed", "Unsubscribed"},
}

var Fields = []FieldDef{
	{Field: "reply.classification", Label: "Reply classification", Type: "enum", Options: classificationOptions},
	{Field: "lead.industry", Label: "Lead industry", Type: "text"},
	{Field: "lead.city", Label: "Lead city", Type: "text"},
	{Field: "lead.state", Label: "Lead state", Type: "text"},
	{Field: "lead.country", Label: "Lead country", Type: "text"},
	{Field: "lead.source", Label: "Lead source", Type: "text"},
	{Field: "lead.status", Label: "Lead status", Type: "enum", Options: leadStatusOptions},
	{Field: "lead.tags", Label: "Lead tags", Type: "tags"},
	{Field: "lead.lead_score", Label: "Lead score", Type: "number"},
	{Field: "lead.icp_fit", Label: "ICP fit", Type: "number"},
	{Field: "lead.owner_id", Label: "Lead owner", Type: "user"},
	{Field: "deal.value", Label: "Deal value", Type: "number"},
	{Field: "deal.stage_id", Label: "Deal stage", Type: "stage"},
	{Field: "deal.status", Label: "Deal status", Type: "enum", Options: []Option{{"open", "Open"}, {"won", "Won"}, {"lost", "Lost"}}},
	{Field: "deal.owner_id", Label: "Deal owner", Type: "user"},
	{Field: "deal.lost_reason", Label: "Lost reason", Type: "text"},
	{Field: "deal.source", Label: "Deal source", Type: "text"},
	{Field: "deal.days_in_stage", Label: "Days in stage", Type: "number"},
}

var Ops = []Option{
	{"eq", "is"}, {"neq", "is not"}, {"in", "is any of"}, {"not_in", "is none of"}, {"contains", "contains"},
	{"gte", "is at least"}, {"lte", "is at most"}, {"is_set", "is set"}, {"is_empty", "is empty"},
}

var ActionDefs = []ActionDef{
	{"create_deal", "Create a deal", "Open a deal for the lead (skipped if the lead already has an open deal).", false},
	{"move_deal", "Move the deal", "Move the deal to another stage.", true},
	{"assign_owner", "Assign an owner", "Give the lead and its deal an owner — a specific teammate or round-robin.", false},
	{"create_task", "Create a task", "Add a task with a due date for the owner or a teammate.", false},
	{"add_note", "Add a note", "Log a note on the lead's or deal's timeline.", false},
	{"add_tag", "Tag the lead", "Add a tag to the lead.", false},
	{"set_lead_status", "Set lead status", "Change the lead's status.", false},
	{"stop_sequences", "Stop sequences", "Stop every campaign sequence still emailing the lead.", false},
	{"notify", "Notify by email", "Email the owner or the workspace admins.", false},
	{"sync_crm", "Sync to CRM", "Push the lead to your connected CRMs.", false},
}

var LostReasons = []string{"Price", "Timing", "Went with a competitor", "No budget", "No response", "Not a fit", "Other"}

type Meta struct {
	Triggers    []TriggerDef `json:"triggers"`
	Fields      []FieldDef   `json:"fields"`
	Ops         []Option     `json:"ops"`
	Actions     []ActionDef  `json:"actions"`
	Variables   []string     `json:"variables"`
	LostReasons []string     `json:"lost_reasons"`
}

func MetaInfo() Meta {
	return Meta{Triggers, Fields, Ops, ActionDefs,
		[]string{"{{company}}", "{{deal}}", "{{stage}}", "{{value}}", "{{days}}", "{{owner}}", "{{classification}}"}, LostReasons}
}

// ───────────── templates ─────────────

type Template struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Input
}

var Templates = []Template{
	{Key: "interested_reply", Name: "Interested reply → deal + discovery call",
		Description: "When someone replies interested, open a deal, assign an owner round-robin and create a task to book the discovery call.", Category: "Engagement",
		Input: Input{Trigger: TrReply, TriggerConfig: map[string]any{"classifications": []any{"interested", "positive"}}, Actions: []Action{
			{"create_deal", map[string]any{"owner": "round_robin"}},
			{"create_task", map[string]any{"title": "Book a discovery call with {{company}}", "due_in_days": 1, "assignee": "owner"}},
		}}},
	{Key: "not_now_nurture", Name: "Not now → nurture and follow up later",
		Description: "When a prospect says not now, tag them for nurture, stop their sequences and set a reminder to reach out in 60 days.", Category: "Engagement",
		Input: Input{Trigger: TrReply, TriggerConfig: map[string]any{"classifications": []any{"not_now"}}, Actions: []Action{
			{"add_tag", map[string]any{"tag": "nurture"}},
			{"stop_sequences", map[string]any{}},
			{"create_task", map[string]any{"title": "Check back in with {{company}}", "due_in_days": 60, "assignee": "owner"}},
		}}},
	{Key: "proposal_follow_up", Name: "Proposal sent → follow up in 3 days",
		Description: "When a deal enters Proposal sent, remind the owner to follow up three days later.", Category: "Deals",
		Input: Input{Trigger: TrStageChanged, TriggerConfig: map[string]any{"to_stage": "Proposal sent"}, Actions: []Action{
			{"create_task", map[string]any{"title": "Follow up on the proposal for {{deal}}", "due_in_days": 3, "assignee": "owner"}},
		}}},
	{Key: "stuck_deal_alert", Name: "Stuck deal alert",
		Description: "When an open deal sits in a stage longer than the stage allows, email the owner and add a task to unblock it.", Category: "Time-based",
		Input: Input{Trigger: TrDealStale, TriggerConfig: map[string]any{}, Actions: []Action{
			{"notify", map[string]any{"to": "owner", "message": "{{deal}} has been in {{stage}} for {{days}} days."}},
			{"create_task", map[string]any{"title": "Unblock {{deal}} ({{days}} days in {{stage}})", "due_in_days": 1, "assignee": "owner"}},
		}}},
	{Key: "won_onboarding", Name: "Deal won → onboarding handoff",
		Description: "When a deal is won, create kickoff tasks, note the win and push the customer to your CRM.", Category: "Deals",
		Input: Input{Trigger: TrDealWon, TriggerConfig: map[string]any{}, Actions: []Action{
			{"create_task", map[string]any{"title": "Send the welcome pack to {{company}}", "due_in_days": 1, "assignee": "owner"}},
			{"create_task", map[string]any{"title": "Schedule the kickoff call with {{company}}", "due_in_days": 3, "assignee": "owner"}},
			{"add_tag", map[string]any{"tag": "customer"}},
			{"sync_crm", map[string]any{}},
		}}},
	{Key: "lost_timing_revisit", Name: "Lost on timing → revisit in 90 days",
		Description: "When a deal is lost because of timing, tag the lead and set a reminder to revisit it in three months.", Category: "Deals",
		Input: Input{Trigger: TrDealLost, TriggerConfig: map[string]any{},
			Conditions: []Condition{{Field: "deal.lost_reason", Op: "eq", Value: "Timing"}},
			Actions: []Action{
				{"add_tag", map[string]any{"tag": "revisit"}},
				{"create_task", map[string]any{"title": "Revisit {{company}} — lost on timing 90 days ago", "due_in_days": 90, "assignee": "owner"}},
			}}},
}

func templateByKey(key string) (*Template, bool) {
	for i := range Templates {
		if Templates[i].Key == key {
			return &Templates[i], true
		}
	}
	return nil, false
}

// ───────────── CRUD ─────────────

type Input struct {
	Name          string         `json:"name,omitempty"`
	Description   string         `json:"description,omitempty"`
	Enabled       *bool          `json:"enabled,omitempty"`
	Trigger       string         `json:"trigger"`
	TriggerConfig map[string]any `json:"trigger_config"`
	Conditions    []Condition    `json:"conditions"`
	Actions       []Action       `json:"actions"`
	Template      string         `json:"template,omitempty"`
}

// PipelineEnsurer creates an organization's default pipeline on first use.
type PipelineEnsurer interface {
	EnsureDefault(ctx context.Context, orgID uuid.UUID) error
}

type Service struct {
	pool      *pgxpool.Pool
	audit     *audit.Log
	pipelines PipelineEnsurer
}

func NewService(pool *pgxpool.Pool, a *audit.Log, pipelines PipelineEnsurer) *Service {
	return &Service{pool: pool, audit: a, pipelines: pipelines}
}

var fieldTypes = func() map[string]string {
	m := map[string]string{}
	for _, f := range Fields {
		m[f.Field] = f.Type
	}
	return m
}()

func validate(in *Input) error {
	in.Name = strings.TrimSpace(in.Name)
	errs := map[string]string{}
	if in.Name == "" {
		errs["name"] = "is required"
	}
	if !slices.ContainsFunc(Triggers, func(t TriggerDef) bool { return t.Trigger == in.Trigger }) {
		errs["trigger"] = "unknown trigger"
	}
	if in.TriggerConfig == nil {
		in.TriggerConfig = map[string]any{}
	}
	if in.Conditions == nil {
		in.Conditions = []Condition{}
	}
	for i, c := range in.Conditions {
		if _, ok := fieldTypes[c.Field]; !ok {
			errs[fmt.Sprintf("conditions.%d", i)] = "unknown field " + c.Field
		} else if !slices.ContainsFunc(Ops, func(o Option) bool { return o.Value == c.Op }) {
			errs[fmt.Sprintf("conditions.%d", i)] = "unknown operator " + c.Op
		}
	}
	if len(in.Actions) == 0 {
		errs["actions"] = "add at least one action"
	}
	if len(in.Actions) > 10 || len(in.Conditions) > 20 {
		errs["actions"] = "at most 10 actions and 20 conditions"
	}
	for i := range in.Actions {
		a := &in.Actions[i]
		if a.Params == nil {
			a.Params = map[string]any{}
		}
		key := fmt.Sprintf("actions.%d", i)
		switch a.Type {
		case "create_task":
			if str(a.Params, "title") == "" {
				errs[key] = "task title is required"
			}
		case "add_note":
			if str(a.Params, "body") == "" {
				errs[key] = "note text is required"
			}
		case "add_tag":
			if str(a.Params, "tag") == "" {
				errs[key] = "tag is required"
			}
		case "set_lead_status":
			if !slices.ContainsFunc(leadStatusOptions, func(o Option) bool { return o.Value == str(a.Params, "status") }) {
				errs[key] = "pick a lead status"
			}
		case "move_deal":
			if str(a.Params, "stage_id") == "" {
				errs[key] = "pick a stage"
			}
		case "notify":
			if str(a.Params, "message") == "" {
				errs[key] = "message is required"
			}
		case "assign_owner":
			if str(a.Params, "mode") == "user" && str(a.Params, "user_id") == "" {
				errs[key] = "pick a teammate"
			}
		case "create_deal", "stop_sequences", "sync_crm":
		default:
			errs[key] = "unknown action " + a.Type
		}
	}
	if len(errs) > 0 {
		return apperr.Invalid("validation failed", errs)
	}
	return nil
}

const wfCols = `id, name, description, enabled, trigger, trigger_config, conditions, actions, template, run_count, last_run_at, created_at, updated_at`

func scanWorkflow(row pgx.Row) (*Workflow, error) {
	var w Workflow
	var cfg, conds, acts []byte
	if err := row.Scan(&w.ID, &w.Name, &w.Description, &w.Enabled, &w.Trigger, &cfg, &conds, &acts, &w.Template, &w.RunCount, &w.LastRunAt, &w.CreatedAt, &w.UpdatedAt); err != nil {
		if database.IsNoRows(err) {
			return nil, apperr.NotFound("workflow")
		}
		return nil, err
	}
	_ = json.Unmarshal(cfg, &w.TriggerConfig)
	_ = json.Unmarshal(conds, &w.Conditions)
	_ = json.Unmarshal(acts, &w.Actions)
	if w.TriggerConfig == nil {
		w.TriggerConfig = map[string]any{}
	}
	if w.Conditions == nil {
		w.Conditions = []Condition{}
	}
	return &w, nil
}

func (s *Service) List(ctx context.Context, orgID uuid.UUID) ([]Workflow, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+wfCols+` FROM workflows WHERE organization_id=$1 ORDER BY created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Workflow{}
	for rows.Next() {
		w, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, orgID, id uuid.UUID) (*Workflow, error) {
	return scanWorkflow(s.pool.QueryRow(ctx, `SELECT `+wfCols+` FROM workflows WHERE organization_id=$1 AND id=$2`, orgID, id))
}

// enabledFor returns the organization's enabled workflows for a trigger.
func (s *Service) enabledFor(ctx context.Context, orgID uuid.UUID, trigger string) ([]Workflow, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+wfCols+` FROM workflows WHERE organization_id=$1 AND trigger=$2 AND enabled ORDER BY created_at`, orgID, trigger)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workflow
	for rows.Next() {
		w, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

// Create saves a workflow; with only a template key it is created from that
// template (enabled), resolving stage names to the default pipeline's stages.
func (s *Service) Create(ctx context.Context, p auth.Principal, in Input) (*Workflow, error) {
	if in.Template != "" && in.Trigger == "" {
		t, ok := templateByKey(in.Template)
		if !ok {
			return nil, apperr.Invalid("validation failed", map[string]string{"template": "unknown template"})
		}
		tpl := t.Input
		tpl.Template, tpl.Name, tpl.Description = t.Key, t.Name, t.Description
		if in.Name != "" {
			tpl.Name = in.Name
		}
		if err := s.resolveStageNames(ctx, p.OrgID, &tpl); err != nil {
			return nil, err
		}
		in = tpl
	}
	if err := validate(&in); err != nil {
		return nil, err
	}
	enabled := in.Enabled == nil || *in.Enabled
	cfg, _ := json.Marshal(in.TriggerConfig)
	conds, _ := json.Marshal(in.Conditions)
	acts, _ := json.Marshal(in.Actions)
	w, err := scanWorkflow(s.pool.QueryRow(ctx, `INSERT INTO workflows (organization_id, name, description, enabled, trigger, trigger_config, conditions, actions, template, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+wfCols, p.OrgID, in.Name, strings.TrimSpace(in.Description), enabled, in.Trigger, cfg, conds, acts, in.Template, p.UserID))
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "workflow.created", EntityType: "workflow", EntityID: w.ID.String(), Metadata: map[string]any{"name": w.Name}})
	return w, nil
}

// resolveStageNames turns a template's {"to_stage": "Proposal sent"} into the
// matching stage id of the organization's default pipeline.
func (s *Service) resolveStageNames(ctx context.Context, orgID uuid.UUID, in *Input) error {
	name, ok := in.TriggerConfig["to_stage"].(string)
	if !ok {
		return nil
	}
	// A workspace that hasn't opened the pipeline yet has no stages to match.
	if err := s.pipelines.EnsureDefault(ctx, orgID); err != nil {
		return err
	}
	cfg := map[string]any{}
	for k, v := range in.TriggerConfig {
		if k != "to_stage" {
			cfg[k] = v
		}
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT st.id FROM pipeline_stages st JOIN pipelines p ON p.id = st.pipeline_id
		WHERE p.organization_id=$1 AND p.is_default AND lower(st.name) = lower($2) LIMIT 1`, orgID, name).Scan(&id)
	if err == nil {
		cfg["to_stage_id"] = id.String()
	} else if !database.IsNoRows(err) {
		return err
	}
	in.TriggerConfig = cfg
	return nil
}

func (s *Service) Update(ctx context.Context, p auth.Principal, id uuid.UUID, in Input) (*Workflow, error) {
	cur, err := s.Get(ctx, p.OrgID, id)
	if err != nil {
		return nil, err
	}
	enabled := cur.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	// A bare {"enabled": …} toggles the workflow without re-validating it.
	if in.Trigger == "" && in.Name == "" && in.Actions == nil {
		in = Input{Name: cur.Name, Description: cur.Description, Trigger: cur.Trigger, TriggerConfig: cur.TriggerConfig, Conditions: cur.Conditions, Actions: cur.Actions}
	}
	if err := validate(&in); err != nil {
		return nil, err
	}
	cfg, _ := json.Marshal(in.TriggerConfig)
	conds, _ := json.Marshal(in.Conditions)
	acts, _ := json.Marshal(in.Actions)
	w, err := scanWorkflow(s.pool.QueryRow(ctx, `UPDATE workflows SET name=$3, description=$4, enabled=$5, trigger=$6, trigger_config=$7, conditions=$8, actions=$9, updated_at=now()
		WHERE organization_id=$1 AND id=$2 RETURNING `+wfCols, p.OrgID, id, in.Name, strings.TrimSpace(in.Description), enabled, in.Trigger, cfg, conds, acts))
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "workflow.updated", EntityType: "workflow", EntityID: id.String(), Metadata: map[string]any{"enabled": enabled}})
	return w, nil
}

func (s *Service) Delete(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM workflows WHERE organization_id=$1 AND id=$2`, p.OrgID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("workflow")
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "workflow.deleted", EntityType: "workflow", EntityID: id.String()})
	return nil
}

func (s *Service) Runs(ctx context.Context, orgID, workflowID uuid.UUID, limit int) ([]Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT r.id, r.trigger, r.lead_id, r.deal_id, r.subject, r.status, r.results, r.created_at, l.company_name
		FROM workflow_runs r LEFT JOIN leads l ON l.id = r.lead_id
		WHERE r.organization_id=$1 AND r.workflow_id=$2 ORDER BY r.created_at DESC LIMIT $3`, orgID, workflowID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		var raw []byte
		if err := rows.Scan(&r.ID, &r.Trigger, &r.LeadID, &r.DealID, &r.Subject, &r.Status, &raw, &r.CreatedAt, &r.CompanyName); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &r.Results)
		if r.Results == nil {
			r.Results = []ActionResult{}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
