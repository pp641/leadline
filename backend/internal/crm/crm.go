// Package crm is the lightweight internal CRM: lead owners, notes, tasks and
// a pipeline view over lead statuses. The activity timeline is lead_events.
package crm

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/auth"
	"leadline/internal/leads"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/httpx"
)

// PipelineStatuses are the board columns, in order.
var PipelineStatuses = []string{"replied", "interested", "qualified", "meeting", "won", "lost"}

type Note struct {
	ID         uuid.UUID  `json:"id"`
	LeadID     uuid.UUID  `json:"lead_id"`
	AuthorID   *uuid.UUID `json:"author_id"`
	AuthorName *string    `json:"author_name"`
	Body       string     `json:"body"`
	CreatedAt  time.Time  `json:"created_at"`
}

type Task struct {
	ID           uuid.UUID  `json:"id"`
	LeadID       *uuid.UUID `json:"lead_id"`
	CompanyName  *string    `json:"company_name"`
	AssigneeID   *uuid.UUID `json:"assignee_id"`
	AssigneeName *string    `json:"assignee_name"`
	Title        string     `json:"title"`
	Notes        string     `json:"notes"`
	DueAt        *time.Time `json:"due_at"`
	CompletedAt  *time.Time `json:"completed_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

type Card struct {
	ID          uuid.UUID  `json:"id"`
	CompanyName string     `json:"company_name"`
	City        string     `json:"city"`
	LeadScore   int        `json:"lead_score"`
	OwnerName   *string    `json:"owner_name"`
	OpenTasks   int        `json:"open_tasks"`
	UpdatedAt   time.Time  `json:"updated_at"`
	OwnerID     *uuid.UUID `json:"owner_id"`
}

type Column struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
	Leads  []Card `json:"leads"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func event(ctx context.Context, db database.DBTX, p auth.Principal, leadID uuid.UUID, t string, data map[string]any) error {
	return leads.RecordEvent(ctx, db, p.OrgID, leadID, t, &p.UserID, data)
}

func (s *Service) assertLead(ctx context.Context, db database.DBTX, orgID, leadID uuid.UUID) error {
	var ok bool
	if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM leads WHERE organization_id=$1 AND id=$2)`, orgID, leadID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return apperr.NotFound("lead")
	}
	return nil
}

func (s *Service) assertMember(ctx context.Context, orgID, userID uuid.UUID) error {
	var ok bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM organization_members WHERE organization_id=$1 AND user_id=$2)
		    OR EXISTS(SELECT 1 FROM organizations o JOIN organization_members m ON m.organization_id=o.parent_id WHERE o.id=$1 AND m.user_id=$2)`,
		orgID, userID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return apperr.Invalid("validation failed", map[string]string{"user_id": "not a member of this organization"})
	}
	return nil
}

