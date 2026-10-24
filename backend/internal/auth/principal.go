package auth

import (
	"context"

	"github.com/google/uuid"
)

type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	RoleViewer Role = "viewer"
)

var roleRank = map[Role]int{RoleViewer: 1, RoleMember: 2, RoleAdmin: 3, RoleOwner: 4}

func (r Role) Valid() bool { return roleRank[r] > 0 }

// AtLeast reports whether r grants every permission of min.
func (r Role) AtLeast(min Role) bool { return roleRank[r] >= roleRank[min] }

// Principal identifies the caller of a tenant-scoped request. OrgID is the
// zero UUID on routes that are not organization-scoped.
type Principal struct {
	UserID   uuid.UUID
	OrgID    uuid.UUID
	Role     Role
	APIKeyID uuid.UUID // set when authenticated with an API key (UserID is then the key's creator)
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// MustPrincipal is for handlers mounted behind the auth middleware.
func MustPrincipal(ctx context.Context) Principal {
	p, ok := FromContext(ctx)
	if !ok {
		panic("auth: no principal in context; route is missing auth middleware")
	}
	return p
}
