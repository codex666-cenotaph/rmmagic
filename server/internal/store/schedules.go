package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Schedule struct {
	ID         uuid.UUID
	ScriptID   uuid.UUID
	ScriptName string
	Name       string
	Cron       string
	Target     json.RawMessage
	Parameters json.RawMessage
	TimeoutS   int
	ExpiresInS int
	Enabled    bool
	NextRunAt  time.Time
	LastRunAt  *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

const scheduleSelect = `
	SELECT sc.id, sc.script_id, s.name, sc.name, sc.cron, sc.target, sc.parameters,
	       sc.timeout_s, sc.expires_in_s, sc.enabled, sc.next_run_at, sc.last_run_at,
	       sc.created_at, sc.updated_at
	FROM schedules sc
	JOIN scripts s ON s.id = sc.script_id`

func scanSchedule(row pgx.Row) (Schedule, error) {
	var s Schedule
	err := row.Scan(&s.ID, &s.ScriptID, &s.ScriptName, &s.Name, &s.Cron, &s.Target,
		&s.Parameters, &s.TimeoutS, &s.ExpiresInS, &s.Enabled, &s.NextRunAt,
		&s.LastRunAt, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

func ListSchedules(ctx context.Context, tx pgx.Tx) ([]Schedule, error) {
	rows, err := tx.Query(ctx, scheduleSelect+" ORDER BY sc.name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func GetSchedule(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Schedule, error) {
	return scanSchedule(tx.QueryRow(ctx, scheduleSelect+" WHERE sc.id = $1", id))
}

func CreateSchedule(ctx context.Context, tx pgx.Tx, tenantID, scriptID, createdBy uuid.UUID,
	name, cron string, target, params json.RawMessage,
	timeoutS, expiresInS int, enabled bool, nextRunAt time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO schedules (tenant_id, script_id, name, cron, target, parameters,
		                       timeout_s, expires_in_s, enabled, next_run_at, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
		tenantID, scriptID, name, cron, target, params,
		timeoutS, expiresInS, enabled, nextRunAt, createdBy).Scan(&id)
	return id, err
}

func UpdateSchedule(ctx context.Context, tx pgx.Tx, id uuid.UUID,
	name, cron string, target, params json.RawMessage,
	timeoutS, expiresInS int, enabled bool, nextRunAt time.Time) error {
	tag, err := tx.Exec(ctx, `
		UPDATE schedules SET name=$2, cron=$3, target=$4, parameters=$5,
		       timeout_s=$6, expires_in_s=$7, enabled=$8, next_run_at=$9, updated_at=now()
		WHERE id=$1`,
		id, name, cron, target, params, timeoutS, expiresInS, enabled, nextRunAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func DeleteSchedule(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `DELETE FROM schedules WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimDueSchedules locks and returns schedules due to fire. The row
// locks (SKIP LOCKED) make concurrent worker ticks claim disjoint sets;
// the caller must advance next_run_at in the same transaction.
func ClaimDueSchedules(ctx context.Context, tx pgx.Tx, limit int) ([]Schedule, error) {
	rows, err := tx.Query(ctx, `
		SELECT sc.id, sc.script_id, s.name, sc.name, sc.cron, sc.target, sc.parameters,
		       sc.timeout_s, sc.expires_in_s, sc.enabled, sc.next_run_at, sc.last_run_at,
		       sc.created_at, sc.updated_at
		FROM schedules sc
		JOIN scripts s ON s.id = sc.script_id
		WHERE sc.enabled AND sc.next_run_at <= now()
		ORDER BY sc.next_run_at
		LIMIT $1
		FOR UPDATE OF sc SKIP LOCKED`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// MarkScheduleRun records a firing and schedules the next one.
func MarkScheduleRun(ctx context.Context, tx pgx.Tx, id uuid.UUID, nextRunAt time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE schedules SET last_run_at=now(), next_run_at=$2, updated_at=now()
		WHERE id=$1`, id, nextRunAt)
	return err
}

// WorkerListTenants returns all active tenant IDs via the SECURITY
// DEFINER helper; the worker iterates these with WithTenant so all of
// its per-tenant work stays under RLS.
func WorkerListTenants(ctx context.Context, tx pgx.Tx) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, "SELECT worker_list_tenants()")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