// AssignOwner sets (or clears, with nil) the owner of leads.
func (s *Service) AssignOwner(ctx context.Context, p auth.Principal, leadIDs []uuid.UUID, ownerID *uuid.UUID) (int, error) {
	if ownerID != nil {
		if err := s.assertMember(ctx, p.OrgID, *ownerID); err != nil {
			return 0, err
		}
	}
	var n int64
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE leads SET owner_id=$3, updated_at=now() WHERE organization_id=$1 AND id = ANY($2)`, p.OrgID, leadIDs, ownerID)
		if err != nil {
			return err
		}
		n = tag.RowsAffected()
		_, err = tx.Exec(ctx, `INSERT INTO lead_events (organization_id, lead_id, event_type, actor_user_id, data)
			SELECT $1::uuid, l.id, 'owner_changed', $3::uuid, jsonb_build_object('owner_id', $4::uuid)
			FROM leads l WHERE l.organization_id=$1 AND l.id = ANY($2)`, p.OrgID, leadIDs, p.UserID, ownerID)
		return err
	})
	return int(n), err
}

func (s *Service) Notes(ctx context.Context, p auth.Principal, leadID uuid.UUID) ([]Note, error) {
	rows, err := s.pool.Query(ctx, `SELECT n.id, n.lead_id, n.author_id, u.full_name, n.body, n.created_at
		FROM lead_notes n LEFT JOIN users u ON u.id=n.author_id
		WHERE n.organization_id=$1 AND n.lead_id=$2 ORDER BY n.created_at DESC`, p.OrgID, leadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Note{}
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.LeadID, &n.AuthorID, &n.AuthorName, &n.Body, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Service) AddNote(ctx context.Context, p auth.Principal, leadID uuid.UUID, body string) (*Note, error) {
	body = strings.TrimSpace(body)
	if body == "" || len(body) > 10000 {
		return nil, apperr.Invalid("validation failed", map[string]string{"body": "is required (max 10,000 characters)"})
	}
	n := Note{LeadID: leadID, AuthorID: &p.UserID, Body: body}
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.assertLead(ctx, tx, p.OrgID, leadID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO lead_notes (organization_id, lead_id, author_id, body) VALUES ($1,$2,$3,$4) RETURNING id, created_at`,
			p.OrgID, leadID, p.UserID, body).Scan(&n.ID, &n.CreatedAt); err != nil {
			return err
		}
		return event(ctx, tx, p, leadID, "note_added", map[string]any{"note_id": n.ID})
	})
	return &n, err
}

func (s *Service) DeleteNote(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	// Authors can delete their own notes; admins can delete any.
	tag, err := s.pool.Exec(ctx, `DELETE FROM lead_notes WHERE organization_id=$1 AND id=$2 AND (author_id=$3 OR $4)`,
		p.OrgID, id, p.UserID, p.Role.AtLeast(auth.RoleAdmin))
	if err == nil && tag.RowsAffected() == 0 {
		return apperr.NotFound("note")
	}
	return err
}

type TaskFilter struct {
	AssigneeID *uuid.UUID
	LeadID     *uuid.UUID
	Open       *bool
}

