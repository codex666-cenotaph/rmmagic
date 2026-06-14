package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/alerts"
	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

// policyRequest is the create/update body for a monitoring policy.
type policyRequest struct {
	Name       string          `json:"name"`
	ScopeType  string          `json:"scope_type"`
	ScopeID    *uuid.UUID      `json:"scope_id"`
	ScopeTag   string          `json:"scope_tag"`
	Enabled    bool            `json:"enabled"`
	Rules      json.RawMessage `json:"rules"`
	ChannelIDs []uuid.UUID     `json:"channel_ids"`
}

// validatePolicyReq checks the request, normalizes a tag scope, and
// defaults the channel list in place. It returns the cleaned scope_tag
// (nil unless the scope is "tag") and an error message, empty on
// success.
func validatePolicyReq(req *policyRequest) (*string, string) {
	if req.Name == "" || len(req.Name) > 120 {
		return nil, "name required (max 120 chars)"
	}
	switch req.ScopeType {
	case "tenant", "customer", "site", "device", "tag":
	default:
		return nil, "scope_type must be tenant, customer, site, device, or tag"
	}
	var scopeTag *string
	switch req.ScopeType {
	case "tag":
		if req.ScopeID != nil {
			return nil, "scope_id must be omitted for a tag scope"
		}
		tags, msg := normalizeTags([]string{req.ScopeTag})
		if msg != "" {
			return nil, msg
		}
		if len(tags) == 0 {
			return nil, "scope_tag required for a tag scope"
		}
		scopeTag = &tags[0]
	case "tenant":
		if req.ScopeID != nil {
			return nil, "scope_id must be omitted for a tenant scope"
		}
	default: // customer, site, device
		if req.ScopeID == nil {
			return nil, "scope_id required for non-tenant scope"
		}
	}
	var rules alerts.Rules
	if err := json.Unmarshal(req.Rules, &rules); err != nil {
		return nil, "invalid rules: " + err.Error()
	}
	if err := rules.Validate(); err != nil {
		return nil, "invalid rules: " + err.Error()
	}
	if req.ChannelIDs == nil {
		req.ChannelIDs = []uuid.UUID{}
	}
	return scopeTag, ""
}

func (s *Server) handleListPolicies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	var policies []store.Policy
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		policies, err = store.ListPolicies(ctx, tx)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policies": policies})
}

func (s *Server) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var pol store.Policy
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		pol, err = store.GetPolicy(ctx, tx, id)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pol)
}

func (s *Server) handleCreatePolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	var req policyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	scopeTag, msg := validatePolicyReq(&req)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	pol := store.Policy{
		Name: req.Name, ScopeType: req.ScopeType, ScopeID: req.ScopeID, ScopeTag: scopeTag,
		Enabled: req.Enabled, Rules: req.Rules, ChannelIDs: req.ChannelIDs,
	}
	var id uuid.UUID
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		id, err = store.CreatePolicy(ctx, tx, p.TenantID, pol, &p.UserID)
		if err != nil {
			return err
		}
		return recordAudit(ctx, tx, "policy.create", "policy", id,
			map[string]any{"name": req.Name, "scope_type": req.ScopeType})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) handleUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req policyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	scopeTag, msg := validatePolicyReq(&req)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	pol := store.Policy{
		ID: id, Name: req.Name, ScopeType: req.ScopeType, ScopeID: req.ScopeID, ScopeTag: scopeTag,
		Enabled: req.Enabled, Rules: req.Rules, ChannelIDs: req.ChannelIDs,
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := store.UpdatePolicy(ctx, tx, pol); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "policy.update", "policy", id,
			map[string]any{"name": req.Name, "enabled": req.Enabled})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeletePolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := store.DeletePolicy(ctx, tx, id); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "policy.delete", "policy", id, map[string]any{})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
