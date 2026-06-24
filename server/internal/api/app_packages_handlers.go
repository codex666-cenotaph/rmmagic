package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

// appPackageReq is the create/update body for a centrally-managed app
// package. Packages are the install spec (forwarded to the agent);
// DetectionNames are the package names whose presence in inventory proves
// the app is installed — empty means "detect by the install package names"
// (i.e. by app name).
type appPackageReq struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	OS             string   `json:"os"`
	Packages       []string `json:"packages"`
	DetectionNames []string `json:"detection_names"`
	TimeoutS       int      `json:"timeout_s"`
}

// validateAppPackageReq checks the request and returns the install and
// detection JSON payloads to store, plus an error message (empty on
// success).
func validateAppPackageReq(req *appPackageReq) (install, detection json.RawMessage, msg string) {
	if req.Name == "" || len(req.Name) > 120 {
		return nil, nil, "name required (max 120 chars)"
	}
	switch req.OS {
	case "linux", "windows", "darwin":
	default:
		return nil, nil, "os must be linux, windows, or darwin"
	}
	if len(req.Packages) == 0 {
		return nil, nil, "at least one package is required"
	}
	if len(req.Packages) > maxPackagesPerJob {
		return nil, nil, "too many packages"
	}
	for _, name := range req.Packages {
		if !packageNameRe.MatchString(name) {
			return nil, nil, "invalid package name: " + name
		}
	}
	if len(req.DetectionNames) > maxPackagesPerJob {
		return nil, nil, "too many detection names"
	}
	for _, name := range req.DetectionNames {
		if !packageNameRe.MatchString(name) {
			return nil, nil, "invalid detection name: " + name
		}
	}
	names := req.DetectionNames
	if names == nil {
		names = []string{}
	}
	install, _ = json.Marshal(store.PackageSpecJSON{Packages: req.Packages})
	detection, _ = json.Marshal(store.AppDetection{Method: "package_name", Names: names})
	return install, detection, ""
}

func (s *Server) handleListAppPackages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	includeArchived := r.URL.Query().Get("archived") == "true"
	var pkgs []store.AppPackage
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		pkgs, err = store.ListAppPackages(ctx, tx, includeArchived)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"packages": pkgs})
}

func (s *Server) handleGetAppPackage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var pkg store.AppPackage
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		pkg, err = store.GetAppPackage(ctx, tx, id)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pkg)
}

func (s *Server) handleCreateAppPackage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	var req appPackageReq
	if !decodeJSON(w, r, &req) {
		return
	}
	install, detection, msg := validateAppPackageReq(&req)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if req.TimeoutS <= 0 {
		req.TimeoutS = 600
	}
	if req.TimeoutS > 86400 {
		writeError(w, http.StatusBadRequest, "timeout_s must be at most 86400")
		return
	}
	pkg := store.AppPackage{
		Name: req.Name, Description: req.Description, OS: req.OS,
		Install: install, Detection: detection, TimeoutS: req.TimeoutS,
	}
	var id uuid.UUID
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		id, err = store.CreateAppPackage(ctx, tx, p.TenantID, pkg, &p.UserID)
		if err != nil {
			return err
		}
		return recordAudit(ctx, tx, "app_package.create", "app_package", id,
			map[string]any{"name": req.Name, "os": req.OS, "packages": req.Packages})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) handleUpdateAppPackage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req appPackageReq
	if !decodeJSON(w, r, &req) {
		return
	}
	install, detection, msg := validateAppPackageReq(&req)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if req.TimeoutS <= 0 {
		req.TimeoutS = 600
	}
	if req.TimeoutS > 86400 {
		writeError(w, http.StatusBadRequest, "timeout_s must be at most 86400")
		return
	}
	pkg := store.AppPackage{
		ID: id, Name: req.Name, Description: req.Description, OS: req.OS,
		Install: install, Detection: detection, TimeoutS: req.TimeoutS,
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := store.UpdateAppPackage(ctx, tx, pkg); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "app_package.update", "app_package", id,
			map[string]any{"name": req.Name, "os": req.OS})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleArchiveAppPackage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := store.ArchiveAppPackage(ctx, tx, id); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "app_package.archive", "app_package", id, map[string]any{})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
