// Package organization implements tenants (organizations) and memberships.
package organization

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/internal/users"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/secure"
	"leadline/pkg/validate"
)

type Organization struct {
	ID                uuid.UUID  `json:"id"`
	Name              string     `json:"name"`
	Slug              string     `json:"slug"`
	LegalName         string     `json:"legal_name"`
	PhysicalAddress   string     `json:"physical_address"`
	DailySendLimit    int        `json:"daily_send_limit"`
	DataRetentionDays *int       `json:"data_retention_days"`
	Role              auth.Role  `json:"role,omitempty"` // caller's role, when listed for a user
	CreatedAt         time.Time  `json:"created_at"`
	ParentID          *uuid.UUID `json:"parent_id"` // set for agency client organizations
	IsAgency          bool       `json:"is_agency"`
}

type Member struct {
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	FullName  string    `json:"full_name"`
	Role      auth.Role `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// ───────────────────────── Repository ─────────────────────────

type Repository struct{ db database.DBTX }

func NewRepository(db database.DBTX) *Repository { return &Repository{db: db} }

const orgCols = `o.id, o.name, o.slug, o.legal_name, o.physical_address, o.daily_send_limit, o.data_retention_days, o.created_at, o.parent_id, o.is_agency`

func scanOrg(row pgx.Row, extra ...any) (*Organization, error) {
	var o Organization
	dest := append([]any{&o.ID, &o.Name, &o.Slug, &o.LegalName, &o.PhysicalAddress, &o.DailySendLimit, &o.DataRetentionDays, &o.CreatedAt, &o.ParentID, &o.IsAgency}, extra...)
	if err := row.Scan(dest...); err != nil {
		if database.IsNoRows(err) {
			return nil, apperr.NotFound("organization")
		}
		return nil, err
	}
	return &o, nil
}

// MemberRole implements auth.MembershipStore. Members of an agency inherit
// access to its client organizations; agency owners act as client admins.
func (r *Repository) MemberRole(ctx context.Context, orgID, userID uuid.UUID) (auth.Role, error) {
	var role string
	err := r.db.QueryRow(ctx, `
		SELECT role FROM (
			SELECT role, 0 AS pri FROM organization_members WHERE organization_id=$1 AND user_id=$2
			UNION ALL
			SELECT CASE m.role WHEN 'owner' THEN 'admin' ELSE m.role END, 1
			FROM organizations o JOIN organization_members m ON m.organization_id = o.parent_id
			WHERE o.id=$1 AND m.user_id=$2
		) x ORDER BY pri LIMIT 1`, orgID, userID).Scan(&role)
	if database.IsNoRows(err) {
		return "", apperr.NotFound("membership")
	}
	return auth.Role(role), err
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (*Organization, error) {
	return scanOrg(r.db.QueryRow(ctx, `SELECT `+orgCols+` FROM organizations o WHERE o.id=$1`, id))
}

func (r *Repository) ListForUser(ctx context.Context, userID uuid.UUID) ([]Organization, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+orgCols+`, m.role FROM organizations o
		JOIN organization_members m ON m.organization_id = o.id
		WHERE m.user_id = $1
		UNION ALL
		SELECT `+orgCols+`, CASE m.role WHEN 'owner' THEN 'admin' ELSE m.role END FROM organizations o
		JOIN organization_members m ON m.organization_id = o.parent_id
		WHERE m.user_id = $1 AND NOT EXISTS (SELECT 1 FROM organization_members d WHERE d.organization_id = o.id AND d.user_id = $1)
		ORDER BY 2`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Organization{}
	for rows.Next() {
		var role string
		o, err := scanOrg(rows, &role)
		if err != nil {
			return nil, err
		}
		o.Role = auth.Role(role)
		out = append(out, *o)
	}
	return out, rows.Err()
}

func (r *Repository) Members(ctx context.Context, orgID uuid.UUID) ([]Member, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.id, u.email::text, u.full_name, m.role, m.created_at
		FROM organization_members m JOIN users u ON u.id = m.user_id
		WHERE m.organization_id = $1 ORDER BY m.created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		var role string
		if err := rows.Scan(&m.UserID, &m.Email, &m.FullName, &role, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Role = auth.Role(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ───────────────────────── Service ─────────────────────────

type Service struct {
	pool  *pgxpool.Pool
	repo  *Repository
	users *users.Repository
	audit *audit.Log
}

func NewService(pool *pgxpool.Pool, repo *Repository, u *users.Repository, a *audit.Log) *Service {
	return &Service{pool: pool, repo: repo, users: u, audit: a}
}

type CreateInput struct {
	Name            string `json:"name"`
	LegalName       string `json:"legal_name"`
	PhysicalAddress string `json:"physical_address"`
}

type UpdateInput struct {
	Name              *string `json:"name"`
	LegalName         *string `json:"legal_name"`
	PhysicalAddress   *string `json:"physical_address"`
	DailySendLimit    *int    `json:"daily_send_limit"`
	DataRetentionDays *int    `json:"data_retention_days"`
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	s := strings.Trim(slugStrip.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		s = "org"
	}
	suffix, _ := secure.RandomToken(4)
	return s + "-" + strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(suffix))
}

// Create makes a new organization with the caller as owner.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, in CreateInput) (*Organization, error) {
	in.Name = strings.TrimSpace(in.Name)
	v := validate.Errors{}
	v.Required("name", in.Name)
	v.MaxLen("name", in.Name, 120)
	v.MaxLen("physical_address", in.PhysicalAddress, 500)
	if err := v.Err(); err != nil {
		return nil, err
	}
	var org *Organization
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		org, err = scanOrg(tx.QueryRow(ctx, `
			INSERT INTO organizations AS o (name, slug, legal_name, physical_address)
			VALUES ($1, $2, $3, $4) RETURNING `+orgCols,
			in.Name, slugify(in.Name), strings.TrimSpace(in.LegalName), strings.TrimSpace(in.PhysicalAddress)))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1, $2, 'owner')`, org.ID, userID)
		return err
	})
	if err != nil {
		return nil, err
	}
	org.Role = auth.RoleOwner
	s.audit.Record(ctx, audit.Entry{OrgID: org.ID, UserID: userID, Action: "organization.created", EntityType: "organization", EntityID: org.ID.String()})
	return org, nil
}

