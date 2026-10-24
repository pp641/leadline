// Package deals is the sales pipeline: configurable pipelines and stages,
// deals with a value and an expected close date, a board with column totals
// and a per-deal timeline. Lifecycle events (created, stage changed, won,
// lost) go to webhooks and to the OnEvent hook that drives workflows.
package deals

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/internal/leads"
	"leadline/internal/webhooks"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
)

// Event triggers emitted to OnEvent.
const (
	EvCreated      = "deal.created"
	EvStageChanged = "deal.stage_changed"
	EvWon          = "deal.won"
	EvLost         = "deal.lost"
)

type Stage struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Position    int       `json:"position"`
	Probability int       `json:"probability"`
	Kind        string    `json:"kind"` // open | won | lost
	StaleDays   *int      `json:"stale_days"`
}

type Pipeline struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	IsDefault bool      `json:"is_default"`
	Stages    []Stage   `json:"stages"`
	CreatedAt time.Time `json:"created_at"`
}

type Deal struct {
	ID             uuid.UUID  `json:"id"`
	PipelineID     uuid.UUID  `json:"pipeline_id"`
	StageID        uuid.UUID  `json:"stage_id"`
	StageName      string     `json:"stage_name"`
	Probability    int        `json:"probability"`
	LeadID         *uuid.UUID `json:"lead_id"`
	CompanyName    string     `json:"company_name"`
	City           string     `json:"city"`
	Name           string     `json:"name"`
	Value          float64    `json:"value"`
	Currency       string     `json:"currency"`
	ExpectedClose  *string    `json:"expected_close"`
	OwnerID        *uuid.UUID `json:"owner_id"`
	OwnerName      *string    `json:"owner_name"`
	Status         string     `json:"status"`
	LostReason     string     `json:"lost_reason"`
	NextStep       string     `json:"next_step"`
	NextStepAt     *time.Time `json:"next_step_at"`
	Source         string     `json:"source"`
	StageEnteredAt time.Time  `json:"stage_entered_at"`
	LastActivityAt time.Time  `json:"last_activity_at"`
	DaysInStage    int        `json:"days_in_stage"`
	Stale          bool       `json:"stale"`
	OpenTasks      int        `json:"open_tasks"`
	ClosedAt       *time.Time `json:"closed_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type Event struct {
	ID        int64           `json:"id"`
	EventType string          `json:"event_type"`
	ActorName *string         `json:"actor_name"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

// Actor is who changed a deal: a user, or a workflow (UserID nil).
type Actor struct {
	UserID   *uuid.UUID
	Workflow string // workflow name when automated
}

func UserActor(p auth.Principal) Actor { u := p.UserID; return Actor{UserID: &u} }

type Service struct {
	pool  *pgxpool.Pool
	audit *audit.Log
	// OnEvent is called after a deal event commits (set to the workflow engine).
	OnEvent func(ctx context.Context, orgID uuid.UUID, trigger string, d *Deal)
}

func NewService(pool *pgxpool.Pool, a *audit.Log) *Service { return &Service{pool: pool, audit: a} }

func (s *Service) emit(ctx context.Context, orgID uuid.UUID, trigger string, d *Deal) {
	if s.OnEvent != nil {
		s.OnEvent(ctx, orgID, trigger, d)
	}
}

// ───────────── pipelines ─────────────

var defaultStages = []Stage{
	{Name: "Engaged", Probability: 10, Kind: "open", StaleDays: ptr(7)},
	{Name: "Discovery call", Probability: 20, Kind: "open", StaleDays: ptr(7)},
	{Name: "Qualified", Probability: 40, Kind: "open", StaleDays: ptr(10)},
	{Name: "Proposal sent", Probability: 60, Kind: "open", StaleDays: ptr(7)},
	{Name: "Negotiation", Probability: 80, Kind: "open", StaleDays: ptr(10)},
	{Name: "Won", Probability: 100, Kind: "won"},
	{Name: "Lost", Probability: 0, Kind: "lost"},
}

func ptr[T any](v T) *T { return &v }

// EnsureDefault creates the organization's first pipeline if it has none.
func (s *Service) EnsureDefault(ctx context.Context, orgID uuid.UUID) error {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pipelines WHERE organization_id=$1)`, orgID).Scan(&exists); err != nil || exists {
		return err
	}
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var pid uuid.UUID
		// The advisory lock stops two first requests from creating two pipelines.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('pipelines:' || $1::text))`, orgID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pipelines WHERE organization_id=$1)`, orgID).Scan(&exists); err != nil || exists {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO pipelines (organization_id, name, is_default) VALUES ($1,'Sales pipeline',true) RETURNING id`, orgID).Scan(&pid); err != nil {
			return err
		}
		return insertStages(ctx, tx, orgID, pid, defaultStages)
	})
}

