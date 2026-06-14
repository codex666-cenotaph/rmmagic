package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AgentRelease is one signed agent binary in the global release catalog.
// The table has no tenant_id and no RLS: releases are shared by every
// tenant. Agents verify sha256 + the Ed25519 signature before swapping.
type AgentRelease struct {
	ID         uuid.UUID
	Channel    string
	Version    string
	OS         string
	Arch       string
	URL        string // external download URL; empty for server-hosted releases
	StorageKey string // blob storage key when the binary is server-hosted
	SHA256     string
	Signature  string // base64 detached Ed25519 signature over the binary
	SizeBytes  int64
	Notes      string
	CreatedBy  *uuid.UUID
	CreatedAt  time.Time
}

const releaseSelect = `
	SELECT id, channel, version, os, arch, COALESCE(url, ''), COALESCE(storage_key, ''),
	       sha256, signature, size_bytes, notes, created_by, created_at
	FROM agent_releases`

func scanRelease(row pgx.Row) (AgentRelease, error) {
	var r AgentRelease
	err := row.Scan(&r.ID, &r.Channel, &r.Version, &r.OS, &r.Arch, &r.URL, &r.StorageKey,
		&r.SHA256, &r.Signature, &r.SizeBytes, &r.Notes, &r.CreatedBy, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// ListReleases returns the release catalog newest-first, optionally
// filtered to one channel ("" = all channels).
func ListReleases(ctx context.Context, tx pgx.Tx, channel string) ([]AgentRelease, error) {
	q := releaseSelect
	var args []any
	if channel != "" {
		q += " WHERE channel = $1"
		args = append(args, channel)
	}
	q += " ORDER BY created_at DESC"
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentRelease
	for rows.Next() {
		r, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func GetRelease(ctx context.Context, tx pgx.Tx, id uuid.UUID) (AgentRelease, error) {
	return scanRelease(tx.QueryRow(ctx, releaseSelect+" WHERE id = $1", id))
}

// LatestRelease returns the newest release on a channel for an os/arch, or
// ErrNotFound if none exists.
func LatestRelease(ctx context.Context, tx pgx.Tx, channel, os, arch string) (AgentRelease, error) {
	return scanRelease(tx.QueryRow(ctx, releaseSelect+`
		WHERE channel = $1 AND os = $2 AND arch = $3
		ORDER BY created_at DESC LIMIT 1`, channel, os, arch))
}

// CreateRelease registers a release in the catalog. url may be empty for a
// server-hosted release whose binary is uploaded afterwards (which sets
// storage_key via SetReleaseStorageKey).
func CreateRelease(ctx context.Context, tx pgx.Tx, r AgentRelease) (uuid.UUID, error) {
	var url *string
	if r.URL != "" {
		url = &r.URL
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO agent_releases (channel, version, os, arch, url, sha256, signature, size_bytes, notes, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id`,
		r.Channel, r.Version, r.OS, r.Arch, url, r.SHA256, r.Signature, r.SizeBytes, r.Notes, r.CreatedBy).
		Scan(&id)
	return id, err
}

// SetReleaseStorageKey records the blob storage key (and size) after the
// binary has been uploaded for a server-hosted release.
func SetReleaseStorageKey(ctx context.Context, tx pgx.Tx, id uuid.UUID, key string, size int64) error {
	tag, err := tx.Exec(ctx,
		`UPDATE agent_releases SET storage_key=$2, size_bytes=$3 WHERE id=$1`, id, key, size)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeviceUpdate is the latest rollout state for one device.
type DeviceUpdate struct {
	DeviceID  uuid.UUID
	Version   string
	Phase     string
	Error     string
	OfferedAt time.Time
	UpdatedAt time.Time
}

// OfferDeviceUpdate records (upsert) that a device has been offered a
// version, resetting the phase to 'offered'. One row per device.
func OfferDeviceUpdate(ctx context.Context, tx pgx.Tx, tenantID, deviceID uuid.UUID, version string, offeredBy *uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO device_updates (device_id, tenant_id, version, phase, error, offered_by, offered_at, updated_at)
		VALUES ($1,$2,$3,'offered','',$4, now(), now())
		ON CONFLICT (device_id) DO UPDATE
		SET version=$3, phase='offered', error='', offered_by=$4, offered_at=now(), updated_at=now()`,
		deviceID, tenantID, version, offeredBy)
	return err
}

// SetDeviceUpdatePhase records a phase the agent reported (downloading,
// verified, applied, rolled_back, failed) for its current update version.
func SetDeviceUpdatePhase(ctx context.Context, tx pgx.Tx, deviceID uuid.UUID, version, phase, errMsg string) error {
	_, err := tx.Exec(ctx, `
		UPDATE device_updates SET phase=$2, error=$3, version=$4, updated_at=now()
		WHERE device_id=$1`, deviceID, phase, errMsg, version)
	return err
}

// ListDeviceUpdates returns rollout state for all devices in the tenant.
func ListDeviceUpdates(ctx context.Context, tx pgx.Tx) ([]DeviceUpdate, error) {
	rows, err := tx.Query(ctx, `
		SELECT device_id, version, phase, error, offered_at, updated_at
		FROM device_updates`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeviceUpdate
	for rows.Next() {
		var u DeviceUpdate
		if err := rows.Scan(&u.DeviceID, &u.Version, &u.Phase, &u.Error, &u.OfferedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetDeviceUpdate returns the rollout state for one device, or ErrNotFound.
func GetDeviceUpdate(ctx context.Context, tx pgx.Tx, deviceID uuid.UUID) (DeviceUpdate, error) {
	var u DeviceUpdate
	err := tx.QueryRow(ctx, `
		SELECT device_id, version, phase, error, offered_at, updated_at
		FROM device_updates WHERE device_id=$1`, deviceID).
		Scan(&u.DeviceID, &u.Version, &u.Phase, &u.Error, &u.OfferedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}
