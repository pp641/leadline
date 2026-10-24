package crm

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/auth"
	"leadline/internal/integrations"
	"leadline/internal/jobs"
	"leadline/internal/leads"
	"leadline/pkg/apperr"
	"leadline/pkg/httpx"
	"leadline/pkg/queue"
)

// Syncer pushes leads to an external CRM (HubSpot) as company + contact.
type Syncer struct {
	pool *pgxpool.Pool
	reg  *integrations.Registry
	q    jobs.Enqueuer
	log  *slog.Logger
}

func NewSyncer(pool *pgxpool.Pool, reg *integrations.Registry, q jobs.Enqueuer, log *slog.Logger) *Syncer {
	return &Syncer{pool: pool, reg: reg, q: q, log: log}
}

type syncLead struct {
	id                                                       uuid.UUID
	name, domain, website, city, state, country, phone, desc string
	employees, founded                                       *int
	email, first, last, title                                *string
	companyExt, contactExt                                   *string
}

// SyncLeads pushes leads to the given CRM, or to every connected CRM.
func (s *Syncer) SyncLeads(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID, provider string) error {
	kinds := []string{provider}
	if provider == "" {
		var err error
		if kinds, err = s.reg.ConnectedWith(ctx, orgID, integrations.CapCRM); err != nil {
			return err
		}
	}
	if len(kinds) == 0 {
		return queue.Permanent(apperr.Conflict("integration_not_configured", "no CRM is connected"))
	}
	var firstErr error
	for _, kind := range kinds {
		client, err := s.reg.CRM(ctx, orgID, kind)
		if err == nil {
			err = s.syncTo(ctx, orgID, ids, kind, client)
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if ae, ok := apperr.As(firstErr); ok && ae.Kind == apperr.KindConflict {
		return queue.Permanent(firstErr) // not connected any more: retrying won't help
	}
	return firstErr
}

func (s *Syncer) syncTo(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID, kind string, client integrations.CRM) error {
	rows, err := s.pool.Query(ctx, `
		SELECT l.id, l.company_name, l.domain, l.website, l.city, l.state, l.country, l.phone, l.description,
		       l.employee_count, l.founded_year, pc.email::text, pc.first_name, pc.last_name, pc.job_title,
		       k.external_company_id, k.external_contact_id
		FROM leads l
		LEFT JOIN LATERAL (SELECT c.email, c.first_name, c.last_name, c.job_title FROM lead_contacts c
			WHERE c.lead_id=l.id AND c.email IS NOT NULL ORDER BY c.is_primary DESC, c.created_at LIMIT 1) pc ON true
		LEFT JOIN crm_links k ON k.lead_id=l.id AND k.provider=$3
		WHERE l.organization_id=$1 AND l.id = ANY($2)`, orgID, ids, kind)
	if err != nil {
		return err
	}
	var batch []syncLead
	for rows.Next() {
		var l syncLead
		if err := rows.Scan(&l.id, &l.name, &l.domain, &l.website, &l.city, &l.state, &l.country, &l.phone, &l.desc,
			&l.employees, &l.founded, &l.email, &l.first, &l.last, &l.title, &l.companyExt, &l.contactExt); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, l)
	}
	rows.Close()

	for _, l := range batch {
		companyID, contactID, err := s.push(ctx, client, l)
		var errMsg *string
		if err != nil {
			m := err.Error()
			errMsg = &m
			s.log.Warn("crm sync failed", "lead_id", l.id, "provider", kind, "error", err)
		}
		if _, dbErr := s.pool.Exec(ctx, `
			INSERT INTO crm_links (organization_id, lead_id, provider, external_company_id, external_contact_id, synced_at, last_error)
			VALUES ($1,$2,$6,NULLIF($3,''),NULLIF($4,''),now(),$5)
			ON CONFLICT (lead_id, provider) DO UPDATE SET
				external_company_id=COALESCE(EXCLUDED.external_company_id, crm_links.external_company_id),
				external_contact_id=COALESCE(EXCLUDED.external_contact_id, crm_links.external_contact_id),
				synced_at=now(), last_error=EXCLUDED.last_error`, orgID, l.id, companyID, contactID, errMsg, kind); dbErr != nil {
			return dbErr
		}
		if err == nil {
			_ = leads.RecordEvent(ctx, s.pool, orgID, l.id, "crm_synced", nil, map[string]any{"provider": kind, "company_id": companyID})
		}
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (s *Syncer) push(ctx context.Context, client integrations.CRM, l syncLead) (string, string, error) {
	companyID, err := client.UpsertCompany(ctx, deref(l.companyExt), integrations.CRMCompany{
		Name: l.name, Domain: l.domain, Website: l.website, Phone: l.phone, City: l.city, State: l.state, Country: l.country,
		Description: l.desc, Employees: l.employees, Founded: l.founded,
	})
	if err != nil {
		return "", "", err
	}
	if l.email == nil {
		return companyID, "", nil
	}
	contactID, err := client.UpsertPerson(ctx, deref(l.contactExt), companyID, integrations.CRMPerson{
		Email: *l.email, FirstName: deref(l.first), LastName: deref(l.last), Title: deref(l.title), Company: l.name,
	})
	return companyID, contactID, err
}

func (s *Syncer) HandleSync(ctx context.Context, job queue.Job) error {
	var p jobs.CRMSync
	if err := job.Decode(&p); err != nil {
		return queue.Permanent(err)
	}
	return s.SyncLeads(ctx, p.OrgID, p.LeadIDs, p.Provider)
}

// AutoSync periodically queues engaged leads (interested and beyond) that
// changed since their last sync, for organizations with auto_sync enabled.
func (s *Syncer) AutoSync(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		rows, err := s.pool.Query(ctx, `SELECT organization_id, kind FROM integrations
			WHERE status <> 'disabled' AND (config->>'auto_sync')::boolean IS TRUE`)
		if err != nil {
			continue
		}
		type target struct {
			org  uuid.UUID
			kind string
		}
		var targets []target
		for rows.Next() {
			var t target
			if rows.Scan(&t.org, &t.kind) == nil && integrations.HasCapability(t.kind, integrations.CapCRM) {
				targets = append(targets, t)
			}
		}
		rows.Close()
		for _, t := range targets {
			ids, err := s.pendingLeads(ctx, t.org, t.kind)
			if err != nil || len(ids) == 0 {
				continue
			}
			for _, c := range jobs.Chunk(ids, 25) {
				_ = s.q.Enqueue(ctx, jobs.QueueDefault, jobs.TypeCRMSync, jobs.CRMSync{OrgID: t.org, LeadIDs: c, Provider: t.kind})
			}
		}
	}
}

func (s *Syncer) pendingLeads(ctx context.Context, orgID uuid.UUID, kind string) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT l.id FROM leads l LEFT JOIN crm_links k ON k.lead_id=l.id AND k.provider=$2
		WHERE l.organization_id=$1 AND l.status IN ('interested','qualified','meeting','won')
		  AND (k.lead_id IS NULL OR (k.synced_at < l.updated_at AND k.synced_at < now() - interval '10 minutes'))
		LIMIT 200`, orgID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type SyncHandler struct {
	s  *Syncer
	mw *auth.Middleware
}

func NewSyncHandler(s *Syncer, mw *auth.Middleware) *SyncHandler { return &SyncHandler{s: s, mw: mw} }

func (h *SyncHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/crm/sync", h.mw.Org(auth.RoleMember, h.sync))
}

func (h *SyncHandler) sync(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LeadIDs []uuid.UUID `json:"lead_ids"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	p := auth.MustPrincipal(r.Context())
	if kinds, err := h.s.reg.ConnectedWith(r.Context(), p.OrgID, integrations.CapCRM); err != nil || len(kinds) == 0 {
		httpx.Error(w, r, apperr.Conflict("integration_not_configured", "connect a CRM under Integrations first"))
		return
	}
	if len(in.LeadIDs) == 0 || len(in.LeadIDs) > 1000 {
		httpx.Error(w, r, apperr.Invalid("provide 1–1000 lead_ids", nil))
		return
	}
	for _, c := range jobs.Chunk(in.LeadIDs, 25) {
		if err := h.s.q.Enqueue(r.Context(), jobs.QueueDefault, jobs.TypeCRMSync, jobs.CRMSync{OrgID: p.OrgID, LeadIDs: c}); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}
	httpx.JSON(w, http.StatusAccepted, map[string]int{"queued": len(in.LeadIDs)}, nil)
}