func insertStages(ctx context.Context, tx pgx.Tx, orgID, pid uuid.UUID, stages []Stage) error {
	for i, st := range stages {
		if _, err := tx.Exec(ctx, `INSERT INTO pipeline_stages (organization_id, pipeline_id, name, position, probability, kind, stale_days) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			orgID, pid, st.Name, i, st.Probability, st.Kind, st.StaleDays); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Pipelines(ctx context.Context, orgID uuid.UUID) ([]Pipeline, error) {
	if err := s.EnsureDefault(ctx, orgID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, name, is_default, created_at FROM pipelines WHERE organization_id=$1 ORDER BY is_default DESC, created_at`, orgID)
	if err != nil {
		return nil, err
	}
	var out []Pipeline
	for rows.Next() {
		var p Pipeline
		if err := rows.Scan(&p.ID, &p.Name, &p.IsDefault, &p.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	for i := range out {
		if out[i].Stages, err = s.stages(ctx, s.pool, orgID, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) stages(ctx context.Context, db database.DBTX, orgID, pid uuid.UUID) ([]Stage, error) {
	rows, err := db.Query(ctx, `SELECT id, name, position, probability, kind, stale_days FROM pipeline_stages WHERE organization_id=$1 AND pipeline_id=$2 ORDER BY position`, orgID, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Stage{}
	for rows.Next() {
		var st Stage
		if err := rows.Scan(&st.ID, &st.Name, &st.Position, &st.Probability, &st.Kind, &st.StaleDays); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Service) Pipeline(ctx context.Context, orgID, id uuid.UUID) (*Pipeline, error) {
	var p Pipeline
	err := s.pool.QueryRow(ctx, `SELECT id, name, is_default, created_at FROM pipelines WHERE organization_id=$1 AND id=$2`, orgID, id).Scan(&p.ID, &p.Name, &p.IsDefault, &p.CreatedAt)
	if database.IsNoRows(err) {
		return nil, apperr.NotFound("pipeline")
	}
	if err != nil {
		return nil, err
	}
	p.Stages, err = s.stages(ctx, s.pool, orgID, id)
	return &p, err
}

type PipelineInput struct {
	Name   string  `json:"name"`
	Stages []Stage `json:"stages"`
}

func (in *PipelineInput) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	errs := map[string]string{}
	if in.Name == "" {
		errs["name"] = "is required"
	}
	open, won, lost := 0, 0, 0
	for i := range in.Stages {
		st := &in.Stages[i]
		st.Name = strings.TrimSpace(st.Name)
		if st.Name == "" {
			errs["stages"] = "every stage needs a name"
		}
		switch st.Kind {
		case "", "open":
			st.Kind = "open"
			open++
		case "won":
			won++
			st.Probability = 100
		case "lost":
			lost++
			st.Probability = 0
		default:
			errs["stages"] = "stage kind must be open, won or lost"
		}
		st.Probability = max(0, min(100, st.Probability))
	}
	if open == 0 || won != 1 || lost != 1 {
		errs["stages"] = "a pipeline needs at least one open stage, exactly one Won stage and exactly one Lost stage"
	}
	if len(in.Stages) > 20 {
		errs["stages"] = "at most 20 stages"
	}
	if len(errs) > 0 {
		return apperr.Invalid("validation failed", errs)
	}
	return nil
}

func (s *Service) CreatePipeline(ctx context.Context, p auth.Principal, in PipelineInput) (*Pipeline, error) {
	if len(in.Stages) == 0 {
		in.Stages = defaultStages
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	var pid uuid.UUID
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO pipelines (organization_id, name, is_default) VALUES ($1,$2, NOT EXISTS (SELECT 1 FROM pipelines WHERE organization_id=$1)) RETURNING id`,
			p.OrgID, in.Name).Scan(&pid); err != nil {
			return err
		}
		return insertStages(ctx, tx, p.OrgID, pid, in.Stages)
	})
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "pipeline.created", EntityType: "pipeline", EntityID: pid.String()})
	return s.Pipeline(ctx, p.OrgID, pid)
}

// UpdatePipeline renames and replaces the stage list. Stages keep their id
// when sent back with it; removed stages must be empty.
func (s *Service) UpdatePipeline(ctx context.Context, p auth.Principal, id uuid.UUID, in PipelineInput) (*Pipeline, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE pipelines SET name=$3 WHERE organization_id=$1 AND id=$2`, p.OrgID, id, in.Name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("pipeline")
		}
		keep := []uuid.UUID{}
		for i, st := range in.Stages {
			if st.ID != uuid.Nil {
				t, err := tx.Exec(ctx, `UPDATE pipeline_stages SET name=$4, position=$5, probability=$6, kind=$7, stale_days=$8
					WHERE organization_id=$1 AND pipeline_id=$2 AND id=$3`, p.OrgID, id, st.ID, st.Name, i, st.Probability, st.Kind, st.StaleDays)
				if err != nil {
					return err
				}
				if t.RowsAffected() == 1 {
					keep = append(keep, st.ID)
					continue
				}
			}
			var nid uuid.UUID
			if err := tx.QueryRow(ctx, `INSERT INTO pipeline_stages (organization_id, pipeline_id, name, position, probability, kind, stale_days) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
				p.OrgID, id, st.Name, i, st.Probability, st.Kind, st.StaleDays).Scan(&nid); err != nil {
				return err
			}
			keep = append(keep, nid)
		}
		var inUse int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM deals WHERE organization_id=$1 AND pipeline_id=$2 AND NOT (stage_id = ANY($3))`, p.OrgID, id, keep).Scan(&inUse); err != nil {
			return err
		}
		if inUse > 0 {
			return apperr.Conflict("stage_in_use", "move the deals out of a stage before removing it")
		}
		_, err = tx.Exec(ctx, `DELETE FROM pipeline_stages WHERE organization_id=$1 AND pipeline_id=$2 AND NOT (id = ANY($3))`, p.OrgID, id, keep)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "pipeline.updated", EntityType: "pipeline", EntityID: id.String()})
	return s.Pipeline(ctx, p.OrgID, id)
}

func (s *Service) DeletePipeline(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	var isDefault bool
	var deals int
	err := s.pool.QueryRow(ctx, `SELECT is_default, (SELECT count(*) FROM deals WHERE pipeline_id=$2) FROM pipelines WHERE organization_id=$1 AND id=$2`, p.OrgID, id).Scan(&isDefault, &deals)
	if database.IsNoRows(err) {
		return apperr.NotFound("pipeline")
	}
	if err != nil {
		return err
	}
	if isDefault {
		return apperr.Conflict("default_pipeline", "the default pipeline can't be deleted")
	}
	if deals > 0 {
		return apperr.Conflict("pipeline_in_use", "move or delete this pipeline's deals first")
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM pipelines WHERE organization_id=$1 AND id=$2`, p.OrgID, id)
	return err
}

// ───────────── deals ─────────────

const dealCols = `d.id, d.pipeline_id, d.stage_id, st.name, st.probability, d.lead_id, COALESCE(l.company_name, ''), COALESCE(l.city, ''),
	d.name, d.value::float8, d.currency, to_char(d.expected_close, 'YYYY-MM-DD'), d.owner_id, u.full_name, d.status, d.lost_reason,
	d.next_step, d.next_step_at, d.source, d.stage_entered_at, d.last_activity_at, st.stale_days,
	(SELECT count(*) FROM tasks t WHERE t.lead_id = d.lead_id AND d.lead_id IS NOT NULL AND t.completed_at IS NULL),
	d.closed_at, d.created_at, d.updated_at`

const dealFrom = ` FROM deals d JOIN pipeline_stages st ON st.id = d.stage_id
	LEFT JOIN leads l ON l.id = d.lead_id LEFT JOIN users u ON u.id = d.owner_id `

func scanDeal(row pgx.Row) (*Deal, error) {
	var d Deal
	var staleDays *int
	err := row.Scan(&d.ID, &d.PipelineID, &d.StageID, &d.StageName, &d.Probability, &d.LeadID, &d.CompanyName, &d.City,
		&d.Name, &d.Value, &d.Currency, &d.ExpectedClose, &d.OwnerID, &d.OwnerName, &d.Status, &d.LostReason,
		&d.NextStep, &d.NextStepAt, &d.Source, &d.StageEnteredAt, &d.LastActivityAt, &staleDays, &d.OpenTasks,
		&d.ClosedAt, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		if database.IsNoRows(err) {
			return nil, apperr.NotFound("deal")
		}
		return nil, err
	}
	d.DaysInStage = int(time.Since(d.StageEnteredAt).Hours() / 24)
	d.Stale = d.Status == "open" && staleDays != nil && d.DaysInStage >= *staleDays
	return &d, nil
}

func (s *Service) Get(ctx context.Context, orgID, id uuid.UUID) (*Deal, error) {
	return scanDeal(s.pool.QueryRow(ctx, `SELECT `+dealCols+dealFrom+`WHERE d.organization_id=$1 AND d.id=$2`, orgID, id))
}

type ListFilter struct {
	PipelineID *uuid.UUID
	LeadID     *uuid.UUID
	OwnerID    *uuid.UUID
	Status     string
	Q          string
	Limit      int
}

func (s *Service) List(ctx context.Context, orgID uuid.UUID, f ListFilter) ([]Deal, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 500
	}
	rows, err := s.pool.Query(ctx, `SELECT `+dealCols+dealFrom+`
		WHERE d.organization_id=$1 AND ($2::uuid IS NULL OR d.pipeline_id=$2) AND ($3::uuid IS NULL OR d.lead_id=$3)
		  AND ($4::uuid IS NULL OR d.owner_id=$4) AND ($5 = '' OR d.status=$5)
		  AND ($6 = '' OR d.name ILIKE '%'||$6||'%' OR l.company_name ILIKE '%'||$6||'%')
		ORDER BY d.updated_at DESC LIMIT $7`, orgID, f.PipelineID, f.LeadID, f.OwnerID, f.Status, f.Q, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deal{}
	for rows.Next() {
		d, err := scanDeal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

type Column struct {
	Stage    Stage   `json:"stage"`
	Deals    []Deal  `json:"deals"`
	Count    int     `json:"count"`
	Value    float64 `json:"value"`
	Weighted float64 `json:"weighted"`
}

type Board struct {
	Pipeline Pipeline `json:"pipeline"`
	Columns  []Column `json:"columns"`
	Totals   struct {
		OpenCount     int     `json:"open_count"`
		OpenValue     float64 `json:"open_value"`
		WeightedValue float64 `json:"weighted_value"`
		WonValue30d   float64 `json:"won_value_30d"`
		WonCount30d   int     `json:"won_count_30d"`
		StaleCount    int     `json:"stale_count"`
	} `json:"totals"`
}

// Board returns a pipeline's stages with their deals and value totals. Won and
// lost columns show deals closed in the last 90 days.
func (s *Service) Board(ctx context.Context, orgID uuid.UUID, pipelineID *uuid.UUID, ownerID *uuid.UUID) (*Board, error) {
	pipes, err := s.Pipelines(ctx, orgID)
	if err != nil {
		return nil, err
	}
	var pipe *Pipeline
	for i := range pipes {
		if (pipelineID == nil && pipes[i].IsDefault) || (pipelineID != nil && pipes[i].ID == *pipelineID) {
			pipe = &pipes[i]
		}
	}
	if pipe == nil {
		if pipelineID != nil {
			return nil, apperr.NotFound("pipeline")
		}
		pipe = &pipes[0]
	}
	list, err := s.List(ctx, orgID, ListFilter{PipelineID: &pipe.ID, OwnerID: ownerID, Limit: 1000})
	if err != nil {
		return nil, err
	}
	b := &Board{Pipeline: *pipe}
	idx := map[uuid.UUID]int{}
	for i, st := range pipe.Stages {
		b.Columns = append(b.Columns, Column{Stage: st, Deals: []Deal{}})
		idx[st.ID] = i
	}
	cutoff := time.Now().AddDate(0, 0, -90)
	for _, d := range list {
		i, ok := idx[d.StageID]
		if !ok {
			continue
		}
		if d.Status != "open" && d.ClosedAt != nil && d.ClosedAt.Before(cutoff) {
			continue
		}
		col := &b.Columns[i]
		col.Deals = append(col.Deals, d)
		col.Count++
		col.Value += d.Value
		col.Weighted += d.Value * float64(d.Probability) / 100
		switch d.Status {
		case "open":
			b.Totals.OpenCount++
			b.Totals.OpenValue += d.Value
			b.Totals.WeightedValue += d.Value * float64(d.Probability) / 100
			if d.Stale {
				b.Totals.StaleCount++
			}
		case "won":
			if d.ClosedAt != nil && d.ClosedAt.After(time.Now().AddDate(0, 0, -30)) {
				b.Totals.WonValue30d += d.Value
				b.Totals.WonCount30d++
			}
		}
	}
	return b, nil
}

type Input struct {
	PipelineID    *uuid.UUID `json:"pipeline_id"`
	StageID       *uuid.UUID `json:"stage_id"`
	LeadID        *uuid.UUID `json:"lead_id"`
	Name          string     `json:"name"`
	Value         *float64   `json:"value"`
	Currency      string     `json:"currency"`
	ExpectedClose *string    `json:"expected_close"` // YYYY-MM-DD or "" to clear
	OwnerID       *uuid.UUID `json:"owner_id"`
	NextStep      *string    `json:"next_step"`
	NextStepAt    *time.Time `json:"next_step_at"`
	Source        string     `json:"source"`
}

func (s *Service) recordEvent(ctx context.Context, db database.DBTX, orgID, dealID uuid.UUID, a Actor, typ string, data map[string]any) error {
	if a.Workflow != "" {
		if data == nil {
			data = map[string]any{}
		}
		data["workflow"] = a.Workflow
	}
	raw, _ := json.Marshal(data)
	_, err := db.Exec(ctx, `INSERT INTO deal_events (organization_id, deal_id, event_type, actor_id, data) VALUES ($1,$2,$3,$4,$5)`, orgID, dealID, typ, a.UserID, raw)
	return err
}

func parseDate(s *string) (*time.Time, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", strings.TrimSpace(*s))
	if err != nil {
		return nil, apperr.Invalid("validation failed", map[string]string{"expected_close": "must be YYYY-MM-DD"})
	}
	return &t, nil
}

// Create adds a deal. Without a pipeline/stage it lands in the default
// pipeline's first open stage; without a name it is named after the lead.
func (s *Service) Create(ctx context.Context, orgID uuid.UUID, a Actor, in Input) (*Deal, error) {
	if err := s.EnsureDefault(ctx, orgID); err != nil {
		return nil, err
	}
	var pid, sid uuid.UUID
	if in.StageID != nil {
		if err := s.pool.QueryRow(ctx, `SELECT pipeline_id, id FROM pipeline_stages WHERE organization_id=$1 AND id=$2`, orgID, *in.StageID).Scan(&pid, &sid); err != nil {
			if database.IsNoRows(err) {
				return nil, apperr.NotFound("stage")
			}
			return nil, err
		}
	} else {
		q := `SELECT p.id, st.id FROM pipelines p JOIN pipeline_stages st ON st.pipeline_id = p.id
			WHERE p.organization_id=$1 AND ($2::uuid IS NULL AND p.is_default OR p.id = $2) AND st.kind='open' ORDER BY st.position LIMIT 1`
		if err := s.pool.QueryRow(ctx, q, orgID, in.PipelineID).Scan(&pid, &sid); err != nil {
			if database.IsNoRows(err) {
				return nil, apperr.NotFound("pipeline")
			}
			return nil, err
		}
	}
	name := strings.TrimSpace(in.Name)
	if in.LeadID != nil {
		var company string
		var owner *uuid.UUID
		if err := s.pool.QueryRow(ctx, `SELECT company_name, owner_id FROM leads WHERE organization_id=$1 AND id=$2`, orgID, *in.LeadID).Scan(&company, &owner); err != nil {
			if database.IsNoRows(err) {
				return nil, apperr.NotFound("lead")
			}
			return nil, err
		}
		if name == "" {
			name = company
		}
		if in.OwnerID == nil {
			in.OwnerID = owner
		}
	}
	if name == "" {
		return nil, apperr.Invalid("validation failed", map[string]string{"name": "is required"})
	}
	value := 0.0
	if in.Value != nil {
		if *in.Value < 0 {
			return nil, apperr.Invalid("validation failed", map[string]string{"value": "can't be negative"})
		}
		value = *in.Value
	}
	if in.Currency == "" {
		in.Currency = "INR"
	}
	if in.Source == "" {
		in.Source = "manual"
	}
	if in.OwnerID == nil && a.UserID != nil {
		in.OwnerID = a.UserID
	}
	closeAt, err := parseDate(in.ExpectedClose)
	if err != nil {
		return nil, err
	}
	nextStep := ""
	if in.NextStep != nil {
		nextStep = strings.TrimSpace(*in.NextStep)
	}
	var id uuid.UUID
	err = database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO deals (organization_id, pipeline_id, stage_id, lead_id, name, value, currency, expected_close, owner_id,
			next_step, next_step_at, source, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`,
			orgID, pid, sid, in.LeadID, name, value, strings.ToUpper(in.Currency), closeAt, in.OwnerID, nextStep, in.NextStepAt, in.Source, a.UserID).Scan(&id); err != nil {
			return err
		}
		if err := s.recordEvent(ctx, tx, orgID, id, a, "created", map[string]any{"value": value, "source": in.Source}); err != nil {
			return err
		}
		if in.LeadID != nil {
			if err := leads.RecordEvent(ctx, tx, orgID, *in.LeadID, "deal_created", a.UserID, map[string]any{"deal_id": id, "name": name}); err != nil {
				return err
			}
		}
		return webhooks.Publish(ctx, tx, orgID, webhooks.DealCreated, map[string]any{"deal_id": id, "name": name, "value": value, "lead_id": in.LeadID})
	})
	if err != nil {
		return nil, err
	}
	d, err := s.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	s.emit(ctx, orgID, EvCreated, d)
	return d, nil
}

// Update edits a deal's fields (not its stage — see Move).
func (s *Service) Update(ctx context.Context, orgID, id uuid.UUID, a Actor, in Input) (*Deal, error) {
	cur, err := s.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	changes := map[string]any{}
	name := cur.Name
	if n := strings.TrimSpace(in.Name); n != "" && n != cur.Name {
		name, changes["name"] = n, n
	}
	value := cur.Value
	if in.Value != nil && *in.Value != cur.Value {
		if *in.Value < 0 {
			return nil, apperr.Invalid("validation failed", map[string]string{"value": "can't be negative"})
		}
		value, changes["value"] = *in.Value, *in.Value
	}
	currency := cur.Currency
	if in.Currency != "" {
		currency = strings.ToUpper(in.Currency)
	}
	closeAt, err := parseDate(cur.ExpectedClose)
	if err != nil {
		return nil, err
	}
	if in.ExpectedClose != nil {
		if closeAt, err = parseDate(in.ExpectedClose); err != nil {
			return nil, err
		}
		changes["expected_close"] = *in.ExpectedClose
	}
	owner := cur.OwnerID
	if in.OwnerID != nil && (owner == nil || *owner != *in.OwnerID) {
		owner = in.OwnerID
		if *owner == uuid.Nil {
			owner = nil
		}
		changes["owner_id"] = owner
	}
	nextStep, nextAt := cur.NextStep, cur.NextStepAt
	if in.NextStep != nil {
		nextStep, nextAt = strings.TrimSpace(*in.NextStep), in.NextStepAt
		changes["next_step"] = nextStep
	}
	err = database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE deals SET name=$3, value=$4, currency=$5, expected_close=$6, owner_id=$7, next_step=$8, next_step_at=$9,
			last_activity_at=now(), updated_at=now() WHERE organization_id=$1 AND id=$2`, orgID, id, name, value, currency, closeAt, owner, nextStep, nextAt); err != nil {
			return err
		}
		if len(changes) == 0 {
			return nil
		}
		return s.recordEvent(ctx, tx, orgID, id, a, "updated", changes)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, orgID, id)
}

// Move changes a deal's stage. Entering a Won/Lost stage closes the deal
// (lost needs a reason) and updates the lead's status to match.
func (s *Service) Move(ctx context.Context, orgID, id uuid.UUID, a Actor, stageID uuid.UUID, lostReason string) (*Deal, error) {
	cur, err := s.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if cur.StageID == stageID {
		return cur, nil
	}
	var st Stage
	var pid uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT id, pipeline_id, name, kind, probability FROM pipeline_stages WHERE organization_id=$1 AND id=$2`, orgID, stageID).
		Scan(&st.ID, &pid, &st.Name, &st.Kind, &st.Probability); err != nil {
		if database.IsNoRows(err) {
			return nil, apperr.NotFound("stage")
		}
		return nil, err
	}
	if pid != cur.PipelineID {
		return nil, apperr.Invalid("validation failed", map[string]string{"stage_id": "stage belongs to another pipeline"})
	}
	lostReason = strings.TrimSpace(lostReason)
	if st.Kind == "lost" && lostReason == "" {
		return nil, apperr.Invalid("validation failed", map[string]string{"lost_reason": "say why the deal was lost"})
	}
	status := st.Kind
	if status == "open" {
		lostReason = ""
	}
	err = database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE deals SET stage_id=$3, status=$4, lost_reason=$5, stage_entered_at=now(), last_activity_at=now(), updated_at=now(),
			closed_at = CASE WHEN $4 = 'open' THEN NULL ELSE now() END WHERE organization_id=$1 AND id=$2`, orgID, id, st.ID, status, lostReason); err != nil {
			return err
		}
		if err := s.recordEvent(ctx, tx, orgID, id, a, "stage_changed", map[string]any{"from": cur.StageName, "to": st.Name, "lost_reason": lostReason}); err != nil {
			return err
		}
		if cur.LeadID != nil && status != "open" {
			if _, err := tx.Exec(ctx, `UPDATE leads SET status=$3, updated_at=now() WHERE organization_id=$1 AND id=$2`, orgID, *cur.LeadID, status); err != nil {
				return err
			}
		}
		ev := webhooks.DealStageChanged
		switch status {
		case "won":
			ev = webhooks.DealWon
		case "lost":
			ev = webhooks.DealLost
		}
		return webhooks.Publish(ctx, tx, orgID, ev, map[string]any{"deal_id": id, "name": cur.Name, "value": cur.Value, "from": cur.StageName, "to": st.Name, "lead_id": cur.LeadID})
	})
	if err != nil {
		return nil, err
	}
	d, err := s.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	s.emit(ctx, orgID, EvStageChanged, d)
	switch status {
	case "won":
		s.emit(ctx, orgID, EvWon, d)
	case "lost":
		s.emit(ctx, orgID, EvLost, d)
	}
	return d, nil
}

func (s *Service) Delete(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM deals WHERE organization_id=$1 AND id=$2`, p.OrgID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return apperr.NotFound("deal")
	}
	if err == nil {
		s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "deal.deleted", EntityType: "deal", EntityID: id.String()})
	}
	return err
}

