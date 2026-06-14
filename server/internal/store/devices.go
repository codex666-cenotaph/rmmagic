package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Device struct {
	ID           uuid.UUID
	SiteID       uuid.UUID
	CustomerID   uuid.UUID // joined from sites for scope checks/UI
	SiteName     string
	CustomerName string
	Hostname     string
	OS           string
	Arch          string
	AgentVersion  string
	Status        string
	UpdateChannel string
	LastSeenAt    *time.Time
	CreatedAt     time.Time
}

const deviceSelect = `
	SELECT d.id, d.site_id, s.customer_id, s.name, c.name,
	       d.hostname, d.os, d.arch, d.agent_version, d.status, d.update_channel, d.last_seen_at, d.created_at
	FROM devices d
	JOIN sites s ON s.id = d.site_id
	JOIN customers c ON c.id = s.customer_id`

func scanDevice(row pgx.Row) (Device, error) {
	var d Device
	err := row.Scan(&d.ID, &d.SiteID, &d.CustomerID, &d.SiteName, &d.CustomerName,
		&d.Hostname, &d.OS, &d.Arch, &d.AgentVersion, &d.Status, &d.UpdateChannel, &d.LastSeenAt, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

func ListDevices(ctx context.Context, tx pgx.Tx) ([]Device, error) {
	rows, err := tx.Query(ctx, deviceSelect+" ORDER BY d.hostname")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func GetDevice(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Device, error) {
	return scanDevice(tx.QueryRow(ctx, deviceSelect+" WHERE d.id = $1", id))
}

func CreateDevice(ctx context.Context, tx pgx.Tx, tenantID, siteID uuid.UUID, hostname, os, arch, agentVersion string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO devices (tenant_id, site_id, hostname, os, arch, agent_version)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		tenantID, siteID, hostname, os, arch, agentVersion).Scan(&id)
	return id, err
}

func AddDeviceCredential(ctx context.Context, tx pgx.Tx, tenantID, deviceID uuid.UUID, pubkey, fingerprint []byte) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO device_credentials (tenant_id, device_id, pubkey, fingerprint)
		VALUES ($1, $2, $3, $4)`, tenantID, deviceID, pubkey, fingerprint)
	return err
}

// TouchDevice updates liveness metadata on heartbeat/hello.
func TouchDevice(ctx context.Context, tx pgx.Tx, id uuid.UUID, agentVersion string) error {
	_, err := tx.Exec(ctx, `
		UPDATE devices SET last_seen_at = now(), updated_at = now(),
		       agent_version = CASE WHEN $2 = '' THEN agent_version ELSE $2 END
		WHERE id = $1`, id, agentVersion)
	return err
}

// SetDeviceUpdateChannel changes which release channel a device follows.
func SetDeviceUpdateChannel(ctx context.Context, tx pgx.Tx, id uuid.UUID, channel string) error {
	tag, err := tx.Exec(ctx, `
		UPDATE devices SET update_channel = $2, updated_at = now()
		WHERE id = $1 AND status = 'active'`, id, channel)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DecommissionDevice marks the device decommissioned and revokes all of
// its credentials; the caller is responsible for kicking live
// connections.
func DecommissionDevice(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		UPDATE devices SET status = 'decommissioned', updated_at = now()
		WHERE id = $1 AND status = 'active'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = tx.Exec(ctx, `
		UPDATE device_credentials SET revoked_at = now()
		WHERE device_id = $1 AND revoked_at IS NULL`, id)
	return err
}

// AuthDevice is the unscoped connection-auth lookup (SECURITY DEFINER).
type AuthDevice struct {
	TenantID uuid.UUID
	Status   string
	Pubkey   []byte
}

func LookupDevice(ctx context.Context, tx pgx.Tx, deviceID uuid.UUID) (AuthDevice, error) {
	var d AuthDevice
	err := tx.QueryRow(ctx, "SELECT * FROM auth_lookup_device($1)", deviceID).Scan(
		&d.TenantID, &d.Status, &d.Pubkey)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}
