package verification

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/auth"
	"leadline/internal/jobs"
	"leadline/pkg/apperr"
	"leadline/pkg/httpx"
	"leadline/pkg/queue"
)

type Service struct {
	pool     *pgxpool.Pool
	verifier EmailVerifier
	q        jobs.Enqueuer
}

func NewService(pool *pgxpool.Pool, v EmailVerifier, q jobs.Enqueuer) *Service {
	return &Service{pool: pool, verifier: v, q: q}
}

// VerifyContacts verifies each contact and stores the result, then rescoring
// is enqueued for the affected leads (email status feeds the score).
func (s *Service) VerifyContacts(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) error {
	rows, err := s.pool.Query(ctx, `SELECT id, lead_id, email::text FROM lead_contacts WHERE organization_id=$1 AND id = ANY($2) AND email IS NOT NULL`, orgID, ids)
	if err != nil {
		return err
	}
	type target struct {
		id, lead uuid.UUID
		email    string
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.lead, &t.email); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	leadSet := map[uuid.UUID]struct{}{}
	for _, t := range targets {
		res, err := s.verifier.Verify(ctx, t.email)
		if err != nil {
			return err // transient: the job is retried; finished contacts are simply re-verified
		}
		details, _ := json.Marshal(res)
		if _, err := s.pool.Exec(ctx, `
			UPDATE lead_contacts SET email_status=$3, verification_details=$4, email_verified_at=now(), updated_at=now()
			WHERE organization_id=$1 AND id=$2`, orgID, t.id, string(res.Status), details); err != nil {
			return err
		}
		leadSet[t.lead] = struct{}{}
	}
	leadIDs := make([]uuid.UUID, 0, len(leadSet))
	for id := range leadSet {
		leadIDs = append(leadIDs, id)
	}
	return jobs.EnqueueScore(ctx, s.q, orgID, leadIDs)
}

// RequestVerification enqueues verification for contacts (all unverified
// contacts in the org when ids is empty).
func (s *Service) RequestVerification(ctx context.Context, p auth.Principal, ids []uuid.UUID, all bool) (int, error) {
	q := `SELECT id FROM lead_contacts WHERE organization_id=$1 AND email IS NOT NULL AND id = ANY($2)`
	args := []any{p.OrgID, ids}
	if all {
		q = `SELECT id FROM lead_contacts WHERE organization_id=$1 AND email IS NOT NULL AND email_status='unverified'`
		args = args[:1]
	} else if len(ids) == 0 {
		return 0, apperr.Invalid("validation failed", map[string]string{"contact_ids": "provide contact_ids or set all=true"})
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	var found []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		found = append(found, id)
	}
	rows.Close()
	return len(found), jobs.EnqueueVerify(ctx, s.q, p.OrgID, found)
}

func (s *Service) HandleVerifyContacts(ctx context.Context, job queue.Job) error {
	var p jobs.VerifyContacts
	if err := job.Decode(&p); err != nil {
		return queue.Permanent(err)
	}
	return s.VerifyContacts(ctx, p.OrgID, p.ContactIDs)
}

// ───────────────────────── HTTP ─────────────────────────

type Handler struct {
	svc *Service
	mw  *auth.Middleware
}

func NewHandler(svc *Service, mw *auth.Middleware) *Handler { return &Handler{svc: svc, mw: mw} }

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/contacts/verify", h.mw.Org(auth.RoleMember, h.verify))
}

func (h *Handler) verify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ContactIDs []uuid.UUID `json:"contact_ids"`
		All        bool        `json:"all_unverified"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	n, err := h.svc.RequestVerification(r.Context(), auth.MustPrincipal(r.Context()), in.ContactIDs, in.All)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]int{"queued": n}, nil)
}
