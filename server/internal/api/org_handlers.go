package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

// txResolver answers authz scope containment inside the current
// transaction (customer contains site).
type txResolver struct{ tx pgx.Tx }

func (r txResolver) Contains(ctx context.Context, outer, inner auth.Scope) (bool, error) {
	if outer.Type == auth.ScopeCustomer && inner.Type == auth.ScopeSite {
		return store.SiteBelongsToCustomer(ctx, r.tx, inner.ID, outer.ID)
	}
	return false, nil
}

// requireInTx is the fine-grained authorization check handlers run
// against the specific resource scope, inside the tenant transaction.
func requireInTx(ctx context.Context, tx pgx.Tx, perm auth.Permission, target auth.Scope) error {
	return auth.Require(ctx, txResolver{tx}, perm, target)
}

// recordAudit writes an audit entry attributed to the current principal.
func recordAudit(ctx context.Context, tx pgx.Tx, action, targetType string, targetID uuid.UUID, details map[string]any) error {
	p, _ := auth.PrincipalFrom(ctx)
	if p == nil {
		return nil
	}
	actorType := "user"
	actorID := p.UserID
	if p.APITokenID != nil {
		actorType = "api_token"
		actorID = *p.APITokenID
	}
	ip, _ := ctx.Value(ctxIP).(string)
	return store.InsertAudit(ctx, tx, p.TenantID, store.AuditEntry{
		ActorType: actorType, ActorID: &actorID, Action: action,
		TargetType: &targetType, TargetID: &targetID, IP: &ip,
		Details: mustJSON(details),
	})
}

func pathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return uuid.Nil, false
	}
	return id, true
}

// --- customers ---

type customerJSON struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Server) handleListCustomers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)

	var out []customerJSON
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		customers, err := store.ListCustomers(ctx, tx)
		if err != nil {
			return err
		}
		// Tenant-wide readers see everything; scoped principals see only
		// the customers their grants cover.
		allowed := map[uuid.UUID]bool{}
		all := p.HasTenantWide(auth.PermOrgRead)
		if !all {
			for _, id := range p.CustomerIDsWith(auth.PermOrgRead) {
				allowed[id] = true
			}
		}
		for _, c := range customers {
			if all || allowed[c.ID] {
				out = append(out, customerJSON{c.ID, c.Name, c.CreatedAt})
			}
		}
		return nil
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if out == nil {
		out = []customerJSON{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"customers": out})
}

func (s *Server) handleCreateCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 200 {
		writeError(w, http.StatusBadRequest, "name required (max 200 chars)")
		return
	}

	var c store.Customer
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		// Creating a customer is a tenant-level action.
		if err := requireInTx(ctx, tx, auth.PermOrgManage, auth.TenantScope()); err != nil {
			return err
		}
		var err error
		if c, err = store.CreateCustomer(ctx, tx, p.TenantID, req.Name); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "customer.create", "customer", c.ID, map[string]any{"name": c.Name})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, customerJSON{c.ID, c.Name, c.CreatedAt})
}

func (s *Server) handleRenameCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 200 {
		writeError(w, http.StatusBadRequest, "name required (max 200 chars)")
		return
	}

	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := requireInTx(ctx, tx, auth.PermOrgManage, auth.Scope{Type: auth.ScopeCustomer, ID: id}); err != nil {
			return err
		}
		if err := store.RenameCustomer(ctx, tx, id, req.Name); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "customer.update", "customer", id, map[string]any{"name": req.Name})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleDeleteCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}

	conflict := false
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := requireInTx(ctx, tx, auth.PermOrgManage, auth.Scope{Type: auth.ScopeCustomer, ID: id}); err != nil {
			return err
		}
		n, err := store.CustomerSiteCount(ctx, tx, id)
		if err != nil {
			return err
		}
		if n > 0 {
			conflict = true
			return nil
		}
		if err := store.DeleteCustomer(ctx, tx, id); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "customer.delete", "customer", id, nil)
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if conflict {
		writeError(w, http.StatusConflict, "customer still has sites")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- sites ---

type siteJSON struct {
	ID         uuid.UUID `json:"id"`
	CustomerID uuid.UUID `json:"customer_id"`
	Name       string    `json:"name"`
	Timezone   string    `json:"timezone"`
}

func (s *Server) handleListSites(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	customerID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}

	var out []siteJSON
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		// Resolve the ID under RLS first: a tenant-wide grant must not
		// vouch for another tenant's customer ID.
		if _, err := store.GetCustomer(ctx, tx, customerID); err != nil {
			return err
		}
		if err := requireInTx(ctx, tx, auth.PermOrgRead, auth.Scope{Type: auth.ScopeCustomer, ID: customerID}); err != nil {
			return err
		}
		sites, err := store.ListSites(ctx, tx, customerID)
		if err != nil {
			return err
		}
		for _, st := range sites {
			out = append(out, siteJSON{st.ID, st.CustomerID, st.Name, st.Timezone})
		}
		return nil
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if out == nil {
		out = []siteJSON{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": out})
}

func (s *Server) handleCreateSite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	customerID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Name     string `json:"name"`
		Timezone string `json:"timezone"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 200 {
		writeError(w, http.StatusBadRequest, "name required (max 200 chars)")
		return
	}
	if req.Timezone == "" {
		req.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		writeError(w, http.StatusBadRequest, "invalid timezone")
		return
	}

	var st store.Site
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if _, err := store.GetCustomer(ctx, tx, customerID); err != nil {
			return err
		}
		if err := requireInTx(ctx, tx, auth.PermOrgManage, auth.Scope{Type: auth.ScopeCustomer, ID: customerID}); err != nil {
			return err
		}
		var err error
		if st, err = store.CreateSite(ctx, tx, p.TenantID, customerID, req.Name, req.Timezone); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "site.create", "site", st.ID,
			map[string]any{"name": st.Name, "customer_id": customerID})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, siteJSON{st.ID, st.CustomerID, st.Name, st.Timezone})
}

func (s *Server) handleUpdateSite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Name     *string `json:"name"`
		Timezone *string `json:"timezone"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		st, err := store.GetSite(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := requireInTx(ctx, tx, auth.PermOrgManage, auth.Scope{Type: auth.ScopeCustomer, ID: st.CustomerID}); err != nil {
			return err
		}
		name, tz := st.Name, st.Timezone
		if req.Name != nil {
			name = strings.TrimSpace(*req.Name)
		}
		if req.Timezone != nil {
			tz = *req.Timezone
		}
		if name == "" || len(name) > 200 {
			return errValidation
		}
		if _, err := time.LoadLocation(tz); err != nil {
			return errValidation
		}
		if err := store.UpdateSite(ctx, tx, id, name, tz); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "site.update", "site", id,
			map[string]any{"name": name, "timezone": tz})
	})
	if err == errValidation {
		writeError(w, http.StatusBadRequest, "invalid name or timezone")
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleDeleteSite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}

	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		st, err := store.GetSite(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := requireInTx(ctx, tx, auth.PermOrgManage, auth.Scope{Type: auth.ScopeCustomer, ID: st.CustomerID}); err != nil {
			return err
		}
		if err := store.DeleteSite(ctx, tx, id); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "site.delete", "site", id, nil)
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
