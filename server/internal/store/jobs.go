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
	Kind       string     // script | package_install | package_remove
	ScriptID   *uuid.UUID // nil for package jobs
	ScriptName string     // "" for package jobs
	DeviceID   uuid.UUID
	SiteID     uuid.UUID // for scope filtering
	CustomerID uuid.UUID // for scope filtering
	Hostname   string
	CommandID  string
	Status     string
	TimeoutS   int
	Language   string          // "" for package jobs
	Parameters json.RawMessage // script params
	Spec       json.RawMessage // package spec ({"packages":[...]}) for package jobs
	ScheduleID *uuid.UUID
	CreatedAt  time.Time
	ExpiresAt  time.Time
	SentAt     *time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// PackageSpecJSON is the spec payload for package_install / package_remove
// jobs, stored in jobs.spec and forwarded verbatim to the agent.
type PackageSpecJSON struct {
	Packages []string `json:"packages"`
}

// JobTarget is the target selector for a dispatch or schedule: exactly
// one of the fields is set. Beyond explicit devices/site/customer, a
// target may select every active device of a given OS or carrying a
// given tag.
type JobTarget struct {
	DeviceIDs  []uuid.UUID `json:"device_ids,omitempty"`
	SiteID     *uuid.UUID  `json:"site_id,omitempty"`
	CustomerID *uuid.UUID  `json:"customer_id,omitempty"`
	OS         string      `json:"os,omitempty"`
	Tag        string      `json:"tag,omitempty"`
}

func (t JobTarget) Validate() error {
	n := 0
	if len(t.DeviceIDs) > 0 {
		n++
	}
	if t.SiteID != nil {
		n++
	}
	if t.CustomerID != nil {
		n++
	}
	if t.OS != "" {
		n++
	}
	if t.Tag != "" {
		n++
	}
	if n != 1 {
		return errors.New("target must set exactly one of device_ids, site_id, customer_id, os, tag")
	}
	return nil
}

// TargetDevice is one device a target selector resolved to; SiteID is
// carried for per-device authorization checks.
type TargetDevice struct {
	ID     uuid.UUID
	SiteID uuid.UUID
}

