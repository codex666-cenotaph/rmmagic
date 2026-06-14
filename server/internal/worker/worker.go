// Package worker runs background maintenance: firing cron schedules
// into jobs and expiring queued jobs that outlived their window. All
// per-tenant work happens inside WithTenant transactions, so RLS stays
// in force; the only cross-tenant primitive is the SECURITY DEFINER
// tenant-ID listing.
//
// Multiple worker processes are safe: due schedules are claimed with
// FOR UPDATE SKIP LOCKED and next_run_at is advanced in the claiming
// transaction, so a schedule fires once per due time.
package worker

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/robfig/cron/v3"

	"github.com/codex666-cenotaph/rmmagic/server/internal/gateway"
	"github.com/codex666-cenotaph/rmmagic/server/internal/notify"
	"github.com/codex666-cenotaph/rmmagic/server/internal/secrets"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

const (
	defaultInterval   = 30 * time.Second
	dueScheduleClaims = 100 // per tenant per tick
)

type Worker struct {
	Store *store.Store
	Log   *slog.Logger
	// Gateway, when the roles are colocated, lets fired jobs reach
	// online agents immediately instead of waiting for reconnect drain.
	Gateway  *gateway.Gateway
	Interval time.Duration
	// Notify sends alert notifications; nil leaves the outbox queued
	// (e.g. a worker pool without SMTP/network egress).
	Notify *notify.Notifier
	// Box unseals webhook signing secrets.
	Box *secrets.Box
}

func New(st *store.Store, log *slog.Logger, gw *gateway.Gateway) *Worker {
	return &Worker{Store: st, Log: log, Gateway: gw, Interval: defaultInterval}
}

func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	w.Log.Info("worker started", "interval", w.Interval.String())
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.Tick(ctx)
		}
	}
}

// Tick runs one maintenance pass over all tenants. Exported so tests
// can drive the worker deterministically.
func (w *Worker) Tick(ctx context.Context) {
	var tenants []uuid.UUID
	err := w.Store.System(ctx, func(tx pgx.Tx) error {
		var err error
		tenants, err = store.WorkerListTenants(ctx, tx)
		return err
	})
	if err != nil {
		w.Log.Error("worker: list tenants failed", "error", err)
		return
	}
	for _, tenantID := range tenants {
		if ctx.Err() != nil {
			return
		}
		w.tickTenant(ctx, tenantID)
	}
}

func (w *Worker) tickTenant(ctx context.Context, tenantID uuid.UUID) {
	// Expire queued jobs past their window.
	err := w.Store.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		n, err := store.ExpireQueuedJobs(ctx, tx)
		if err == nil && n > 0 {
			w.Log.Info("expired queued jobs", "tenant_id", tenantID, "count", n)
		}
		return err
	})
	if err != nil {
		w.Log.Error("worker: expire jobs failed", "tenant_id", tenantID, "error", err)
	}

	// Fire due schedules. Jobs are created in the claiming transaction;
	// delivery to online agents happens after commit.
	var created []store.PendingJob
	err = w.Store.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		due, err := store.ClaimDueSchedules(ctx, tx, dueScheduleClaims)
		if err != nil {
			return err
		}
		for _, sched := range due {
			jobs, err := w.fireSchedule(ctx, tx, tenantID, sched)
			if err != nil {
				return err
			}
			created = append(created, jobs...)
		}
		return nil
	})
	if err != nil {
		w.Log.Error("worker: schedule firing failed", "tenant_id", tenantID, "error", err)
		return
	}

	if w.Gateway != nil {
		for _, j := range created {
			if sent := w.Gateway.DispatchJob(ctx, tenantID, j.DeviceID, j.JobID, j.CommandID); sent {
				_ = w.Store.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
					return store.MarkJobSent(ctx, tx, j.JobID)
				})
			}
		}
	}

	// Reconcile rule-based app deployments: each enabled rule is claimed
	// at most hourly and creates install jobs for in-scope devices that
	// don't already have the app.
	if err := w.reconcileDeployments(ctx, tenantID); err != nil {
		w.Log.Error("worker: deployment reconciliation failed", "tenant_id", tenantID, "error", err)
	}

	// Evaluate monitoring policies and fan out alert notifications.
	if err := w.evaluateAlerts(ctx, tenantID); err != nil {
		w.Log.Error("worker: alert evaluation failed", "tenant_id", tenantID, "error", err)
	}
	if err := w.deliverNotifications(ctx, tenantID); err != nil {
		w.Log.Error("worker: notification delivery failed", "tenant_id", tenantID, "error", err)
	}
}

// fireSchedule creates one job per target device and advances
// next_run_at. A schedule whose cron no longer parses is disabled
// rather than wedging the tick.
func (w *Worker) fireSchedule(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, sched store.Schedule) ([]store.PendingJob, error) {
	cronSched, err := cron.ParseStandard(sched.Cron)
	if err != nil {
		w.Log.Error("schedule has invalid cron; disabling", "schedule_id", sched.ID, "cron", sched.Cron)
		_, err := tx.Exec(ctx, `UPDATE schedules SET enabled=false, updated_at=now() WHERE id=$1`, sched.ID)
		return nil, err
	}
	// Next from now, not from the missed slot: a worker that was down
	// for hours must not replay every missed firing.
	if err := store.MarkScheduleRun(ctx, tx, sched.ID, cronSched.Next(time.Now().UTC())); err != nil {
		return nil, err
	}

	script, err := store.GetScript(ctx, tx, sched.ScriptID)
	if err != nil || script.ArchivedAt != nil {
		w.Log.Warn("schedule skipped: script missing or archived", "schedule_id", sched.ID)
		return nil, nil
	}
	var target store.JobTarget
	if err := json.Unmarshal(sched.Target, &target); err != nil {
		w.Log.Error("schedule has invalid target", "schedule_id", sched.ID, "error", err)
		return nil, nil
	}
	devices, err := store.ResolveTarget(ctx, tx, target)
	if err != nil {
		return nil, err
	}

	expiresAt := time.Now().Add(time.Duration(sched.ExpiresInS) * time.Second)
	scheduleID := sched.ID
	var created []store.PendingJob
	for _, dev := range devices {
		jobID, commandID, err := store.CreateJob(ctx, tx,
			tenantID, sched.ScriptID, dev.ID, nil, &scheduleID,
			sched.TimeoutS, expiresAt, sched.Parameters, script.Body, script.Language)
		if err != nil {
			return nil, err
		}
		created = append(created, store.PendingJob{JobID: jobID, CommandID: commandID, DeviceID: dev.ID})
	}

	if err := store.InsertAudit(ctx, tx, tenantID, store.AuditEntry{
		ActorType: "system", Action: "schedule.run",
		TargetType: strPtr("schedule"), TargetID: &scheduleID,
		Details: mustJSONRaw(map[string]any{
			"schedule_name": sched.Name, "device_count": len(devices),
		}),
	}); err != nil {
		return nil, err
	}
	w.Log.Info("schedule fired", "schedule_id", sched.ID, "name", sched.Name, "jobs", len(devices))
	return created, nil
}

func strPtr(s string) *string { return &s }

func mustJSONRaw(v map[string]any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
