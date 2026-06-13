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

type Alert struct {
	ID         uuid.UUID
	DeviceID   uuid.UUID
	Hostname   string
	SiteID     uuid.UUID
	CustomerID uuid.UUID
	PolicyID   *uuid.UUID
	RuleType   string
	DedupKey   string
	Severity   string
	Message    string
	Details    json.RawMessage
	ChannelIDs []uuid.UUID
	Status     string
	FiredAt    time.Time
	ResolvedAt *time.Time
	AckedBy    *uuid.UUID
	AckedAt    *time.Time
}

const alertSelect = `
	SELECT a.id, a.device_id, d.hostname, d.site_id, s.customer_id,
	       a.policy_id, a.rule_type, a.dedup_key, a.severity, a.message, a.details,
	       a.channel_ids, a.status, a.fired_at, a.resolved_at, a.acked_by, a.acked_at
	FROM alerts a
	JOIN devices d ON d.id = a.device_id
	JOIN sites s ON s.id = d.site_id`

func scanAlert(row pgx.Row) (Alert, error) {
	var a Alert
	err := row.Scan(&a.ID, &a.DeviceID, &a.Hostname, &a.SiteID, &a.CustomerID,
		&a.PolicyID, &a.RuleType, &a.DedupKey, &a.Severity, &a.Message, &a.Details,
		&a.ChannelIDs, &a.Status, &a.FiredAt, &a.ResolvedAt, &a.AckedBy, &a.AckedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// ListAlerts returns alerts newest first. status filters to
// firing/resolved; empty means both. deviceID nil means all devices.
func ListAlerts(ctx context.Context, tx pgx.Tx, status string, deviceID *uuid.UUID, limit int) ([]Alert, error) {
	q := alertSelect
	var args []any
	var conds []string
	if status != "" {
		args = append(args, status)
		conds = append(conds, fmt.Sprintf("a.status = $%d", len(args)))
	}
	if deviceID != nil {
		args = append(args, *deviceID)
		conds = append(conds, fmt.Sprintf("a.device_id = $%d", len(args)))
	}
	for i, c := range conds {
		if i == 0 {
			q += " WHERE " + c
		} else {
			q += " AND " + c
		}
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY a.fired_at DESC LIMIT $%d", len(args))

	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func GetAlert(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Alert, error) {
	return scanAlert(tx.QueryRow(ctx, alertSelect+" WHERE a.id = $1", id))
}

// FiringAlert is the minimal view the evaluator needs of an open alert.
type FiringAlert struct {
	ID         uuid.UUID
	RuleType   string
	DedupKey   string
	ChannelIDs []uuid.UUID
}

func ListFiringAlertsForDevice(ctx context.Context, tx pgx.Tx, deviceID uuid.UUID) ([]FiringAlert, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, rule_type, dedup_key, channel_ids
		FROM alerts WHERE device_id = $1 AND status = 'firing'`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FiringAlert
	for rows.Next() {
		var a FiringAlert
		if err := rows.Scan(&a.ID, &a.RuleType, &a.DedupKey, &a.ChannelIDs); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// OpenAlert inserts a firing alert unless one is already open for the
// dedup key (the partial unique index makes this race-safe across
// workers). Returns the new alert ID and true when a row was inserted.
func OpenAlert(ctx context.Context, tx pgx.Tx, tenantID, deviceID uuid.UUID, policyID *uuid.UUID,
	ruleType, dedupKey, severity, message string, details json.RawMessage, channelIDs []uuid.UUID) (uuid.UUID, bool, error) {
	if details == nil {
		details = json.RawMessage(`{}`)
	}
	if channelIDs == nil {
		channelIDs = []uuid.UUID{}
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO alerts (tenant_id, device_id, policy_id, rule_type, dedup_key,
		                    severity, message, details, channel_ids)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (tenant_id, dedup_key) WHERE status = 'firing' DO NOTHING
		RETURNING id`,
		tenantID, deviceID, policyID, ruleType, dedupKey, severity, message, details, channelIDs).
		Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil // already firing
	}
	return id, err == nil, err
}

// ResolveAlert closes one firing alert; returns ErrNotFound when it was
// not firing (already resolved or unknown).
func ResolveAlert(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		UPDATE alerts SET status = 'resolved', resolved_at = now()
		WHERE id = $1 AND status = 'firing'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func AckAlert(ctx context.Context, tx pgx.Tx, id, userID uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		UPDATE alerts SET acked_by = $2, acked_at = now()
		WHERE id = $1 AND status = 'firing' AND acked_at IS NULL`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResolveAlertsForDecommissioned closes open alerts on devices that are
// no longer active; their conditions can never be re-confirmed.
func ResolveAlertsForDecommissioned(ctx context.Context, tx pgx.Tx) (int64, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE alerts SET status = 'resolved', resolved_at = now()
		WHERE status = 'firing'
		  AND device_id IN (SELECT id FROM devices WHERE status <> 'active')`)
	return tag.RowsAffected(), err
}

// --- evaluation inputs ---

// EvalDevice is one active device plus the telemetry the evaluator
// needs, assembled by CollectEvalDevices.
type EvalDevice struct {
	DeviceID   uuid.UUID
	SiteID     uuid.UUID
	CustomerID uuid.UUID
	Tags       []string
	Hostname   string
	LastSeenAt *time.Time

	HasRecentStats bool
	AvgCPUPct      float64
	MemUsedPct     float64
	Disks          json.RawMessage // latest sample's [{mount, used, total}]

	Services          json.RawMessage // latest [{name, state}]
	ServicesUpdatedAt *time.Time
}

// CollectEvalDevices loads all active devices with recent stats
// aggregates (window for CPU averaging) and latest service states, in
// three set-based queries instead of per-device round trips.
func CollectEvalDevices(ctx context.Context, tx pgx.Tx, window time.Duration) ([]EvalDevice, error) {
	rows, err := tx.Query(ctx, `
		SELECT d.id, d.site_id, s.customer_id, d.tags, d.hostname, d.last_seen_at
		FROM devices d JOIN sites s ON s.id = d.site_id
		WHERE d.status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EvalDevice
	idx := map[uuid.UUID]int{}
	for rows.Next() {
		var d EvalDevice
		if err := rows.Scan(&d.DeviceID, &d.SiteID, &d.CustomerID, &d.Tags, &d.Hostname, &d.LastSeenAt); err != nil {
			return nil, err
		}
		idx[d.DeviceID] = len(out)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	since := time.Now().Add(-window)
	srows, err := tx.Query(ctx, `
		SELECT device_id, avg(cpu_pct),
		       (array_agg(mem_used ORDER BY ts DESC))[1],
		       (array_agg(mem_total ORDER BY ts DESC))[1],
		       (array_agg(disks ORDER BY ts DESC))[1]
		FROM device_stats WHERE ts >= $1 GROUP BY device_id`, since)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	for srows.Next() {
		var deviceID uuid.UUID
		var avgCPU float64
		var memUsed, memTotal int64
		var disks json.RawMessage
		if err := srows.Scan(&deviceID, &avgCPU, &memUsed, &memTotal, &disks); err != nil {
			return nil, err
		}
		if i, ok := idx[deviceID]; ok {
			out[i].HasRecentStats = true
			out[i].AvgCPUPct = avgCPU
			if memTotal > 0 {
				out[i].MemUsedPct = float64(memUsed) / float64(memTotal) * 100
			}
			out[i].Disks = disks
		}
	}
	if err := srows.Err(); err != nil {
		return nil, err
	}

	svcRows, err := tx.Query(ctx, `SELECT device_id, services, updated_at FROM device_services`)
	if err != nil {
		return nil, err
	}
	defer svcRows.Close()
	for svcRows.Next() {
		var deviceID uuid.UUID
		var services json.RawMessage
		var updatedAt time.Time
		if err := svcRows.Scan(&deviceID, &services, &updatedAt); err != nil {
			return nil, err
		}
		if i, ok := idx[deviceID]; ok {
			out[i].Services = services
			out[i].ServicesUpdatedAt = &updatedAt
		}
	}
	return out, svcRows.Err()
}

// --- notification outbox ---

// EnqueueDeliveries inserts one pending delivery per channel that still
// exists (stale channel IDs on policies/alerts are skipped).
func EnqueueDeliveries(ctx context.Context, tx pgx.Tx, tenantID, alertID uuid.UUID, channelIDs []uuid.UUID, event string) error {
	if len(channelIDs) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO notification_deliveries (tenant_id, alert_id, channel_id, event)
		SELECT $1, $2, c.id, $4
		FROM notification_channels c WHERE c.id = ANY($3)`,
		tenantID, alertID, channelIDs, event)
	return err
}

// Delivery is one claimed outbox row joined with everything the sender
// needs.
type Delivery struct {
	ID        uuid.UUID
	AlertID   uuid.UUID
	ChannelID uuid.UUID
	Event     string
	Attempts  int
}

const maxDeliveryAttempts = 5

// ClaimDueDeliveries claims up to limit due pending deliveries with
// SKIP LOCKED and pre-schedules the next retry (exponential backoff),
// so a crashed worker never loses or duplicates a notification beyond
// at-least-once semantics. The actual send happens after commit.
func ClaimDueDeliveries(ctx context.Context, tx pgx.Tx, limit int) ([]Delivery, error) {
	rows, err := tx.Query(ctx, `
		UPDATE notification_deliveries nd
		SET attempts = nd.attempts + 1,
		    next_attempt_at = now() + least(power(2, nd.attempts)::int, 60) * interval '1 minute'
		WHERE nd.id IN (
			SELECT id FROM notification_deliveries
			WHERE status = 'pending' AND next_attempt_at <= now()
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		RETURNING nd.id, nd.alert_id, nd.channel_id, nd.event, nd.attempts`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.AlertID, &d.ChannelID, &d.Event, &d.Attempts); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func MarkDeliverySent(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE notification_deliveries SET status = 'sent', sent_at = now(), last_error = NULL
		WHERE id = $1`, id)
	return err
}

// MarkDeliveryError records a failed attempt; after the attempt budget
// is spent the delivery moves to failed and stops retrying.
func MarkDeliveryError(ctx context.Context, tx pgx.Tx, id uuid.UUID, attempt int, sendErr string) error {
	if len(sendErr) > 500 {
		sendErr = sendErr[:500]
	}
	status := "pending"
	if attempt >= maxDeliveryAttempts {
		status = "failed"
	}
	_, err := tx.Exec(ctx, `
		UPDATE notification_deliveries SET status = $2, last_error = $3 WHERE id = $1`,
		id, status, sendErr)
	return err
}
