package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Tenant struct {
	ID   uuid.UUID
	Name string
	Slug string
}

func GetTenant(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Tenant, error) {
	var t Tenant
	err := tx.QueryRow(ctx,
		"SELECT id, name, slug FROM tenants WHERE id = $1", id).Scan(&t.ID, &t.Name, &t.Slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// CreateTenant inserts a tenant row. Only the bootstrap path may call
// this, on a privileged (non-rmm_app) connection: RLS on tenants blocks
// the app role from inserting rows for a tenant scope it doesn't have.
func CreateTenant(ctx context.Context, tx pgx.Tx, name, slug string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx,
		"INSERT INTO tenants (name, slug) VALUES ($1, $2) RETURNING id", name, slug).Scan(&id)
	return id, err
}
