package leads

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/internal/contacts"
	"leadline/internal/jobs"
	"leadline/internal/scoring"
	"leadline/internal/webhooks"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/validate"
)

const maxBulk = 50000

type Service struct {
	pool     *pgxpool.Pool
	repo     *Repository
	contacts *contacts.Service
	scoring  *scoring.Service
	q        jobs.Enqueuer
	audit    *audit.Log
}

func NewService(pool *pgxpool.Pool, c *contacts.Service, s *scoring.Service, q jobs.Enqueuer, a *audit.Log) *Service {
	return &Service{pool: pool, repo: NewRepository(pool), contacts: c, scoring: s, q: q, audit: a}
}

// Prepare normalizes and validates a lead in place and returns its dedupe domain.
func Prepare(in *Input) (string, error) {
	v := validate.Errors{}
	in.CompanyName = Clean(in.CompanyName)
	in.Industry = Clean(in.Industry)
	in.Country = Clean(in.Country)
	in.State = Clean(in.State)
	in.City = Clean(in.City)
	in.Address = Clean(in.Address)
	in.Phone = Clean(in.Phone)
	in.Description = strings.TrimSpace(in.Description)
	in.LinkedInURL = strings.TrimSpace(in.LinkedInURL)
	in.SourceURL = strings.TrimSpace(in.SourceURL)
	in.BusinessEmail = validate.NormalizeEmail(in.BusinessEmail)
	in.Tags = NormalizeTags(in.Tags)
	if in.Source == "" {
		in.Source = "manual"
	}
	if in.Status == "" {
		in.Status = "new"
	}

	var domain string
	if strings.TrimSpace(in.Website) != "" {
		in.Website, domain = NormalizeWebsite(in.Website)
		v.Check(in.Website != "", "website", "must be a valid URL or domain")
	}
	v.Required("company_name", in.CompanyName)
	v.MaxLen("company_name", in.CompanyName, 255)
	v.MaxLen("description", in.Description, 5000)
	if in.BusinessEmail != "" {
		v.Check(validate.Email(in.BusinessEmail), "business_email", "must be a valid email address")
	}
	if in.EmployeeCount != nil {
		v.Check(*in.EmployeeCount >= 0 && *in.EmployeeCount <= 10_000_000, "employee_count", "must be a positive number")
	}
	if in.FoundedYear != nil {
		v.Check(*in.FoundedYear >= 1800 && *in.FoundedYear <= time.Now().Year()+1, "founded_year", "must be a valid year")
	}
	v.Check(slices.Contains(Statuses, in.Status), "status", "invalid status")
	v.Check(len(in.SocialURLs) <= 20, "social_urls", "at most 20 URLs")
	v.Check(len(in.Tags) <= 50, "tags", "at most 50 tags")
	return domain, v.Err()
}

func (s *Service) List(ctx context.Context, p auth.Principal, f Filter, limit, offset int) ([]Lead, int64, error) {
	return s.repo.List(ctx, p.OrgID, f, limit, offset)
}

func (s *Service) Get(ctx context.Context, p auth.Principal, id uuid.UUID) (*Lead, error) {
	return s.repo.Get(ctx, p.OrgID, id)
}

func (s *Service) Create(ctx context.Context, p auth.Principal, in Input) (*Lead, error) {
	domain, err := Prepare(&in)
	if err != nil {
		return nil, err
	}
	if in.Contact != nil {
		ci := contacts.Input{FirstName: in.Contact.FirstName, LastName: in.Contact.LastName, JobTitle: in.Contact.JobTitle,
			Email: in.Contact.Email, LinkedInURL: in.Contact.LinkedInURL, Source: in.Source}
		if err := ci.Prepare(); err != nil {
			return nil, err
		}
	}
	var id uuid.UUID
	var contactID uuid.UUID
	err = database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		if id, err = Insert(ctx, tx, p.OrgID, &in, domain, nil); err != nil {
			return err
		}
		if err := AddTags(ctx, tx, p.OrgID, []uuid.UUID{id}, in.Tags); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO lead_sources (organization_id, lead_id, source, source_url) VALUES ($1,$2,$3,$4)`,
			p.OrgID, id, in.Source, in.SourceURL); err != nil {
			return err
		}
		if in.Contact != nil {
			ci := &contacts.Input{FirstName: in.Contact.FirstName, LastName: in.Contact.LastName, JobTitle: in.Contact.JobTitle,
				Email: in.Contact.Email, LinkedInURL: in.Contact.LinkedInURL, Source: in.Source, IsPrimary: true}
			_ = ci.Prepare()
			if contactID, err = contacts.Insert(ctx, tx, p.OrgID, id, ci); err != nil {
				return err
			}
		}
		if err := webhooks.Publish(ctx, tx, p.OrgID, webhooks.LeadCreated, map[string]any{"lead_id": id, "company_name": in.CompanyName, "source": in.Source}); err != nil {
			return err
		}
		return RecordEvent(ctx, tx, p.OrgID, id, "created", &p.UserID, map[string]any{"source": in.Source})
	})
	if err != nil {
		return nil, err
	}
	if contactID != uuid.Nil {
		if err := jobs.EnqueueVerify(ctx, s.q, p.OrgID, []uuid.UUID{contactID}); err != nil {
			return nil, err
		}
	}
	if err := s.scoring.ScoreLeads(ctx, p.OrgID, []uuid.UUID{id}); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, p.OrgID, id)
}

func (s *Service) Update(ctx context.Context, p auth.Principal, id uuid.UUID, in Input) (*Lead, error) {
	tagsProvided := in.Tags != nil // omitted tags leave existing tags untouched
	domain, err := Prepare(&in)
	if err != nil {
		return nil, err
	}
	cur, err := s.repo.Get(ctx, p.OrgID, id)
	if err != nil {
		return nil, err
	}
	err = database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.Update(ctx, tx, p.OrgID, id, &in, domain); err != nil {
			return err
		}
		if tagsProvided {
			if err := SetTags(ctx, tx, p.OrgID, id, in.Tags); err != nil {
				return err
			}
		}
		if cur.Status != in.Status {
			if err := webhooks.Publish(ctx, tx, p.OrgID, webhooks.LeadStatusChanged, map[string]any{"lead_id": id, "from": cur.Status, "to": in.Status}); err != nil {
				return err
			}
			return RecordEvent(ctx, tx, p.OrgID, id, "status_changed", &p.UserID, map[string]any{"from": cur.Status, "to": in.Status})
		}
		return RecordEvent(ctx, tx, p.OrgID, id, "updated", &p.UserID, nil)
	})
	if err != nil {
		return nil, err
	}
	if err := s.scoring.ScoreLeads(ctx, p.OrgID, []uuid.UUID{id}); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, p.OrgID, id)
}

func (s *Service) Delete(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM leads WHERE organization_id=$1 AND id=$2`, p.OrgID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("lead")
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "lead.deleted", EntityType: "lead", EntityID: id.String()})
	return nil
}

