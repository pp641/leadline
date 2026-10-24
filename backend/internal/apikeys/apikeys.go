// Package apikeys issues organization-scoped API keys for programmatic access.
// Keys look like "ll_<prefix>_<secret>"; only a SHA-256 hash is stored and the
// plaintext is shown once at creation.
package apikeys

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/httpx"
	"leadline/pkg/secure"
)

type Key struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Role       auth.Role  `json:"role"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	CreatedAt  time.Time  `json:"created_at"`
	Token      string     `json:"token,omitempty"` // plaintext, creation response only
}

type Service struct {
	pool  *pgxpool.Pool
	audit *audit.Log
}

func NewService(pool *pgxpool.Pool, a *audit.Log) *Service { return &Service{pool: pool, audit: a} }

// LookupKey implements auth.KeyStore.
func (s *Service) LookupKey(ctx context.Context, token string) (*auth.APIKey, error) {
	var k auth.APIKey
	var role string
	var createdBy *uuid.UUID
	err := s.pool.QueryRow(ctx, `
		UPDATE api_keys SET last_used_at = now()
		WHERE key_hash = $1 AND revoked_at IS NULL
		RETURNING id, organization_id, created_by, role`, secure.HashToken(token)).Scan(&k.ID, &k.OrgID, &createdBy, &role)
	if err != nil {
		if database.IsNoRows(err) {
			return nil, apperr.Unauthorized("invalid API key")
		}
		return nil, err
	}
	if createdBy == nil {
		return nil, apperr.Unauthorized("API key creator no longer exists")
	}
	k.CreatedBy, k.Role = *createdBy, auth.Role(role)
	return &k, nil
}

func (s *Service) List(ctx context.Context, p auth.Principal) ([]Key, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, prefix, role, last_used_at, revoked_at, created_at
		FROM api_keys WHERE organization_id=$1 ORDER BY created_at DESC`, p.OrgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		var k Key
		var role string
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &role, &k.LastUsedAt, &k.RevokedAt, &k.CreatedAt); err != nil {
			return nil, err
		}
		k.Role = auth.Role(role)
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Service) Create(ctx context.Context, p auth.Principal, name string, role auth.Role) (*Key, error) {
	name = strings.TrimSpace(name)
	errs := map[string]string{}
	if name == "" || len(name) > 100 {
		errs["name"] = "is required (max 100 characters)"
	}
	if role != auth.RoleAdmin && role != auth.RoleMember && role != auth.RoleViewer {
		errs["role"] = "must be admin, member or viewer"
	} else if !p.Role.AtLeast(role) {
		errs["role"] = "cannot exceed your own role"
	}
	if len(errs) > 0 {
		return nil, apperr.Invalid("validation failed", errs)
	}
	if p.APIKeyID != uuid.Nil {
		return nil, apperr.Forbidden("API keys cannot create API keys")
	}
	prefix, err := secure.RandomToken(6)
	if err != nil {
		return nil, err
	}
	prefix = strings.NewReplacer("-", "x", "_", "y").Replace(prefix)
	secret, err := secure.RandomToken(32)
	if err != nil {
		return nil, err
	}
	token := auth.APIKeyPrefix + prefix + "_" + secret
	k := Key{Name: name, Prefix: auth.APIKeyPrefix + prefix, Role: role, Token: token}
	if err := s.pool.QueryRow(ctx, `INSERT INTO api_keys (organization_id, name, prefix, key_hash, role, created_by)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, created_at`, p.OrgID, name, k.Prefix, secure.HashToken(token), role, p.UserID).
		Scan(&k.ID, &k.CreatedAt); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "api_key.created", EntityType: "api_key", EntityID: k.ID.String(), Metadata: map[string]any{"role": role}})
	return &k, nil
}

func (s *Service) Revoke(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `UPDATE api_keys SET revoked_at=now() WHERE organization_id=$1 AND id=$2 AND revoked_at IS NULL`, p.OrgID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("API key")
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "api_key.revoked", EntityType: "api_key", EntityID: id.String()})
	return nil
}

type Handler struct {
	svc *Service
	mw  *auth.Middleware
}

func NewHandler(svc *Service, mw *auth.Middleware) *Handler { return &Handler{svc: svc, mw: mw} }

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/api-keys", h.mw.Org(auth.RoleAdmin, h.list))
	mux.HandleFunc("POST /api/v1/api-keys", h.mw.Org(auth.RoleAdmin, h.create))
	mux.HandleFunc("DELETE /api/v1/api-keys/{id}", h.mw.Org(auth.RoleAdmin, h.revoke))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.List(r.Context(), auth.MustPrincipal(r.Context()))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string    `json:"name"`
		Role auth.Role `json:"role"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	k, err := h.svc.Create(r.Context(), auth.MustPrincipal(r.Context()), in.Name, in.Role)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.Created(w, k)
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Error(w, r, apperr.NotFound("API key"))
		return
	}
	if err := h.svc.Revoke(r.Context(), auth.MustPrincipal(r.Context()), id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}
