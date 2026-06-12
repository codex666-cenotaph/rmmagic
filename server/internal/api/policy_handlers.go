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
	var req struct {
		Name       string          `json:"name"`
		ScopeType  string          `json:"scope_type"`
		ScopeID    *uuid.UUID      `json:"scope_id"`
		Enabled    bool            `json:"enabled"`
		Rules      json.RawMessage `json:"rules"`
		ChannelIDs []uuid.UUID     `json:"channel_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || len(req.Name) > 120 {
		writeError(w, http.StatusBadRequest, "name required (max 120 chars)")
		return
	}
	if req.ScopeType != "tenant" && req.ScopeType != "customer" && req.ScopeType != "site" && req.ScopeType != "device" {
		writeError(w, http.StatusBadRequest, "scope_type must be tenant, customer, site, or device")
		return
	}
	if req.ScopeType != "tenant" && req.ScopeID == nil {
		writeError(w, http.StatusBadRequest, "scope_id required for non-tenant scope")
		return
	}
	var rules alerts.Rules
	if err := json.Unmarshal(req.Rules, &rules); err != nil {
		writeError(w, http.StatusBadRequest, "invalid rules: "+err.Error())
		return
	}
	if err := rules.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid rules: "+err.Error())
		return
	}
	if req.ChannelIDs == nil {
		req.ChannelIDs = []uuid.UUID{}
	}

	pol := store.Policy{
		Name: req.Name, ScopeType: req.ScopeType, ScopeID: req.ScopeID,
		Enabled: req.Enabled, Rules: req.Rules, ChannelIDs: req.ChannelIDs,
	}
	var id uuid.UUID
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		id, err = store.CreatePolicy(ctx, tx, p.TenantID, pol, &p.UserID)
		return err
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
	var req struct {
		Name       string          `json:"name"`
		ScopeType  string          `json:"scope_type"`
		ScopeID    *uuid.UUID      `json:"scope_id"`
		Enabled    bool            `json:"enabled"`
		Rules      json.RawMessage `json:"rules"`
		ChannelIDs []uuid.UUID     `json:"channel_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || len(req.Name) > 120 {
		writeError(w, http.StatusBadRequest, "name required (max 120 chars)")
		return
	}
	if req.ScopeType != "tenant" && req.ScopeType != "customer" && req.ScopeType != "site" && req.ScopeType != "device" {
		writeError(w, http.StatusBadRequest, "scope_type must be tenant, customer, site, or device")
		return
	}
	if req.ScopeType != "tenant" && req.ScopeID == nil {
		writeError(w, http.StatusBadRequest, "scope_id required for non-tenant scope")
		return
	}
	var rules alerts.Rules
	if err := json.Unmarshal(req.Rules, &rules); err != nil {
		writeError(w, http.StatusBadRequest, "invalid rules: "+err.Error())
		return
	}
	if err := rules.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid rules: "+err.Error())
		return
	}
	if req.ChannelIDs == nil {
		req.ChannelIDs = []uuid.UUID{}
	}

	pol := store.Policy{
		ID: id, Name: req.Name, ScopeType: req.ScopeType, ScopeID: req.ScopeID,
		Enabled: req.Enabled, Rules: req.Rules, ChannelIDs: req.ChannelIDs,
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		return store.UpdatePolicy(ctx, tx, pol)
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
		return store.DeletePolicy(ctx, tx, id)
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
