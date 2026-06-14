package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

// packageNameRe constrains package names to what apt/dnf accept, keeping
// shell-unsafe input out of the agent's package-manager invocation. The
// agent passes names as argv (no shell), but validating here is defence in
// depth and surfaces typos as a 400.
var packageNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9+._-]*$`)

const maxPackagesPerJob = 50

type deployAppReq struct {
	// Operation is "install" or "remove".
	Operation string `json:"operation"`
	Packages  []string `json:"packages"`
	// DeviceID is the single-device shorthand for Target.
	DeviceID     uuid.UUID       `json:"device_id"`
	Target       store.JobTarget `json:"target"`
	TimeoutS     int             `json:"timeout_s"`
	ExpiresInS   int             `json:"expires_in_s"`
	ConfirmToken string          `json:"confirm_token"`
}

// handleDeployApp creates package install/remove jobs across a target,
// reusing the job dispatch/offline-queue/result machinery. Same
// blast-radius safeguard as script dispatch.
func (s *Server) handleDeployApp(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)

	var req deployAppReq
	if !decodeJSON(w, r, &req) {
		return
	}
	var kind string
	switch req.Operation {
	case "install":
		kind = "package_install"
	case "remove":
		kind = "package_remove"
	default:
		writeError(w, http.StatusBadRequest, "operation must be install or remove")
		return
	}
	if len(req.Packages) == 0 {
		writeError(w, http.StatusBadRequest, "at least one package is required")
		return
	}
	if len(req.Packages) > maxPackagesPerJob {
		writeError(w, http.StatusBadRequest, "too many packages in one job")
		return
	}
	for _, name := range req.Packages {
		if !packageNameRe.MatchString(name) {
			writeError(w, http.StatusBadRequest, "invalid package name: "+name)
			return
		}
	}
	if req.DeviceID != uuid.Nil {
		req.Target = store.JobTarget{DeviceIDs: []uuid.UUID{req.DeviceID}}
	}
	if err := req.Target.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.TimeoutS <= 0 {
		req.TimeoutS = 600
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
	expiresAt := time.Now().Add(time.Duration(req.ExpiresInS) * time.Second)
	spec, _ := json.Marshal(store.PackageSpecJSON{Packages: req.Packages})

	var jobs []createdJob
	confirmed := true
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		devices, err := resolveAuthorizedTarget(ctx, tx, req.Target, auth.PermAppsDeploy)
		if err != nil {
			return err
		}
		if len(devices) == 0 {
			return errNoTargetDevices
		}
		// Reuse the mass-action safeguard; the token binds to the target +
		// count (uuid.Nil stands in for "no script").
		if !s.requireBlastRadiusAck(w, p.TenantID, uuid.Nil, req.Target, len(devices), req.ConfirmToken) {
			confirmed = false
			return nil
		}
		for _, dev := range devices {
			jobID, commandID, err := store.CreatePackageJob(ctx, tx,
				p.TenantID, dev.ID, &p.UserID, nil, kind, req.TimeoutS, expiresAt, spec)
			if err != nil {
				return err
			}
			jobs = append(jobs, createdJob{JobID: jobID, DeviceID: dev.ID, CommandID: commandID})
		}
		return recordAudit(ctx, tx, "app.deploy", "device", uuid.Nil,
			map[string]any{"operation": req.Operation, "packages": req.Packages,
				"target": req.Target, "device_count": len(devices)})
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

	resp := map[string]any{"job_ids": jobIDs(jobs), "device_count": len(jobs)}
	if len(jobs) == 1 {
		resp["job_id"] = jobs[0].JobID
	}
	writeJSON(w, http.StatusCreated, resp)
}
