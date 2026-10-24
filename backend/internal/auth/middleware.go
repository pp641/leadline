package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"leadline/pkg/apperr"
	"leadline/pkg/httpx"
	"leadline/pkg/logger"
)

// MembershipStore resolves a user's role in an organization (directly, or
// inherited from a parent agency organization).
type MembershipStore interface {
	MemberRole(ctx context.Context, orgID, userID uuid.UUID) (Role, error)
}

// APIKey is the identity behind an API key.
type APIKey struct {
	ID, OrgID, CreatedBy uuid.UUID
	Role                 Role
}

// KeyStore resolves API keys ("ll_…" bearer tokens).
type KeyStore interface {
	LookupKey(ctx context.Context, token string) (*APIKey, error)
}

// APIKeyPrefix distinguishes API keys from JWT access tokens.
const APIKeyPrefix = "ll_"

type Middleware struct {
	tokens  *TokenIssuer
	members MembershipStore
	keys    KeyStore
}

func NewMiddleware(t *TokenIssuer, m MembershipStore, k KeyStore) *Middleware {
	return &Middleware{tokens: t, members: m, keys: k}
}

// User authenticates the bearer token. Use for routes that are not
// organization-scoped (e.g. /auth/me, creating an organization).
func (m *Middleware) User(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := m.authenticate(r)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		ctx := WithPrincipal(r.Context(), Principal{UserID: userID})
		ctx = logger.With(ctx, "user_id", userID.String())
		httpx.SetAccessIdentity(ctx, userID.String(), "")
		next(w, r.WithContext(ctx))
	}
}

// Org authenticates the caller and authorizes them for the organization named
// in the X-Organization-ID header with at least role min. Every tenant-scoped
// handler is mounted behind this, and services take the OrgID from the
// resulting Principal — never from the request body.
func (m *Middleware) Org(min Role, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "+APIKeyPrefix); ok && m.keys != nil {
			m.withAPIKey(w, r, APIKeyPrefix+tok, min, next)
			return
		}
		userID, err := m.authenticate(r)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		orgID, err := uuid.Parse(r.Header.Get("X-Organization-ID"))
		if err != nil {
			httpx.Error(w, r, apperr.Invalid("X-Organization-ID header is required", nil))
			return
		}
		role, err := m.members.MemberRole(r.Context(), orgID, userID)
		if err != nil {
			if apperr.IsNotFound(err) {
				// Same response whether the org exists or not.
				httpx.Error(w, r, apperr.Forbidden("not a member of this organization"))
				return
			}
			httpx.Error(w, r, err)
			return
		}
		if !role.AtLeast(min) {
			httpx.Error(w, r, apperr.Forbidden("requires "+string(min)+" role"))
			return
		}
		ctx := WithPrincipal(r.Context(), Principal{UserID: userID, OrgID: orgID, Role: role})
		ctx = logger.With(ctx, "user_id", userID.String(), "organization_id", orgID.String())
		httpx.SetAccessIdentity(ctx, userID.String(), orgID.String())
		next(w, r.WithContext(ctx))
	}
}

// withAPIKey authorizes an API-key request. The key is bound to one
// organization; X-Organization-ID, if sent, must match it.
func (m *Middleware) withAPIKey(w http.ResponseWriter, r *http.Request, token string, min Role, next http.HandlerFunc) {
	key, err := m.keys.LookupKey(r.Context(), token)
	if err != nil {
		httpx.Error(w, r, apperr.Unauthorized("invalid or revoked API key"))
		return
	}
	if h := r.Header.Get("X-Organization-ID"); h != "" && h != key.OrgID.String() {
		httpx.Error(w, r, apperr.Forbidden("API key belongs to a different organization"))
		return
	}
	if !key.Role.AtLeast(min) {
		httpx.Error(w, r, apperr.Forbidden("API key requires "+string(min)+" role"))
		return
	}
	ctx := WithPrincipal(r.Context(), Principal{UserID: key.CreatedBy, OrgID: key.OrgID, Role: key.Role, APIKeyID: key.ID})
	ctx = logger.With(ctx, "api_key_id", key.ID.String(), "organization_id", key.OrgID.String())
	httpx.SetAccessIdentity(ctx, "key:"+key.ID.String(), key.OrgID.String())
	next(w, r.WithContext(ctx))
}

func (m *Middleware) authenticate(r *http.Request) (uuid.UUID, error) {
	h := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || token == "" {
		return uuid.Nil, apperr.Unauthorized("missing bearer token")
	}
	id, err := m.tokens.Parse(token)
	if err != nil {
		return uuid.Nil, apperr.Unauthorized("invalid or expired token")
	}
	return id, nil
}
