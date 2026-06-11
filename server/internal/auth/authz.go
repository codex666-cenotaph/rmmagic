// Package auth implements authentication primitives and the RBAC
// authorization engine. Every API handler must call Require (directly
// or via route middleware) before touching tenant data.
package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrForbidden is returned when the principal lacks the required
// permission at the requested scope. Handlers translate it to 403 (or
// 404 for resources whose existence should not be revealed).
var ErrForbidden = errors.New("forbidden")

// ScopeType mirrors role_assignments.scope_type.
type ScopeType string

const (
	ScopeTenant   ScopeType = "tenant"
	ScopeCustomer ScopeType = "customer"
	ScopeSite     ScopeType = "site"
)

// Scope identifies where a permission is needed. ID is ignored for
// ScopeTenant.
type Scope struct {
	Type ScopeType
	ID   uuid.UUID
}

// TenantScope is the scope for tenant-wide actions.
func TenantScope() Scope { return Scope{Type: ScopeTenant} }

// Grant is one resolved role assignment of the current principal.
type Grant struct {
	Scope       Scope
	Permissions []Permission
}

// Principal is the authenticated actor for a request: a user session or
// an API token. It is placed on the request context by the auth
// middleware after credential verification (and MFA, for sessions).
type Principal struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
	// APITokenID is set when authenticated via API token; its permission
	// subset has already been intersected into Grants.
	APITokenID *uuid.UUID
	Grants     []Grant
}

type principalKey struct{}

// WithPrincipal returns a context carrying the authenticated principal.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom extracts the principal; ok is false on unauthenticated
// contexts (which handlers must treat as 401, never proceed).
func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(*Principal)
	return p, ok
}

// ScopeResolver answers containment questions about the org tree, e.g.
// whether a site belongs to a customer. Implemented by the store with a
// per-request cache.
type ScopeResolver interface {
	// Contains reports whether outer (a grant scope) contains inner (the
	// scope of the resource being accessed) within the same tenant.
	Contains(ctx context.Context, outer, inner Scope) (bool, error)
}

// Require returns nil iff the principal on ctx holds perm at a scope
// containing target. It fails closed: missing principal, unknown
// scopes, and resolver errors all deny.
func Require(ctx context.Context, res ScopeResolver, perm Permission, target Scope) error {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return ErrForbidden
	}
	for _, g := range p.Grants {
		if !hasPermission(g.Permissions, perm) {
			continue
		}
		if g.Scope.Type == ScopeTenant {
			return nil
		}
		if g.Scope == target {
			return nil
		}
		contains, err := res.Contains(ctx, g.Scope, target)
		if err != nil {
			return ErrForbidden
		}
		if contains {
			return nil
		}
	}
	return ErrForbidden
}

func hasPermission(perms []Permission, perm Permission) bool {
	for _, p := range perms {
		if p == perm {
			return true
		}
	}
	return false
}