type BulkInput struct {
	Action string      `json:"action"` // delete | add_tags | remove_tags | set_status | rescore
	IDs    []uuid.UUID `json:"ids"`
	Filter *Filter     `json:"filter"` // used when ids is empty ("select all matching")
	Tags   []string    `json:"tags"`
	Status string      `json:"status"`
}

// Bulk applies one action to selected leads and returns how many were affected.
func (s *Service) Bulk(ctx context.Context, p auth.Principal, in BulkInput) (int, error) {
	ids, err := s.resolve(ctx, p, in.IDs, in.Filter)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	tags := NormalizeTags(in.Tags)
	switch in.Action {
	case "delete":
		if !p.Role.AtLeast(auth.RoleAdmin) {
			return 0, apperr.Forbidden("bulk delete requires admin role")
		}
		tag, err := s.pool.Exec(ctx, `DELETE FROM leads WHERE organization_id=$1 AND id = ANY($2)`, p.OrgID, ids)
		if err != nil {
			return 0, err
		}
		s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "lead.bulk_deleted", EntityType: "lead", Metadata: map[string]any{"count": tag.RowsAffected()}})
		return int(tag.RowsAffected()), nil
	case "add_tags", "remove_tags":
		if len(tags) == 0 {
			return 0, apperr.Invalid("validation failed", map[string]string{"tags": "at least one tag is required"})
		}
		if in.Action == "add_tags" {
			err = AddTags(ctx, s.pool, p.OrgID, ids, tags)
		} else {
			err = RemoveTags(ctx, s.pool, p.OrgID, ids, tags)
		}
		return len(ids), err
	case "set_status":
		if !slices.Contains(Statuses, in.Status) {
			return 0, apperr.Invalid("validation failed", map[string]string{"status": "invalid status"})
		}
		var changed []uuid.UUID
		rows, err := s.pool.Query(ctx, `UPDATE leads SET status=$3, updated_at=now() WHERE organization_id=$1 AND id = ANY($2) AND status <> $3 RETURNING id`, p.OrgID, ids, in.Status)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return 0, err
			}
			changed = append(changed, id)
		}
		rows.Close()
		for _, id := range changed {
			if err := webhooks.Publish(ctx, s.pool, p.OrgID, webhooks.LeadStatusChanged, map[string]any{"lead_id": id, "to": in.Status}); err != nil {
				return 0, err
			}
		}
		_, err = s.pool.Exec(ctx, `
			INSERT INTO lead_events (organization_id, lead_id, event_type, actor_user_id, data)
			SELECT $1::uuid, id, 'status_changed', $3::uuid, jsonb_build_object('to', $4::text, 'bulk', true) FROM unnest($2::uuid[]) id`,
			p.OrgID, changed, p.UserID, in.Status)
		return len(changed), err
	case "rescore":
		return len(ids), jobs.EnqueueScore(ctx, s.q, p.OrgID, ids)
	}
	return 0, apperr.Invalid("validation failed", map[string]string{"action": "must be delete, add_tags, remove_tags, set_status or rescore"})
}

// resolve turns explicit IDs or a filter into tenant-scoped lead IDs.
func (s *Service) resolve(ctx context.Context, p auth.Principal, ids []uuid.UUID, f *Filter) ([]uuid.UUID, error) {
	if len(ids) > 0 {
		if len(ids) > maxBulk {
			return nil, apperr.Invalid("too many ids", nil)
		}
		return s.repo.IDs(ctx, p.OrgID, Filter{IDs: ids}, maxBulk)
	}
	if f == nil {
		return nil, apperr.Invalid("validation failed", map[string]string{"ids": "provide ids or a filter"})
	}
	return s.repo.IDs(ctx, p.OrgID, *f, maxBulk)
}

// ResolveIDs is used by campaigns to select an audience.
func (s *Service) ResolveIDs(ctx context.Context, p auth.Principal, ids []uuid.UUID, f *Filter) ([]uuid.UUID, error) {
	return s.resolve(ctx, p, ids, f)
}

func (s *Service) Events(ctx context.Context, p auth.Principal, id uuid.UUID) ([]Event, error) {
	return s.repo.Events(ctx, p.OrgID, id, 200)
}

func (s *Service) Facets(ctx context.Context, p auth.Principal, field string) ([]Facet, error) {
	return s.repo.Facets(ctx, p.OrgID, field)
}

func (s *Service) Each(ctx context.Context, p auth.Principal, f Filter, fn func(*Lead) error) error {
	return s.repo.Each(ctx, p.OrgID, f, fn)
}