func (s *Service) ListForUser(ctx context.Context, userID uuid.UUID) ([]Organization, error) {
	return s.repo.ListForUser(ctx, userID)
}

func (s *Service) Get(ctx context.Context, p auth.Principal) (*Organization, error) {
	o, err := s.repo.Get(ctx, p.OrgID)
	if err != nil {
		return nil, err
	}
	o.Role = p.Role
	return o, nil
}

func (s *Service) Update(ctx context.Context, p auth.Principal, in UpdateInput) (*Organization, error) {
	v := validate.Errors{}
	if in.Name != nil {
		*in.Name = strings.TrimSpace(*in.Name)
		v.Required("name", *in.Name)
		v.MaxLen("name", *in.Name, 120)
	}
	if in.PhysicalAddress != nil {
		v.MaxLen("physical_address", *in.PhysicalAddress, 500)
	}
	if in.DailySendLimit != nil {
		v.Check(*in.DailySendLimit >= 0 && *in.DailySendLimit <= 100000, "daily_send_limit", "must be between 0 and 100000")
	}
	if in.DataRetentionDays != nil {
		v.Check(*in.DataRetentionDays >= 30, "data_retention_days", "must be at least 30 days")
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	o, err := scanOrg(s.pool.QueryRow(ctx, `
		UPDATE organizations o SET
			name = COALESCE($2, name),
			legal_name = COALESCE($3, legal_name),
			physical_address = COALESCE($4, physical_address),
			daily_send_limit = COALESCE($5, daily_send_limit),
			data_retention_days = CASE WHEN $6::boolean THEN $7 ELSE data_retention_days END,
			updated_at = now()
		WHERE o.id = $1 RETURNING `+orgCols,
		p.OrgID, in.Name, in.LegalName, in.PhysicalAddress, in.DailySendLimit, in.DataRetentionDays != nil, in.DataRetentionDays))
	if err != nil {
		return nil, err
	}
	o.Role = p.Role
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "organization.updated", EntityType: "organization", EntityID: p.OrgID.String()})
	return o, nil
}

