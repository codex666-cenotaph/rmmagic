package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ShellSession is one interactive remote-shell session against a device.
type ShellSession struct {
	ID           uuid.UUID
	DeviceID     uuid.UUID
	Hostname     string // joined from devices for the UI
	OpenedBy     *uuid.UUID
	Status       string
	Cols         int
	Rows         int
	ClientIP     *string
	RecordingRef *string
	BytesIn      int64
	BytesOut     int64
	Error        *string
	StartedAt    time.Time
	EndedAt      *time.Time
}

const shellSessionSelect = `
	SELECT ss.id, ss.device_id, d.hostname, ss.opened_by, ss.status,
	       ss.cols, ss.rows, ss.client_ip::text, ss.recording_ref,
	       ss.bytes_in, ss.bytes_out, ss.error, ss.started_at, ss.ended_at
	FROM shell_sessions ss
	JOIN devices d ON d.id = ss.device_id`

func scanShellSession(row pgx.Row) (ShellSession, error) {
	var s ShellSession
	err := row.Scan(&s.ID, &s.DeviceID, &s.Hostname, &s.OpenedBy, &s.Status,
		&s.Cols, &s.Rows, &s.ClientIP, &s.RecordingRef,
		&s.BytesIn, &s.BytesOut, &s.Error, &s.StartedAt, &s.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

// CreateShellSession opens an active session row and returns its ID.
func CreateShellSession(ctx context.Context, tx pgx.Tx, tenantID, deviceID uuid.UUID,
	openedBy *uuid.UUID, clientIP string, cols, rows int) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO shell_sessions (tenant_id, device_id, opened_by, client_ip, cols, rows)
		VALUES ($1, $2, $3, nullif($4,'')::inet, $5, $6) RETURNING id`,
		tenantID, deviceID, openedBy, clientIP, cols, rows).Scan(&id)
	return id, err
}

// FinishShellSession finalizes a session with its terminal status, byte
// counts, and (on success) the recording storage key.
func FinishShellSession(ctx context.Context, tx pgx.Tx, id uuid.UUID,
	status string, recordingRef string, bytesIn, bytesOut int64, errMsg string) error {
	_, err := tx.Exec(ctx, `
		UPDATE shell_sessions
		SET status = $2,
		    recording_ref = nullif($3,''),
		    bytes_in = $4, bytes_out = $5,
		    error = nullif($6,''),
		    ended_at = now()
		WHERE id = $1`,
		id, status, recordingRef, bytesIn, bytesOut, errMsg)
	return err
}

func GetShellSession(ctx context.Context, tx pgx.Tx, id uuid.UUID) (ShellSession, error) {
	return scanShellSession(tx.QueryRow(ctx, shellSessionSelect+" WHERE ss.id = $1", id))
}

// ListShellSessions returns the most recent sessions for a device.
func ListShellSessions(ctx context.Context, tx pgx.Tx, deviceID uuid.UUID, limit int) ([]ShellSession, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := tx.Query(ctx, shellSessionSelect+
		" WHERE ss.device_id = $1 ORDER BY ss.started_at DESC LIMIT $2", deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShellSession
	for rows.Next() {
		s, err := scanShellSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
