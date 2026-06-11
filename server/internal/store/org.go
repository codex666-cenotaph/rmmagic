package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Customer struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
}

func ListCustomers(ctx context.Context, tx pgx.Tx) ([]Customer, error) {
	rows, err := tx.Query(ctx,
		"SELECT id, name, created_at FROM customers ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Customer
	for rows.Next() {
		var c Customer
		if err := rows.Scan(&c.ID, &c.Name, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCustomer resolves a customer inside the tenant-scoped transaction;
// foreign-tenant IDs come back ErrNotFound under RLS. Handlers must call
// this before authorizing against a caller-supplied customer ID.
func GetCustomer(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Customer, error) {
	var c Customer
	err := tx.QueryRow(ctx,
		"SELECT id, name, created_at FROM customers WHERE id = $1", id).Scan(&c.ID, &c.Name, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

func CreateCustomer(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, name string) (Customer, error) {
	var c Customer
	err := tx.QueryRow(ctx, `
		INSERT INTO customers (tenant_id, name) VALUES ($1, $2)
		RETURNING id, name, created_at`, tenantID, name).Scan(&c.ID, &c.Name, &c.CreatedAt)
	return c, err
}

func RenameCustomer(ctx context.Context, tx pgx.Tx, id uuid.UUID, name string) error {
	tag, err := tx.Exec(ctx,
		"UPDATE customers SET name = $2, updated_at = now() WHERE id = $1", id, name)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func DeleteCustomer(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, "DELETE FROM customers WHERE id = $1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func CustomerSiteCount(ctx context.Context, tx pgx.Tx, id uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx,
		"SELECT count(*) FROM sites WHERE customer_id = $1", id).Scan(&n)
	return n, err
}

type Site struct {
	ID         uuid.UUID
	CustomerID uuid.UUID
	Name       string
	Timezone   string
}

func ListSites(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) ([]Site, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, customer_id, name, timezone FROM sites
		WHERE customer_id = $1 ORDER BY name`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Site
	for rows.Next() {
		var s Site
		if err := rows.Scan(&s.ID, &s.CustomerID, &s.Name, &s.Timezone); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func GetSite(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Site, error) {
	var s Site
	err := tx.QueryRow(ctx,
		"SELECT id, customer_id, name, timezone FROM sites WHERE id = $1", id).Scan(
		&s.ID, &s.CustomerID, &s.Name, &s.Timezone)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

func CreateSite(ctx context.Context, tx pgx.Tx, tenantID, customerID uuid.UUID, name, timezone string) (Site, error) {
	var s Site
	err := tx.QueryRow(ctx, `
		INSERT INTO sites (tenant_id, customer_id, name, timezone)
		VALUES ($1, $2, $3, $4)
		RETURNING id, customer_id, name, timezone`,
		tenantID, customerID, name, timezone).Scan(&s.ID, &s.CustomerID, &s.Name, &s.Timezone)
	return s, err
}

func UpdateSite(ctx context.Context, tx pgx.Tx, id uuid.UUID, name, timezone string) error {
	tag, err := tx.Exec(ctx, `
		UPDATE sites SET name = $2, timezone = $3, updated_at = now()
		WHERE id = $1`, id, name, timezone)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func DeleteSite(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, "DELETE FROM sites WHERE id = $1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SiteBelongsToCustomer answers scope containment for the authz resolver.
func SiteBelongsToCustomer(ctx context.Context, tx pgx.Tx, siteID, customerID uuid.UUID) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM sites WHERE id = $1 AND customer_id = $2)",
		siteID, customerID).Scan(&ok)
	return ok, err
}
