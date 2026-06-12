package api

import (
	"encoding/json"
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
	CreatedAt  time.Time       `json:"created_at"`
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
		CreatedAt: j.CreatedAt, SentAt: j.SentAt,
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

	var jobs []store.Job
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		// If filtering by device, verify the caller can see it first.
		if deviceFilter != nil {
			if _, err := getAuthorizedDevice(r, tx, auth.PermDevicesRead, *deviceFilter); err != nil {
				return err
			}
		}
		var err error
		jobs, err = store.ListJobs(ctx, tx, deviceFilter, 200)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]jobJSON, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, toJobJSON(j))
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
	writeJSON(w, http.StatusOK, out)
}

type dispatchReq struct {
	DeviceID   uuid.UUID       `json:"device_id"`
	Parameters json.RawMessage `json:"parameters"`
	TimeoutS   int             `json:"timeout_s"`
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
	if req.DeviceID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "device_id is required")
		return
	}
	if req.TimeoutS <= 0 {
		req.TimeoutS = 300
	}
	if req.Parameters == nil {
		req.Parameters = json.RawMessage("{}")
	}

	var jobID uuid.UUID
	var commandID string
	var deviceID uuid.UUID
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		sc, err := store.GetScript(ctx, tx, scriptID)
		if err != nil {
			return err
		}
		if sc.ArchivedAt != nil {
			return store.ErrNotFound
		}
		dev, err := getAuthorizedDevice(r, tx, auth.PermScriptsExecute, req.DeviceID)
		if err != nil {
			return err
		}
		if dev.Status != "active" {
			return store.ErrNotFound
		}
		deviceID = dev.ID
		jobID, commandID, err = store.CreateJob(ctx, tx,
			p.TenantID, scriptID, deviceID, p.UserID,
			req.TimeoutS, req.Parameters, sc.Body, sc.Language)
		if err != nil {
			return err
		}
		return recordAudit(ctx, tx, "job.dispatch", "job", jobID,
			map[string]any{"script_id": scriptID, "device_id": deviceID})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// Attempt immediate delivery if the device has an open connection.
	if s.Gateway != nil {
		if sent := s.Gateway.DispatchJob(ctx, p.TenantID, deviceID, jobID, commandID); sent {
			_ = s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
				return store.MarkJobSent(ctx, tx, jobID)
			})
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{"job_id": jobID})
}
