package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/robfig/cron/v3"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

// Schedules fire in UTC: cron expressions are evaluated against UTC
// wall-clock time by the worker.

type scheduleJSON struct {
	ID         uuid.UUID       `json:"id"`
	ScriptID   uuid.UUID       `json:"script_id"`
	ScriptName string          `json:"script_name"`
	Name       string          `json:"name"`
	Cron       string          `json:"cron"`
	Target     json.RawMessage `json:"target"`
	Parameters json.RawMessage `json:"parameters"`
	TimeoutS   int             `json:"timeout_s"`
	ExpiresInS int             `json:"expires_in_s"`
	Enabled    bool            `json:"enabled"`
	NextRunAt  time.Time       `json:"next_run_at"`
	LastRunAt  *time.Time      `json:"last_run_at"`
	CreatedAt  time.Time       `json:"created_at"`
}

func toScheduleJSON(s store.Schedule) scheduleJSON {
	return scheduleJSON{
		ID: s.ID, ScriptID: s.ScriptID, ScriptName: s.ScriptName,
		Name: s.Name, Cron: s.Cron, Target: s.Target, Parameters: s.Parameters,
		TimeoutS: s.TimeoutS, ExpiresInS: s.ExpiresInS, Enabled: s.Enabled,
		NextRunAt: s.NextRunAt, LastRunAt: s.LastRunAt, CreatedAt: s.CreatedAt,
	}
}

type scheduleBody struct {
	ScriptID     uuid.UUID       `json:"script_id"`
	Name         string          `json:"name"`
	Cron         string          `json:"cron"`
	Target       store.JobTarget `json:"target"`
	Parameters   json.RawMessage `json:"parameters"`
	TimeoutS     int             `json:"timeout_s"`
	ExpiresInS   int             `json:"expires_in_s"`
	Enabled      *bool           `json:"enabled"`
	ConfirmToken string          `json:"confirm_token"`
}

// validate normalizes defaults and returns (cron schedule, error message).
func (b *scheduleBody) validate() (cron.Schedule, string) {
	if b.ScriptID == uuid.Nil {
		return nil, "script_id is required"
	}
	if len(b.Name) == 0 || len(b.Name) > 200 {
		return nil, "name is required (max 200 chars)"
	}
	sched, err := cron.ParseStandard(b.Cron)
	if err != nil {
		return nil, "cron must be a valid 5-field cron expression or @hourly/@daily/@weekly/@monthly"
	}
	if err := b.Target.Validate(); err != nil {
		return nil, err.Error()
	}
	if b.TimeoutS <= 0 {
		b.TimeoutS = 300
	}
	if b.TimeoutS > 86400 {
		return nil, "timeout_s must be at most 86400"
	}
	if b.ExpiresInS <= 0 {
		b.ExpiresInS = 86400
	}
	if b.ExpiresInS < 60 || b.ExpiresInS > 604800 {
		return nil, "expires_in_s must be between 60 and 604800"
	}
	if b.Parameters == nil {
		b.Parameters = json.RawMessage("{}")
	}
	var tmp map[string]string
	if err := json.Unmarshal(b.Parameters, &tmp); err != nil {
		return nil, "parameters must be a JSON object of string values"
	}
	return sched, ""
}

func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	var schedules []store.Schedule
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		schedules, err = store.ListSchedules(ctx, tx)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]scheduleJSON, 0, len(schedules))
	for _, sc := range schedules {
		out = append(out, toScheduleJSON(sc))
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": out})
}

func (s *Server) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var sc store.Schedule
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		sc, err = store.GetSchedule(ctx, tx, id)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toScheduleJSON(sc))
}

func (s *Server) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	s.upsertSchedule(w, r, uuid.Nil)
}

func (s *Server) handleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	s.upsertSchedule(w, r, id)
}

// upsertSchedule handles create (id == Nil) and full update. Both
// validate the cron expression, authorize the target, and apply the
// blast-radius safeguard against the target's current resolution.
func (s *Server) upsertSchedule(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)

	var req scheduleBody
	if !decodeJSON(w, r, &req) {
		return
	}
	cronSched, msg := req.validate()
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	targetJSON, _ := json.Marshal(req.Target)
	nextRun := cronSched.Next(time.Now().UTC())

	confirmed := true
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		sc, err := store.GetScript(ctx, tx, req.ScriptID)
		if err != nil {
			return err
		}
		if sc.ArchivedAt != nil {
			return store.ErrNotFound
		}
		devices, err := resolveAuthorizedTarget(ctx, tx, req.Target)
		if err != nil {
			return err
		}
		// Unlike a dispatch, an empty resolution is allowed: the schedule
		// may target a site that gets devices later. The blast-radius ack
		// uses the count as of now.
		if !s.requireBlastRadiusAck(w, p.TenantID, req.ScriptID, req.Target, len(devices), req.ConfirmToken) {
			confirmed = false
			return nil
		}
		if id == uuid.Nil {
			id, err = store.CreateSchedule(ctx, tx, p.TenantID, req.ScriptID, p.UserID,
				req.Name, req.Cron, targetJSON, req.Parameters,
				req.TimeoutS, req.ExpiresInS, enabled, nextRun)
			if err != nil {
				return err
			}
			return recordAudit(ctx, tx, "schedule.create", "schedule", id,
				map[string]any{"name": req.Name, "cron": req.Cron, "device_count": len(devices)})
		}
		// Update: the schedule must belong to this tenant (RLS) and exist.
		if _, err := store.GetSchedule(ctx, tx, id); err != nil {
			return err
		}
		if err := store.UpdateSchedule(ctx, tx, id,
			req.Name, req.Cron, targetJSON, req.Parameters,
			req.TimeoutS, req.ExpiresInS, enabled, nextRun); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "schedule.update", "schedule", id,
			map[string]any{"name": req.Name, "cron": req.Cron, "enabled": enabled})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !confirmed {
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"id": id, "next_run_at": nextRun})
}

func (s *Server) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := store.DeleteSchedule(ctx, tx, id); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "schedule.delete", "schedule", id, nil)
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
