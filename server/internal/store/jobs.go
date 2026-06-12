package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Job struct {
	ID         uuid.UUID
	ScriptID   uuid.UUID
	ScriptName string
	DeviceID   uuid.UUID
	Hostname   string
	CommandID  string
	Status     string
	TimeoutS   int
	Language   string
	Parameters json.RawMessage
	CreatedAt  time.Time
	SentAt     *time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

type JobOutput struct {
	Output   string
	ExitCode *int
}

// PendingJob is the minimal data needed to (re-)dispatch a job.
type PendingJob struct {
	JobID      uuid.UUID
	CommandID  string
	DeviceID   uuid.UUID
	Language   string
	ScriptBody string
	Parameters json.RawMessage
	TimeoutS   int
}

const jobSelect = `
	SELECT j.id, j.script_id, s.name, j.device_id, d.hostname,
	       j.command_id, j.status, j.timeout_s, j.language, j.parameters,
	       j.created_at, j.sent_at, j.started_at, j.finished_at
	FROM jobs j
	JOIN scripts s ON s.id = j.script_id
	JOIN devices d ON d.id = j.device_id`

func scanJob(row pgx.Row) (Job, error) {
	var j Job
	err := row.Scan(
		&j.ID, &j.ScriptID, &j.ScriptName, &j.DeviceID, &j.Hostname,
		&j.CommandID, &j.Status, &j.TimeoutS, &j.Language, &j.Parameters,
		&j.CreatedAt, &j.SentAt, &j.StartedAt, &j.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, ErrNotFound
	}
	return j, err
}

func ListJobs(ctx context.Context, tx pgx.Tx, deviceID *uuid.UUID, limit int) ([]Job, error) {
	q := jobSelect
	var args []any
	if deviceID != nil {
		q += " WHERE j.device_id = $1"
		args = append(args, *deviceID)
	}
	q += fmt.Sprintf(" ORDER BY j.created_at DESC LIMIT $%d", len(args)+1)
	args = append(args, limit)

	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func GetJob(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Job, error) {
	return scanJob(tx.QueryRow(ctx, jobSelect+" WHERE j.id = $1", id))
}

func GetJobOutput(ctx context.Context, tx pgx.Tx, jobID uuid.UUID) (JobOutput, error) {
	var o JobOutput
	err := tx.QueryRow(ctx, `SELECT output, exit_code FROM job_outputs WHERE job_id = $1`, jobID).
		Scan(&o.Output, &o.ExitCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

func CreateJob(ctx context.Context, tx pgx.Tx,
	tenantID, scriptID, deviceID, createdBy uuid.UUID,
	timeoutS int, params json.RawMessage, scriptBody, language string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var commandID string
	err := tx.QueryRow(ctx, `
		INSERT INTO jobs (tenant_id, script_id, device_id, timeout_s, parameters,
		                  script_body, language, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, command_id`,
		tenantID, scriptID, deviceID, timeoutS, params, scriptBody, language, createdBy).
		Scan(&id, &commandID)
	return id, commandID, err
}

// MarkJobSent transitions a pending job to sent and records when it was
// delivered over the WebSocket.
func MarkJobSent(ctx context.Context, tx pgx.Tx, jobID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE jobs SET status='sent', sent_at=now()
		WHERE id=$1 AND status IN ('pending','sent')`, jobID)
	return err
}

// ListPendingJobsForDevice returns jobs that need to be (re-)dispatched
// to the device: pending or sent (sent = delivered but ack never received
// or connection dropped before result came back).
func ListPendingJobsForDevice(ctx context.Context, tx pgx.Tx, deviceID uuid.UUID) ([]PendingJob, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, command_id, device_id, language, script_body, parameters, timeout_s
		FROM jobs
		WHERE device_id=$1 AND status IN ('pending','sent')
		ORDER BY created_at`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingJob
	for rows.Next() {
		var p PendingJob
		if err := rows.Scan(&p.JobID, &p.CommandID, &p.DeviceID,
			&p.Language, &p.ScriptBody, &p.Parameters, &p.TimeoutS); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CompleteJob records the result from the agent. It is idempotent; a
// duplicate delivery (same command_id) is silently ignored.
func CompleteJob(ctx context.Context, tx pgx.Tx,
	commandID, statusStr, output string, exitCode *int,
	startedAt, finishedAt time.Time) error {
	tag, err := tx.Exec(ctx, `
		UPDATE jobs SET status=$2, started_at=$3, finished_at=$4
		WHERE command_id=$1 AND status NOT IN ('succeeded','failed','timed_out','expired')`,
		commandID, statusStr, startedAt, finishedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Already completed or unknown command — idempotent no-op.
		return nil
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO job_outputs (job_id, tenant_id, output, exit_code)
		SELECT j.id, j.tenant_id, $2, $3
		FROM jobs j WHERE j.command_id=$1
		ON CONFLICT (job_id) DO NOTHING`,
		commandID, output, exitCode)
	return err
}
