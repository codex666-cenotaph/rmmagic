package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

type jobJSON struct {
	ID         uuid.UUID       `json:"id"`
	ScriptID   uuid.UUID       `json:"script_id"`
	ScriptName string          `json:"script_name"`
	DeviceID   uuid.UUID       `json:"device_id"`
	Hostname   string          `json:"hostname"`
	CommandID  string          `json:"command_id"`
	Status     string          `json:"status"`
	TimeoutS   int             `json:"timeout_s"`
	Language   string          `json:"language"`
	Parameters json.RawMessage `json:"parameters"`
	ScheduleID *uuid.UUID      `json:"schedule_id,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	ExpiresAt  time.Time       `json:"expires_at"`
	SentAt     *time.Time      `json:"sent_at,omitempty"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
}

func toJobJSON(j store.Job) jobJSON {
	params := j.Parameters
	if params == nil {
		params = json.RawMessage("{}")
	}
	return jobJSON{
		ID: j.ID, ScriptID: j.ScriptID, ScriptName: j.ScriptName,
		DeviceID: j.DeviceID, Hostname: j.Hostname,
		CommandID: j.CommandID, Status: j.Status,
		TimeoutS: j.TimeoutS, Language: j.Language, Parameters: params,
		ScheduleID: j.ScheduleID,
		CreatedAt:  j.CreatedAt, ExpiresAt: j.ExpiresAt, SentAt: j.SentAt,
		StartedAt: j.StartedAt, FinishedAt: j.FinishedAt,
	}
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)

	var deviceFilter *uuid.UUID
	if v := r.URL.Query().Get("device_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid device_id")
			return
		}
		deviceFilter = &id
	}

	out := []jobJSON{}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		// If filtering by device, verify the caller can see it first.
		if deviceFilter != nil {
			if _, err := getAuthorizedDevice(r, tx, auth.PermDevicesRead, *deviceFilter); err != nil {
				return err
			}
		}
		jobs, err := store.ListJobs(ctx, tx, deviceFilter, 200)
		if err != nil {
			return err
		}
		// Scope-filter: a job is visible only if the caller may read the
		// device it ran on. Mirrors handleListDevices so a customer- or
		// site-scoped principal cannot enumerate other customers' jobs.
		all := p.HasTenantWide(auth.PermDevicesRead)
		allowedCustomers := map[uuid.UUID]bool{}
		if !all {
			for _, id := range p.CustomerIDsWith(auth.PermDevicesRead) {
				allowedCustomers[id] = true
			}
		}
		for _, j := range jobs {
			if all || allowedCustomers[j.CustomerID] {
				out = append(out, toJobJSON(j))
				continue
			}
			if requireInTx(ctx, tx, auth.PermDevicesRead, auth.Scope{Type: auth.ScopeSite, ID: j.SiteID}) == nil {
				out = append(out, toJobJSON(j))
			}
		}
		return nil
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var j store.Job
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		j, err = store.GetJob(ctx, tx, id)
		if err != nil {
			return err
		}
		// Scope check: caller must be able to see the device the job ran on.
		_, err = getAuthorizedDevice(r, tx, auth.PermDevicesRead, j.DeviceID)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toJobJSON(j))
}

func (s *Server) handleGetJobOutput(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var out store.JobOutput
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		j, err := store.GetJob(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := getAuthorizedDevice(r, tx, auth.PermDevicesRead, j.DeviceID); err != nil {
			return err
		}
		out, err = store.GetJobOutput(ctx, tx, id)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"output":    out.Output,
		"exit_code": out.ExitCode,
	})
}

type dispatchReq struct {
	// DeviceID is the single-device shorthand for Target.
	DeviceID     uuid.UUID       `json:"device_id"`
	Target       store.JobTarget `json:"target"`
	Parameters   json.RawMessage `json:"parameters"`
	TimeoutS     int             `json:"timeout_s"`
	ExpiresInS   int             `json:"expires_in_s"`
	ConfirmToken string          `json:"confirm_token"`
}

type createdJob struct {
	JobID     uuid.UUID
	DeviceID  uuid.UUID
	CommandID string
}

func (s *Server) handleDispatchJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	scriptID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}

	var req dispatchReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.DeviceID != uuid.Nil {
		req.Target = store.JobTarget{DeviceIDs: []uuid.UUID{req.DeviceID}}
	}
	if err := req.Target.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.TimeoutS <= 0 {
		req.TimeoutS = 300
	}
	if req.TimeoutS > 86400 {
		writeError(w, http.StatusBadRequest, "timeout_s must be at most 86400")
		return
	}
	if req.ExpiresInS <= 0 {
		req.ExpiresInS = 86400
	}
	if req.ExpiresInS > 7*86400 {
		writeError(w, http.StatusBadRequest, "expires_in_s must be at most 604800")
		return
	}
	if req.Parameters == nil {
		req.Parameters = json.RawMessage("{}")
	}
	expiresAt := time.Now().Add(time.Duration(req.ExpiresInS) * time.Second)

	var jobs []createdJob
	confirmed := true
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		sc, err := store.GetScript(ctx, tx, scriptID)
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
		if len(devices) == 0 {
			return errNoTargetDevices
		}
		if !s.requireBlastRadiusAck(w, p.TenantID, scriptID, req.Target, len(devices), req.ConfirmToken) {
			confirmed = false
			return nil // response already written; roll back nothing
		}
		for _, dev := range devices {
			jobID, commandID, err := store.CreateJob(ctx, tx,
				p.TenantID, scriptID, dev.ID, &p.UserID, nil,
				req.TimeoutS, expiresAt, req.Parameters, sc.Body, sc.Language)
			if err != nil {
				return err
			}
			jobs = append(jobs, createdJob{JobID: jobID, DeviceID: dev.ID, CommandID: commandID})
		}
		return recordAudit(ctx, tx, "job.dispatch", "script", scriptID,
			map[string]any{"target": req.Target, "device_count": len(devices)})
	})
	if errors.Is(err, errNoTargetDevices) {
		writeError(w, http.StatusBadRequest, errNoTargetDevices.Error())
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !confirmed {
		return
	}

	s.deliverJobs(ctx, p.TenantID, jobs)

	resp := map[string]any{
		"job_ids":      jobIDs(jobs),
		"device_count": len(jobs),
	}
	if len(jobs) == 1 {
		resp["job_id"] = jobs[0].JobID
	}
	writeJSON(w, http.StatusCreated, resp)
}

func jobIDs(jobs []createdJob) []uuid.UUID {
	ids := make([]uuid.UUID, len(jobs))
	for i, j := range jobs {
		ids[i] = j.JobID
	}
	return ids
}

// deliverJobs attempts immediate delivery to devices with an open
// gateway connection; offline devices get theirs on reconnect drain.
func (s *Server) deliverJobs(ctx context.Context, tenantID uuid.UUID, jobs []createdJob) {
	if s.Gateway == nil {
		return
	}
	for _, j := range jobs {
		if sent := s.Gateway.DispatchJob(ctx, tenantID, j.DeviceID, j.JobID, j.CommandID); sent {
			_ = s.Store.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
				return store.MarkJobSent(ctx, tx, j.JobID)
			})
		}
	}
}
