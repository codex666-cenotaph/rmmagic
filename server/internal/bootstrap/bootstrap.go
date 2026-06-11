// Package bootstrap creates the first tenant with its built-in roles
// and owner user. It must run on a privileged DB connection (the
// migration owner), because RLS prevents the app role from creating
// tenant rows.
package bootstrap

import (
	"context"
	"fmt"
	"net/mail"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

type Input struct {
	TenantName string
	Slug       string
	Email      string
	Password   string
}

func (in Input) validate() error {
	if strings.TrimSpace(in.TenantName) == "" || strings.TrimSpace(in.Slug) == "" {
		return fmt.Errorf("tenant name and slug are required")
	}
	if _, err := mail.ParseAddress(in.Email); err != nil {
		return fmt.Errorf("invalid email")
	}
	if len(in.Password) < 12 {
		return fmt.Errorf("password must be at least 12 characters")
	}
	return nil
}

// Run creates tenant + built-in roles + owner with a tenant-wide Owner
// assignment. Returns the new tenant ID.
func Run(ctx context.Context, st *store.Store, in Input) (uuid.UUID, error) {
	if err := in.validate(); err != nil {
		return uuid.Nil, err
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return uuid.Nil, err
	}

	var tenantID uuid.UUID
	err = st.System(ctx, func(tx pgx.Tx) error {
		tenantID, err = store.CreateTenant(ctx, tx, in.TenantName, strings.ToLower(in.Slug))
		if err != nil {
			return fmt.Errorf("create tenant: %w", err)
		}
		// Scope the rest of the transaction so RLS WITH CHECK passes on a
		// non-bypassing connection.
		if _, err := tx.Exec(ctx,
			"SELECT set_config('app.tenant_id', $1, true)", tenantID.String()); err != nil {
			return err
		}

		var ownerRoleID uuid.UUID
		for name, perms := range auth.BuiltinRolePermissions() {
			ps := make([]string, len(perms))
			for i, p := range perms {
				ps[i] = string(p)
			}
			id, err := store.CreateRole(ctx, tx, tenantID, name, ps, true)
			if err != nil {
				return fmt.Errorf("create role %s: %w", name, err)
			}
			if name == auth.RoleOwner {
				ownerRoleID = id
			}
		}

		userID, err := store.CreateUser(ctx, tx, tenantID, strings.ToLower(in.Email), hash)
		if err != nil {
			return fmt.Errorf("create owner user: %w", err)
		}
		if _, err := store.CreateAssignment(ctx, tx, tenantID, userID, ownerRoleID,
			string(auth.ScopeTenant), nil); err != nil {
			return fmt.Errorf("assign owner role: %w", err)
		}
		return store.InsertAudit(ctx, tx, tenantID, store.AuditEntry{
			ActorType: "system", Action: "tenant.bootstrap",
		})
	})
	if err != nil {
		return uuid.Nil, err
	}
	return tenantID, nil
}