func (s *Service) Delete(ctx context.Context, p auth.Principal) error {
	if p.Role != auth.RoleOwner {
		return apperr.Forbidden("only an owner can delete the organization")
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, p.OrgID)
	if err == nil {
		s.audit.Record(ctx, audit.Entry{UserID: p.UserID, Action: "organization.deleted", EntityType: "organization", EntityID: p.OrgID.String()})
	}
	return err
}

func (s *Service) Members(ctx context.Context, p auth.Principal) ([]Member, error) {
	return s.repo.Members(ctx, p.OrgID)
}

type AddMemberInput struct {
	Email string    `json:"email"`
	Role  auth.Role `json:"role"`
}

// AddMember adds an existing user. (Email invitations are a later milestone.)
func (s *Service) AddMember(ctx context.Context, p auth.Principal, in AddMemberInput) error {
	if !in.Role.Valid() {
		return apperr.Invalid("validation failed", map[string]string{"role": "must be owner, admin, member or viewer"})
	}
	if in.Role == auth.RoleOwner && p.Role != auth.RoleOwner {
		return apperr.Forbidden("only an owner can add owners")
	}
	u, err := s.users.GetByEmail(ctx, validate.NormalizeEmail(in.Email))
	if err != nil {
		if apperr.IsNotFound(err) {
			return apperr.Invalid("validation failed", map[string]string{"email": "no account exists with this email; ask them to register first"})
		}
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1,$2,$3)`, p.OrgID, u.ID, in.Role)
	if database.IsUniqueViolation(err) {
		return apperr.Conflict("already_member", "user is already a member")
	}
	if err == nil {
		s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "member.added", EntityType: "user", EntityID: u.ID.String(), Metadata: map[string]any{"role": in.Role}})
	}
	return err
}

func (s *Service) UpdateMemberRole(ctx context.Context, p auth.Principal, userID uuid.UUID, role auth.Role) error {
	if !role.Valid() {
		return apperr.Invalid("validation failed", map[string]string{"role": "invalid role"})
	}
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		current, err := lockMember(ctx, tx, p.OrgID, userID)
		if err != nil {
			return err
		}
		if (current == auth.RoleOwner || role == auth.RoleOwner) && p.Role != auth.RoleOwner {
			return apperr.Forbidden("only an owner can change owner roles")
		}
		if current == auth.RoleOwner && role != auth.RoleOwner {
			if err := ensureAnotherOwner(ctx, tx, p.OrgID, userID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE organization_members SET role=$3 WHERE organization_id=$1 AND user_id=$2`, p.OrgID, userID, role); err != nil {
			return err
		}
		s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "member.role_changed", EntityType: "user", EntityID: userID.String(), Metadata: map[string]any{"from": current, "to": role}})
		return nil
	})
}

func (s *Service) RemoveMember(ctx context.Context, p auth.Principal, userID uuid.UUID) error {
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		current, err := lockMember(ctx, tx, p.OrgID, userID)
		if err != nil {
			return err
		}
		if current == auth.RoleOwner {
			if p.Role != auth.RoleOwner {
				return apperr.Forbidden("only an owner can remove an owner")
			}
			if err := ensureAnotherOwner(ctx, tx, p.OrgID, userID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM organization_members WHERE organization_id=$1 AND user_id=$2`, p.OrgID, userID); err != nil {
			return err
		}
		s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "member.removed", EntityType: "user", EntityID: userID.String()})
		return nil
	})
}

func lockMember(ctx context.Context, tx pgx.Tx, orgID, userID uuid.UUID) (auth.Role, error) {
	// Lock all owner rows too so concurrent demotions can't remove the last owner.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM organization_members WHERE organization_id=$1 AND role='owner' FOR UPDATE`, orgID); err != nil {
		return "", err
	}
	var role string
	err := tx.QueryRow(ctx, `SELECT role FROM organization_members WHERE organization_id=$1 AND user_id=$2 FOR UPDATE`, orgID, userID).Scan(&role)
	if database.IsNoRows(err) {
		return "", apperr.NotFound("member")
	}
	return auth.Role(role), err
}

func ensureAnotherOwner(ctx context.Context, tx pgx.Tx, orgID, userID uuid.UUID) error {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM organization_members WHERE organization_id=$1 AND role='owner' AND user_id<>$2`, orgID, userID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return apperr.Conflict("last_owner", "an organization must keep at least one owner")
	}
	return nil
}
