package api

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

// deploymentRuleReq is the create/update body for a deployment rule.
type deploymentRuleReq struct {
	PackageID uuid.UUID  `json:"package_id"`
	Name      string     `json:"name"`
	ScopeType string     `json:"scope_type"`
	ScopeID   *uuid.UUID `json:"scope_id"`
	Filters   struct {
		Tags          []string `json:"tags"`
		TagsMatch     string   `json:"tags_match"`
		HostnameRegex string   `json:"hostname_regex"`
	} `json:"filters"`
	Enabled bool `json:"enabled"`
}

const maxHostnameRegex = 256

// validateDeploymentRuleReq checks the request and returns the normalized
// filters JSON plus an error message (empty on success).
func validateDeploymentRuleReq(req *deploymentRuleReq) (json.RawMessage, string) {
	if req.Name == "" || len(req.Name) > 120 {
		return nil, "name required (max 120 chars)"
	}
	if req.PackageID == uuid.Nil {
		return nil, "package_id required"
	}
	switch req.ScopeType {
	case "tenant":
		if req.ScopeID != nil {
			return nil, "scope_id must be omitted for a tenant scope"
		}
	case "customer", "site", "device":
		if req.ScopeID == nil {
			return nil, "scope_id required for non-tenant scope"
		}
	default:
		return nil, "scope_type must be tenant, customer, site, or device"
	}

	var f store.DeploymentFilters
	if len(req.Filters.Tags) > 0 {
		tags, msg := normalizeTags(req.Filters.Tags)
		if msg != "" {
			return nil, msg
		}
		f.Tags = tags
	}
	switch req.Filters.TagsMatch {
	case "", "any":
		f.TagsMatch = "any"
	case "all":
		f.TagsMatch = "all"
	default:
		return nil, "tags_match must be any or all"
	}
	if req.Filters.HostnameRegex != "" {
		if len(req.Filters.HostnameRegex) > maxHostnameRegex {
			return nil, "hostname_regex too long"
		}
		if _, err := regexp.Compile(req.Filters.HostnameRegex); err != nil {
			return nil, "invalid hostname_regex: " + err.Error()
		}
		f.HostnameRegex = req.Filters.HostnameRegex
	}
	raw, _ := json.Marshal(f)
	return raw, ""
}

func (s *Server) handleListDeploymentRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	var rules []store.DeploymentRule
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		rules, err = store.ListDeploymentRules(ctx, tx)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

func (s *Server) handleGetDeploymentRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var rule store.DeploymentRule
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		rule, err = store.GetDeploymentRule(ctx, tx, id)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) handleCreateDeploymentRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	var req deploymentRuleReq
	if !decodeJSON(w, r, &req) {
		return
	}
	filters, msg := validateDeploymentRuleReq(&req)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	rule := store.DeploymentRule{
		PackageID: req.PackageID, Name: req.Name, ScopeType: req.ScopeType,
		ScopeID: req.ScopeID, Filters: filters, Enabled: req.Enabled,
	}
	var id uuid.UUID
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		// Confirm the package exists in this tenant before binding to it,
		// so a bad package_id is a clean 404 rather than an FK 500.
		if _, err := store.GetAppPackage(ctx, tx, req.PackageID); err != nil {
			return err
		}
		var err error
		id, err = store.CreateDeploymentRule(ctx, tx, p.TenantID, rule, &p.UserID)
		if err != nil {
			return err
		}
		return recordAudit(ctx, tx, "deployment_rule.create", "deployment_rule", id,
			map[string]any{"name": req.Name, "package_id": req.PackageID, "scope_type": req.ScopeType})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) handleUpdateDeploymentRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req deploymentRuleReq
	if !decodeJSON(w, r, &req) {
		return
	}
	filters, msg := validateDeploymentRuleReq(&req)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	rule := store.DeploymentRule{
		ID: id, PackageID: req.PackageID, Name: req.Name, ScopeType: req.ScopeType,
		ScopeID: req.ScopeID, Filters: filters, Enabled: req.Enabled,
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if _, err := store.GetAppPackage(ctx, tx, req.PackageID); err != nil {
			return err
		}
		if err := store.UpdateDeploymentRule(ctx, tx, rule); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "deployment_rule.update", "deployment_rule", id,
			map[string]any{"name": req.Name, "enabled": req.Enabled})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteDeploymentRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := store.DeleteDeploymentRule(ctx, tx, id); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "deployment_rule.delete", "deployment_rule", id, map[string]any{})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
