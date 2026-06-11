package auth

import "github.com/google/uuid"

// Has reports whether the principal holds perm at any scope. Use only
// as a coarse pre-check; resource access must go through Require.
func (p *Principal) Has(perm Permission) bool {
	for _, g := range p.Grants {
		if hasPermission(g.Permissions, perm) {
			return true
		}
	}
	return false
}

// HasTenantWide reports whether perm is held at tenant scope.
func (p *Principal) HasTenantWide(perm Permission) bool {
	for _, g := range p.Grants {
		if g.Scope.Type == ScopeTenant && hasPermission(g.Permissions, perm) {
			return true
		}
	}
	return false
}

// CustomerIDsWith returns the customer IDs of customer-scoped grants
// holding perm; used to filter list endpoints for scoped principals.
func (p *Principal) CustomerIDsWith(perm Permission) []uuid.UUID {
	var out []uuid.UUID
	for _, g := range p.Grants {
		if g.Scope.Type == ScopeCustomer && hasPermission(g.Permissions, perm) {
			out = append(out, g.Scope.ID)
		}
	}
	return out
}
