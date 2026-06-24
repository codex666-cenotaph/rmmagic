package store

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Health states a device (or one of its checks) can be in. Ordered worst
// to best by healthRank; aggregation takes the worst across a device's
// checks.
const (
	HealthHealthy  = "healthy"
	HealthWarning  = "warning"
	HealthCritical = "critical"
	HealthUnknown  = "unknown"
)

// Check-type values for schedules. "none" is a plain (non-health)
// schedule; the others interpret a job result into a health state.
const (
	CheckNone     = "none"
	CheckExitCode = "exit_code"
	CheckOutput   = "output"
)

// healthRank orders states from worst (highest) to best so a device's
// overall health is the max over its checks.
func healthRank(status string) int {
	switch status {
	case HealthCritical:
		return 3
	case HealthWarning:
		return 2
	case HealthUnknown:
		return 1
	default: // healthy
		return 0
	}
}

// DeviceHealthCheck is the latest result of one health check on one
// device. JSON tags match the dashboard's snake_case shape.
type DeviceHealthCheck struct {
	ScheduleID uuid.UUID  `json:"schedule_id"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Message    string     `json:"message"`
	JobID      *uuid.UUID `json:"job_id"`
	CheckedAt  time.Time  `json:"checked_at"`
}

// healthTokenRe matches a "HEALTH=<status>" token anywhere in script
// output (case-insensitive key and value); the last match wins.
var healthTokenRe = regexp.MustCompile(`(?i)\bHEALTH\s*[=:]\s*(healthy|ok|pass|passing|warning|warn|critical|crit|fail|failed|failing)\b`)

// MapHealth derives a health status from a check-schedule's job result.
// checkType selects the mapping; exitCode is nil when the job did not run
// to a normal completion (timeout/expiry), which is treated as critical
// for exit-code checks. The returned message is a short human summary.
func MapHealth(checkType string, warningExitCodes []int32, exitCode *int, output string) (status, message string) {
	switch checkType {
	case CheckExitCode:
		if exitCode == nil {
			return HealthCritical, "check did not complete"
		}
		if *exitCode == 0 {
			return HealthHealthy, "exit code 0"
		}
		for _, c := range warningExitCodes {
			if int(c) == *exitCode {
				return HealthWarning, "exit code " + itoa(*exitCode)
			}
		}
		return HealthCritical, "exit code " + itoa(*exitCode)

	case CheckOutput:
		m := healthTokenRe.FindAllStringSubmatch(output, -1)
		if len(m) == 0 {
			return HealthUnknown, "no HEALTH token in output"
		}
		token := strings.ToLower(m[len(m)-1][1])
		switch token {
		case "healthy", "ok", "pass", "passing":
			return HealthHealthy, "output reported " + token
		case "warning", "warn":
			return HealthWarning, "output reported " + token
		default: // critical, crit, fail, failed, failing
			return HealthCritical, "output reported " + token
		}

	default:
		return "", ""
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// jobHealthSource is the job + its check-schedule config, looked up by
// command_id. The INNER JOIN means non-check schedules (and ad-hoc jobs
// with no schedule) produce no row, so RecordJobHealth no-ops on them.
type jobHealthSource struct {
	TenantID         uuid.UUID
	DeviceID         uuid.UUID
	ScheduleID       uuid.UUID
	JobID            uuid.UUID
	CheckType        string
	WarningExitCodes []int32
}

// RecordJobHealth maps a completed check-schedule job to a health state
// and upserts it as the device's current result for that check. Jobs not
// produced by a health-check schedule are ignored. Safe to call on
// duplicate completions: the upsert just rewrites the same row.
func RecordJobHealth(ctx context.Context, tx pgx.Tx, commandID string, exitCode *int, output string) error {
	var src jobHealthSource
	err := tx.QueryRow(ctx, `
		SELECT j.tenant_id, j.device_id, j.schedule_id, j.id, sc.check_type, sc.warning_exit_codes
		FROM jobs j
		JOIN schedules sc ON sc.id = j.schedule_id
		WHERE j.command_id = $1 AND sc.check_type <> 'none'`, commandID).
		Scan(&src.TenantID, &src.DeviceID, &src.ScheduleID, &src.JobID, &src.CheckType, &src.WarningExitCodes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // not a health check
	}
	if err != nil {
		return err
	}

	status, message := MapHealth(src.CheckType, src.WarningExitCodes, exitCode, output)
	if status == "" {
		return nil
	}
	if len(message) > 500 {
		message = message[:500]
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO device_health (tenant_id, device_id, schedule_id, status, message, job_id, checked_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (device_id, schedule_id)
		DO UPDATE SET status = EXCLUDED.status, message = EXCLUDED.message,
		              job_id = EXCLUDED.job_id, checked_at = now()`,
		src.TenantID, src.DeviceID, src.ScheduleID, status, message, src.JobID)
	return err
}

// ListDeviceHealth returns the latest result of every health check that
// has run on the device, worst first.
func ListDeviceHealth(ctx context.Context, tx pgx.Tx, deviceID uuid.UUID) ([]DeviceHealthCheck, error) {
	rows, err := tx.Query(ctx, `
		SELECT dh.schedule_id, sc.name, dh.status, dh.message, dh.job_id, dh.checked_at
		FROM device_health dh
		JOIN schedules sc ON sc.id = dh.schedule_id
		WHERE dh.device_id = $1
		ORDER BY dh.checked_at DESC`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeviceHealthCheck
	for rows.Next() {
		var c DeviceHealthCheck
		if err := rows.Scan(&c.ScheduleID, &c.Name, &c.Status, &c.Message, &c.JobID, &c.CheckedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Stable worst-first ordering on top of the recency ordering.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && healthRank(out[j].Status) > healthRank(out[j-1].Status); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// DeviceHealthMap returns each device's overall health (the worst of its
// checks). Devices with no checks are absent from the map; callers treat
// absence as "unknown".
func DeviceHealthMap(ctx context.Context, tx pgx.Tx) (map[uuid.UUID]string, error) {
	rows, err := tx.Query(ctx, `SELECT device_id, status FROM device_health`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]string{}
	for rows.Next() {
		var id uuid.UUID
		var status string
		if err := rows.Scan(&id, &status); err != nil {
			return nil, err
		}
		if cur, ok := out[id]; !ok || healthRank(status) > healthRank(cur) {
			out[id] = status
		}
	}
	return out, rows.Err()
}