// ResolveTarget expands a target selector to the active devices it
// covers right now. Selector IDs outside the tenant resolve to nothing
// under RLS.
func ResolveTarget(ctx context.Context, tx pgx.Tx, t JobTarget) ([]TargetDevice, error) {
	var q string
	var arg any
	switch {
	case len(t.DeviceIDs) > 0:
		q = `SELECT id, site_id FROM devices WHERE id = ANY($1) AND status = 'active' ORDER BY hostname`
		arg = t.DeviceIDs
	case t.SiteID != nil:
		q = `SELECT id, site_id FROM devices WHERE site_id = $1 AND status = 'active' ORDER BY hostname`
		arg = *t.SiteID
	case t.CustomerID != nil:
		q = `SELECT d.id, d.site_id FROM devices d
		     JOIN sites s ON s.id = d.site_id
		     WHERE s.customer_id = $1 AND d.status = 'active' ORDER BY d.hostname`
		arg = *t.CustomerID
	case t.OS != "":
		q = `SELECT id, site_id FROM devices WHERE os = $1 AND status = 'active' ORDER BY hostname`
		arg = t.OS
	case t.Tag != "":
		q = `SELECT id, site_id FROM devices WHERE $1 = ANY(tags) AND status = 'active' ORDER BY hostname`
		arg = t.Tag
	default:
		return nil, errors.New("empty target")
	}
	rows, err := tx.Query(ctx, q, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TargetDevice
	for rows.Next() {
		var d TargetDevice
		if err := rows.Scan(&d.ID, &d.SiteID); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type JobOutput struct {
	Output   string
	ExitCode *int
}

// PendingJob is the minimal data needed to (re-)dispatch a job. For script
// jobs Language/ScriptBody/Parameters are set; for package jobs Spec is set.
type PendingJob struct {
	JobID      uuid.UUID
	Kind       string
	CommandID  string
	DeviceID   uuid.UUID
	Language   string
	ScriptBody string
	Parameters json.RawMessage
	Spec       json.RawMessage
	TimeoutS   int
	ExpiresAt  time.Time
}

// jobSelect LEFT JOINs scripts because package jobs carry no script_id;
// COALESCE keeps the nullable script columns scannable into plain strings.
const jobSelect = `
	SELECT j.id, j.kind, j.script_id, COALESCE(s.name, ''), j.device_id, d.site_id, si.customer_id, d.hostname,
	       j.command_id, j.status, j.timeout_s, COALESCE(j.language, ''), j.parameters,
	       COALESCE(j.spec, 'null'::jsonb), j.schedule_id,
	       j.created_at, j.expires_at, j.sent_at, j.started_at, j.finished_at
	FROM jobs j
	LEFT JOIN scripts s ON s.id = j.script_id
	JOIN devices d ON d.id = j.device_id
	JOIN sites si ON si.id = d.site_id`

func scanJob(row pgx.Row) (Job, error) {
	var j Job
	err := row.Scan(
		&j.ID, &j.Kind, &j.ScriptID, &j.ScriptName, &j.DeviceID, &j.SiteID, &j.CustomerID, &j.Hostname,
		&j.CommandID, &j.Status, &j.TimeoutS, &j.Language, &j.Parameters, &j.Spec, &j.ScheduleID,
		&j.CreatedAt, &j.ExpiresAt, &j.SentAt, &j.StartedAt, &j.FinishedAt)
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

// CreateJob inserts one per-device job. createdBy is nil for jobs fired
// by a schedule (system actor); scheduleID is nil for interactive
// dispatches.
func CreateJob(ctx context.Context, tx pgx.Tx,
	tenantID, scriptID, deviceID uuid.UUID, createdBy, scheduleID *uuid.UUID,
	timeoutS int, expiresAt time.Time, params json.RawMessage,
	scriptBody, language string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var commandID string
	err := tx.QueryRow(ctx, `
		INSERT INTO jobs (tenant_id, script_id, device_id, timeout_s, expires_at,
		                  parameters, script_body, language, created_by, schedule_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, command_id`,
		tenantID, scriptID, deviceID, timeoutS, expiresAt,
		params, scriptBody, language, createdBy, scheduleID).
		Scan(&id, &commandID)
	return id, commandID, err
}

// CreatePackageJob inserts one per-device package install/remove job. kind
// is "package_install" or "package_remove"; spec is the JSON package spec
// ({"packages":[...]}). Script columns are left NULL.
func CreatePackageJob(ctx context.Context, tx pgx.Tx,
	tenantID, deviceID uuid.UUID, createdBy, scheduleID *uuid.UUID,
	kind string, timeoutS int, expiresAt time.Time, spec json.RawMessage) (uuid.UUID, string, error) {
	var id uuid.UUID
	var commandID string
	err := tx.QueryRow(ctx, `
		INSERT INTO jobs (tenant_id, device_id, kind, spec, timeout_s, expires_at, created_by, schedule_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, command_id`,
		tenantID, deviceID, kind, spec, timeoutS, expiresAt, createdBy, scheduleID).
		Scan(&id, &commandID)
	return id, commandID, err
}

// ExpireQueuedJobs sweeps queued jobs past their expiry to status
// 'expired'. Returns the number of jobs expired.
func ExpireQueuedJobs(ctx context.Context, tx pgx.Tx) (int64, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE jobs SET status='expired', finished_at=now()
		WHERE status IN ('pending','sent') AND expires_at < now()`)
	return tag.RowsAffected(), err
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
		SELECT id, kind, command_id, device_id, COALESCE(language, ''), COALESCE(script_body, ''),
		       parameters, COALESCE(spec, 'null'::jsonb), timeout_s, expires_at
		FROM jobs
		WHERE device_id=$1 AND status IN ('pending','sent') AND expires_at > now()
		ORDER BY created_at`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingJob
	for rows.Next() {
		var p PendingJob
		if err := rows.Scan(&p.JobID, &p.Kind, &p.CommandID, &p.DeviceID,
			&p.Language, &p.ScriptBody, &p.Parameters, &p.Spec, &p.TimeoutS, &p.ExpiresAt); err != nil {
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
