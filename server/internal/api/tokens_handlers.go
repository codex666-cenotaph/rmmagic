package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

type apiTokenJSON struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Permissions []string   `json:"permissions"`
	ScopeType   string     `json:"scope_type"`
	ScopeID     *uuid.UUID `json:"scope_id"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (s *Server) handleListAPITokens(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)

	out := []apiTokenJSON{}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := requireInTx(ctx, tx, auth.PermTokensManage, auth.TenantScope()); err != nil {
			return err
		}
		tokens, err := store.ListAPITokens(ctx, tx)
		if err != nil {
			return err
		}
		for _, t := range tokens {
			out = append(out, apiTokenJSON{t.ID, t.Name, t.Permissions, t.ScopeType,
				t.ScopeID, t.LastUsedAt, t.ExpiresAt, t.RevokedAt, t.CreatedAt})
		}
		return nil
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

func (s *Server) handleCreateAPIToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	var req struct {
		Name        string     `json:"name"`
		Permissions []string   `json:"permissions"`
		ScopeType   string     `json:"scope_type"`
		ScopeID     *uuid.UUID `json:"scope_id"`
		ExpiresAt   *time.Time `json:"expires_at"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 200 || len(req.Permissions) == 0 {
		writeError(w, http.StatusBadRequest, "name and permissions required")
		return
	}
	if req.ScopeType == "" {
		req.ScopeType = string(auth.ScopeTenant)
	}
	st := auth.ScopeType(req.ScopeType)
	if st != auth.ScopeTenant && st != auth.ScopeCustomer && st != auth.ScopeSite {
		writeError(w, http.StatusBadRequest, "invalid scope_type")
		return
	}
	if (st == auth.ScopeTenant) != (req.ScopeID == nil) {
		writeError(w, http.StatusBadRequest, "scope_id required for customer/site scope only")
		return
	}
	if req.ExpiresAt != nil && req.ExpiresAt.Before(time.Now()) {
		writeError(w, http.StatusBadRequest, "expires_at is in the past")
		return
	}
	known := map[string]bool{}
	for _, perm := range auth.All() {
		known[string(perm)] = true
	}
	for _, perm := range req.Permissions {
		if !known[perm] {
			writeError(w, http.StatusBadRequest, "unknown permission: "+perm)
			return
		}
	}

	tokenScope := auth.Scope{Type: st}
	if req.ScopeID != nil {
		tokenScope.ID = *req.ScopeID
	}

	token, hash, err := auth.NewAPIToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var id uuid.UUID
	err = s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := requireInTx(ctx, tx, auth.PermTokensManage, auth.TenantScope()); err != nil {
			return err
		}
		// No privilege escalation: the caller must hold every requested
		// permission at the token's scope — a customer-scoped technician
		// cannot mint a tenant-wide token.
		for _, perm := range req.Permissions {
			if err := requireInTx(ctx, tx, auth.Permission(perm), tokenScope); err != nil {
				return err
			}
		}
		var err error
		if id, err = store.CreateAPIToken(ctx, tx, p.TenantID, p.UserID, req.Name,
			hash, req.Permissions, req.ScopeType, req.ScopeID, req.ExpiresAt); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "api_token.create", "api_token", id, map[string]any{
			"name": req.Name, "permissions": req.Permissions,
			"scope_type": req.ScopeType, "scope_id": req.ScopeID,
		})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "token": token})
}

func (s *Server) handleRevokeAPIToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := requireInTx(ctx, tx, auth.PermTokensManage, auth.TenantScope()); err != nil {
			return err
		}
		if err := store.RevokeAPIToken(ctx, tx, id); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "api_token.revoke", "api_token", id, nil)
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
