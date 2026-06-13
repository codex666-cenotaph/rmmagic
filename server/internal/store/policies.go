package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Policy struct {
	ID         uuid.UUID       `json:"id"`
	Name       string          `json:"name"`
	ScopeType  string          `json:"scope_type"`
	ScopeID    *uuid.UUID      `json:"scope_id"`
	ScopeTag   *string         `json:"scope_tag"`
	Enabled    bool            `json:"enabled"`
	Rules      json.RawMessage `json:"rules"`
	ChannelIDs []uuid.UUID     `json:"channel_ids"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

const policySelect = `
	SELECT id, name, scope_type, scope_id, scope_tag, enabled, rules, channel_ids, created_at, updated_at
	FROM policies`

func scanPolicy(row pgx.Row) (Policy, error) {
	var p Policy
	err := row.Scan(&p.ID, &p.Name, &p.ScopeType, &p.ScopeID, &p.ScopeTag, &p.Enabled,
		&p.Rules, &p.ChannelIDs, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// ListPolicies returns all policies, oldest first so the merge's
// "later wins at equal scope" tie-break favors the newest policy.
func ListPolicies(ctx context.Context, tx pgx.Tx) ([]Policy, error) {
	rows, err := tx.Query(ctx, policySelect+` ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Policy
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func GetPolicy(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Policy, error) {
	return scanPolicy(tx.QueryRow(ctx, policySelect+` WHERE id = $1`, id))
}

func CreatePolicy(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, p Policy, createdBy *uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO policies (tenant_id, name, scope_type, scope_id, scope_tag, enabled, rules, channel_ids, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		tenantID, p.Name, p.ScopeType, p.ScopeID, p.ScopeTag, p.Enabled, p.Rules, p.ChannelIDs, createdBy).Scan(&id)
	return id, err
}

func UpdatePolicy(ctx context.Context, tx pgx.Tx, p Policy) error {
	tag, err := tx.Exec(ctx, `
		UPDATE policies
		SET name=$2, scope_type=$3, scope_id=$4, scope_tag=$5, enabled=$6, rules=$7, channel_ids=$8, updated_at=now()
		WHERE id=$1`,
		p.ID, p.Name, p.ScopeType, p.ScopeID, p.ScopeTag, p.Enabled, p.Rules, p.ChannelIDs)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func DeletePolicy(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `DELETE FROM policies WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PolicyDeviceScope is the org placement of one device plus its tags,
// used to decide which policies apply to it.
type PolicyDeviceScope struct {
	DeviceID   uuid.UUID
	SiteID     uuid.UUID
	CustomerID uuid.UUID
	Tags       []string
}

// AppliesTo reports whether the policy covers the device.
func (p Policy) AppliesTo(d PolicyDeviceScope) bool {
	switch p.ScopeType {
	case "tenant":
		return true
	case "customer":
		return p.ScopeID != nil && *p.ScopeID == d.CustomerID
	case "site":
		return p.ScopeID != nil && *p.ScopeID == d.SiteID
	case "device":
		return p.ScopeID != nil && *p.ScopeID == d.DeviceID
	case "tag":
		if p.ScopeTag == nil {
			return false
		}
		for _, t := range d.Tags {
			if t == *p.ScopeTag {
				return true
			}
		}
		return false
	}
	return false
}