func (s *Service) Tasks(ctx context.Context, p auth.Principal, f TaskFilter) ([]Task, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.lead_id, l.company_name, t.assignee_id, u.full_name, t.title, t.notes, t.due_at, t.completed_at, t.created_at
		FROM tasks t LEFT JOIN leads l ON l.id=t.lead_id LEFT JOIN users u ON u.id=t.assignee_id
		WHERE t.organization_id=$1
		  AND ($2::uuid IS NULL OR t.assignee_id=$2)
		  AND ($3::uuid IS NULL OR t.lead_id=$3)
		  AND ($4::boolean IS NULL OR (t.completed_at IS NULL) = $4)
		ORDER BY t.completed_at IS NOT NULL, t.due_at NULLS LAST, t.created_at DESC
		LIMIT 500`, p.OrgID, f.AssigneeID, f.LeadID, f.Open)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.LeadID, &t.CompanyName, &t.AssigneeID, &t.AssigneeName, &t.Title, &t.Notes, &t.DueAt, &t.CompletedAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type TaskInput struct {
	LeadID     *uuid.UUID `json:"lead_id"`
	AssigneeID *uuid.UUID `json:"assignee_id"`
	Title      string     `json:"title"`
	Notes      string     `json:"notes"`
	DueAt      *time.Time `json:"due_at"`
	Completed  *bool      `json:"completed"`
}

func (s *Service) validateTask(ctx context.Context, p auth.Principal, in *TaskInput) error {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || len(in.Title) > 300 {
		return apperr.Invalid("validation failed", map[string]string{"title": "is required (max 300 characters)"})
	}
	if in.AssigneeID != nil {
		if err := s.assertMember(ctx, p.OrgID, *in.AssigneeID); err != nil {
			return err
		}
	}
	if in.LeadID != nil {
		return s.assertLead(ctx, s.pool, p.OrgID, *in.LeadID)
	}
	return nil
}

func (s *Service) CreateTask(ctx context.Context, p auth.Principal, in TaskInput) (uuid.UUID, error) {
	if in.AssigneeID == nil {
		in.AssigneeID = &p.UserID
	}
	if err := s.validateTask(ctx, p, &in); err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO tasks (organization_id, lead_id, assignee_id, created_by, title, notes, due_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, p.OrgID, in.LeadID, in.AssigneeID, p.UserID, in.Title, in.Notes, in.DueAt).Scan(&id); err != nil {
			return err
		}
		if in.LeadID != nil {
			return event(ctx, tx, p, *in.LeadID, "task_created", map[string]any{"task_id": id, "title": in.Title})
		}
		return nil
	})
	return id, err
}

func (s *Service) UpdateTask(ctx context.Context, p auth.Principal, id uuid.UUID, in TaskInput) error {
	if err := s.validateTask(ctx, p, &in); err != nil {
		return err
	}
	var leadID *uuid.UUID
	var wasDone bool
	err := s.pool.QueryRow(ctx, `SELECT lead_id, completed_at IS NOT NULL FROM tasks WHERE organization_id=$1 AND id=$2`, p.OrgID, id).Scan(&leadID, &wasDone)
	if database.IsNoRows(err) {
		return apperr.NotFound("task")
	}
	if err != nil {
		return err
	}
	done := wasDone
	if in.Completed != nil {
		done = *in.Completed
	}
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE tasks SET title=$3, notes=$4, due_at=$5, assignee_id=$6, lead_id=$7,
			completed_at = CASE WHEN $8 THEN COALESCE(completed_at, now()) ELSE NULL END, updated_at=now()
			WHERE organization_id=$1 AND id=$2`, p.OrgID, id, in.Title, in.Notes, in.DueAt, in.AssigneeID, in.LeadID, done); err != nil {
			return err
		}
		if done && !wasDone && leadID != nil {
			return event(ctx, tx, p, *leadID, "task_completed", map[string]any{"task_id": id, "title": in.Title})
		}
		return nil
	})
}

func (s *Service) DeleteTask(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM tasks WHERE organization_id=$1 AND id=$2`, p.OrgID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return apperr.NotFound("task")
	}
	return err
}

// Pipeline returns board columns with counts and the 50 most recently updated leads each.
func (s *Service) Pipeline(ctx context.Context, p auth.Principal, ownerID *uuid.UUID) ([]Column, error) {
	cols := make([]Column, 0, len(PipelineStatuses))
	for _, st := range PipelineStatuses {
		col := Column{Status: st, Leads: []Card{}}
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM leads WHERE organization_id=$1 AND status=$2 AND ($3::uuid IS NULL OR owner_id=$3)`,
			p.OrgID, st, ownerID).Scan(&col.Count); err != nil {
			return nil, err
		}
		rows, err := s.pool.Query(ctx, `
			SELECT l.id, l.company_name, l.city, l.lead_score, u.full_name, l.owner_id,
			       (SELECT count(*) FROM tasks t WHERE t.lead_id=l.id AND t.completed_at IS NULL), l.updated_at
			FROM leads l LEFT JOIN users u ON u.id=l.owner_id
			WHERE l.organization_id=$1 AND l.status=$2 AND ($3::uuid IS NULL OR l.owner_id=$3)
			ORDER BY l.updated_at DESC LIMIT 50`, p.OrgID, st, ownerID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var c Card
			if err := rows.Scan(&c.ID, &c.CompanyName, &c.City, &c.LeadScore, &c.OwnerName, &c.OwnerID, &c.OpenTasks, &c.UpdatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			col.Leads = append(col.Leads, c)
		}
		rows.Close()
		cols = append(cols, col)
	}
	return cols, nil
}

// ───────────────────────── HTTP ─────────────────────────

type Handler struct {
	svc *Service
	mw  *auth.Middleware
}

