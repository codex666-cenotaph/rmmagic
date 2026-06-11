package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Role struct {
	ID          uuid.UUID
	Name        string
	Permissions []string
	IsBuiltin   bool
}

func ListRoles(ctx context.Context, tx pgx.Tx) ([]Role, error) {
	rows, err := tx.Query(ctx,
		"SELECT id, name, permissions, is_builtin FROM roles ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Role
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.ID, &r.Name, &r.Permissions, &r.IsBuiltin); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func GetRole(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Role, error) {
	var r Role
	err := tx.QueryRow(ctx,
		"SELECT id, name, permissions, is_builtin FROM roles WHERE id = $1", id).Scan(
		&r.ID, &r.Name, &r.Permissions, &r.IsBuiltin)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func CreateRole(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, name string, permissions []string, builtin bool) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO roles (tenant_id, name, permissions, is_builtin)
		VALUES ($1, $2, $3, $4) RETURNING id`,
		tenantID, name, permissions, builtin).Scan(&id)
	return id, err
}

type RoleAssignment struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	RoleID    uuid.UUID
	RoleName  string
	ScopeType string
	ScopeID   *uuid.UUID
	CreatedAt time.Time
}

func ListAssignmentsForTenant(ctx context.Context, tx pgx.Tx) ([]RoleAssignment, error) {
	rows, err := tx.Query(ctx, `
		SELECT a.id, a.user_id, a.role_id, r.name, a.scope_type, a.scope_id, a.created_at
		FROM role_assignments a JOIN roles r ON r.id = a.role_id
		ORDER BY a.created_at`)
	if err != nil {
		return nil, err
	}
	return scanAssignments(rows)
}

// GrantsForUser returns (role permissions, scope) pairs for the authz
// principal.
func GrantsForUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]RoleAssignment, [][]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT a.id, a.user_id, a.role_id, r.name, a.scope_type, a.scope_id, a.created_at, r.permissions
		FROM role_assignments a JOIN roles r ON r.id = a.role_id
		WHERE a.user_id = $1`, userID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var as []RoleAssignment
	var perms [][]string
	for rows.Next() {
		var a RoleAssignment
		var p []string
		if err := rows.Scan(&a.ID, &a.UserID, &a.RoleID, &a.RoleName,
			&a.ScopeType, &a.ScopeID, &a.CreatedAt, &p); err != nil {
			return nil, nil, err
		}
		as = append(as, a)
		perms = append(perms, p)
	}
	return as, perms, rows.Err()
}

func CreateAssignment(ctx context.Context, tx pgx.Tx, tenantID, userID, roleID uuid.UUID, scopeType string, scopeID *uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO role_assignments (tenant_id, user_id, role_id, scope_type, scope_id)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		tenantID, userID, roleID, scopeType, scopeID).Scan(&id)
	return id, err
}

func DeleteAssignment(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, "DELETE FROM role_assignments WHERE id = $1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func scanAssignments(rows pgx.Rows) ([]RoleAssignment, error) {
	defer rows.Close()
	var out []RoleAssignment
	for rows.Next() {
		var a RoleAssignment
		if err := rows.Scan(&a.ID, &a.UserID, &a.RoleID, &a.RoleName,
			&a.ScopeType, &a.ScopeID, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