// AddNote logs a note on the deal's timeline.
func (s *Service) AddNote(ctx context.Context, orgID, id uuid.UUID, a Actor, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return apperr.Invalid("validation failed", map[string]string{"body": "is required"})
	}
	if _, err := s.Get(ctx, orgID, id); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `UPDATE deals SET last_activity_at=now() WHERE organization_id=$1 AND id=$2`, orgID, id); err != nil {
		return err
	}
	return s.recordEvent(ctx, s.pool, orgID, id, a, "note", map[string]any{"body": body})
}

func (s *Service) Events(ctx context.Context, orgID, id uuid.UUID) ([]Event, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.id, e.event_type, u.full_name, e.data, e.created_at FROM deal_events e LEFT JOIN users u ON u.id=e.actor_id
		WHERE e.organization_id=$1 AND e.deal_id=$2 ORDER BY e.created_at DESC, e.id DESC LIMIT 200`, orgID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.EventType, &e.ActorName, &e.Data, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// OpenDealForLead returns the lead's most recent open deal, if any.
func (s *Service) OpenDealForLead(ctx context.Context, orgID, leadID uuid.UUID) (*uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM deals WHERE organization_id=$1 AND lead_id=$2 AND status='open' ORDER BY created_at DESC LIMIT 1`, orgID, leadID).Scan(&id)
	if database.IsNoRows(err) {
		return nil, nil
	}
	return &id, err
}