func NewHandler(svc *Service, mw *auth.Middleware) *Handler { return &Handler{svc: svc, mw: mw} }

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/crm/pipeline", h.mw.Org(auth.RoleViewer, h.pipeline))
	mux.HandleFunc("POST /api/v1/crm/assign", h.mw.Org(auth.RoleMember, h.assign))
	mux.HandleFunc("GET /api/v1/leads/{id}/notes", h.mw.Org(auth.RoleViewer, h.notes))
	mux.HandleFunc("POST /api/v1/leads/{id}/notes", h.mw.Org(auth.RoleMember, h.addNote))
	mux.HandleFunc("DELETE /api/v1/notes/{id}", h.mw.Org(auth.RoleMember, h.deleteNote))
	mux.HandleFunc("GET /api/v1/tasks", h.mw.Org(auth.RoleViewer, h.tasks))
	mux.HandleFunc("POST /api/v1/tasks", h.mw.Org(auth.RoleMember, h.createTask))
	mux.HandleFunc("PUT /api/v1/tasks/{id}", h.mw.Org(auth.RoleMember, h.updateTask))
	mux.HandleFunc("DELETE /api/v1/tasks/{id}", h.mw.Org(auth.RoleMember, h.deleteTask))
}

func id(r *http.Request, what string) (uuid.UUID, error) {
	v, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, apperr.NotFound(what)
	}
	return v, nil
}

// userParam resolves "me" or a UUID query parameter.
func userParam(r *http.Request, key string, p auth.Principal) *uuid.UUID {
	switch v := r.URL.Query().Get(key); v {
	case "":
		return nil
	case "me":
		return &p.UserID
	default:
		if u, err := uuid.Parse(v); err == nil {
			return &u
		}
	}
	return nil
}

func (h *Handler) pipeline(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	out, err := h.svc.Pipeline(r.Context(), p, userParam(r, "owner", p))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) assign(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LeadIDs []uuid.UUID `json:"lead_ids"`
		OwnerID *uuid.UUID  `json:"owner_id"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	n, err := h.svc.AssignOwner(r.Context(), auth.MustPrincipal(r.Context()), in.LeadIDs, in.OwnerID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, map[string]int{"affected": n})
}

func (h *Handler) notes(w http.ResponseWriter, r *http.Request) {
	leadID, err := id(r, "lead")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.Notes(r.Context(), auth.MustPrincipal(r.Context()), leadID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) addNote(w http.ResponseWriter, r *http.Request) {
	leadID, err := id(r, "lead")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	n, err := h.svc.AddNote(r.Context(), auth.MustPrincipal(r.Context()), leadID, in.Body)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.Created(w, n)
}

func (h *Handler) deleteNote(w http.ResponseWriter, r *http.Request) {
	nid, err := id(r, "note")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.svc.DeleteNote(r.Context(), auth.MustPrincipal(r.Context()), nid); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) tasks(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	f := TaskFilter{AssigneeID: userParam(r, "assignee", p)}
	if v, err := uuid.Parse(r.URL.Query().Get("lead_id")); err == nil {
		f.LeadID = &v
	}
	switch r.URL.Query().Get("status") {
	case "open":
		t := true
		f.Open = &t
	case "done":
		t := false
		f.Open = &t
	}
	out, err := h.svc.Tasks(r.Context(), p, f)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) createTask(w http.ResponseWriter, r *http.Request) {
	var in TaskInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	tid, err := h.svc.CreateTask(r.Context(), auth.MustPrincipal(r.Context()), in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.Created(w, map[string]uuid.UUID{"id": tid})
}

func (h *Handler) updateTask(w http.ResponseWriter, r *http.Request) {
	tid, err := id(r, "task")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in TaskInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.svc.UpdateTask(r.Context(), auth.MustPrincipal(r.Context()), tid, in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) deleteTask(w http.ResponseWriter, r *http.Request) {
	tid, err := id(r, "task")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.svc.DeleteTask(r.Context(), auth.MustPrincipal(r.Context()), tid); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}
