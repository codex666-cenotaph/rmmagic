package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type staticResolver map[[2]uuid.UUID]bool

func (r staticResolver) Contains(_ context.Context, outer, inner Scope) (bool, error) {
	return r[[2]uuid.UUID{outer.ID, inner.ID}], nil
}

func TestRequire(t *testing.T) {
	customer := uuid.New()
	siteInCustomer := uuid.New()
	otherSite := uuid.New()
	res := staticResolver{{customer, siteInCustomer}: true}

	tenantTech := &Principal{Grants: []Grant{{
		Scope: TenantScope(), Permissions: []Permission{PermScriptsExecute},
	}}}
	customerTech := &Principal{Grants: []Grant{{
		Scope:       Scope{Type: ScopeCustomer, ID: customer},
		Permissions: []Permission{PermScriptsExecute},
	}}}

	tests := []struct {
		name    string
		p       *Principal
		perm    Permission
		target  Scope
		wantErr bool
	}{
		{"tenant grant covers any site", tenantTech, PermScriptsExecute, Scope{ScopeSite, otherSite}, false},
		{"missing permission denied", tenantTech, PermUsersManage, TenantScope(), true},
		{"customer grant covers contained site", customerTech, PermScriptsExecute, Scope{ScopeSite, siteInCustomer}, false},
		{"customer grant denies foreign site", customerTech, PermScriptsExecute, Scope{ScopeSite, otherSite}, true},
		{"customer grant denies tenant scope", customerTech, PermScriptsExecute, TenantScope(), true},
		{"exact scope match allowed", customerTech, PermScriptsExecute, Scope{ScopeCustomer, customer}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := WithPrincipal(context.Background(), tt.p)
			err := Require(ctx, res, tt.perm, tt.target)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Require() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrForbidden) {
				t.Fatalf("error must be ErrForbidden, got %v", err)
			}
		})
	}
}

func TestRequireNoPrincipalFailsClosed(t *testing.T) {
	err := Require(context.Background(), staticResolver{}, PermDevicesRead, TenantScope())
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden without principal, got %v", err)
	}
}

func TestBuiltinRolesAreSubsetsOfAll(t *testing.T) {
	known := map[Permission]bool{}
	for _, p := range All() {
		known[p] = true
	}
	for role, perms := range BuiltinRolePermissions() {
		for _, p := range perms {
			if !known[p] {
				t.Errorf("role %s grants unknown permission %s", role, p)
			}
		}
	}
}
