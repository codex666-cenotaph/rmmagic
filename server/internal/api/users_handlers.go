package api

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

var errValidation = errors.New("validation failed")

type assignmentJSON struct {
	ID        uuid.UUID  `json:"id"`
	RoleID    uuid.UUID  `json:"role_id"`
	RoleName  string     `json:"role_name"`
	ScopeType string     `json:"scope_type"`
	ScopeID   *uuid.UUID `json:"scope_id"`
}

type userJSON struct {
	ID          uuid.UUID        `json:"id"`
	Email       string           `json:"email"`
	Status      string           `json:"status"`
	MFAEnabled  bool             `json:"mfa_enabled"`
	Assignments []assignmentJSON `json:"assignments"`
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)

	var out []userJSON
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := requireInTx(ctx, tx, auth.PermUsersRead, auth.TenantScope()); err != nil {
			return err
		}
		users, err := store.ListUsers(ctx, tx)
		if err != nil {
			return err
		}
		assignments, err := store.ListAssignmentsForTenant(ctx, tx)
		if err != nil {
			return err
		}
		byUser := map[uuid.UUID][]assignmentJSON{}
		for _, a := range assignments {
			byUser[a.UserID] = append(byUser[a.UserID], assignmentJSON{
				ID: a.ID, RoleID: a.RoleID, RoleName: a.RoleName,
				ScopeType: a.ScopeType, ScopeID: a.ScopeID,
			})
		}
		for _, u := range users {
			uj := userJSON{ID: u.ID, Email: u.Email, Status: u.Status,
				MFAEnabled: u.MFAEnabled, Assignments: byUser[u.ID]}
			if uj.Assignments == nil {
				uj.Assignments = []assignmentJSON{}
			}
			out = append(out, uj)
		}
		return nil
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if out == nil {
		out = []userJSON{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}
	if len(req.Password) < 12 {
		writeError(w, http.StatusBadRequest, "password must be at least 12 characters")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var id uuid.UUID
	err = s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := requireInTx(ctx, tx, auth.PermUsersManage, auth.TenantScope()); err != nil {
			return err
		}
		var err error
		if id, err = store.CreateUser(ctx, tx, p.TenantID, req.Email, hash); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "user.create", "user", id, map[string]any{"email": req.Email})
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "email already in use")
			return
		}
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "email": req.Email})
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Status != "active" && req.Status != "disabled" {
		writeError(w, http.StatusBadRequest, "status must be active or disabled")
		return
	}
	if id == p.UserID && req.Status == "disabled" {
		writeError(w, http.StatusBadRequest, "cannot disable your own account")
		return
	}

	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := requireInTx(ctx, tx, auth.PermUsersManage, auth.TenantScope()); err != nil {
			return err
		}
		if err := store.SetUserStatus(ctx, tx, id, req.Status); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "user.update", "user", id, map[string]any{"status": req.Status})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleListRoles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)

	type roleJSON struct {
		ID          uuid.UUID `json:"id"`
		Name        string    `json:"name"`
		Permissions []string  `json:"permissions"`
		IsBuiltin   bool      `json:"is_builtin"`
	}
	out := []roleJSON{}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := requireInTx(ctx, tx, auth.PermUsersRead, auth.TenantScope()); err != nil {
			return err
		}
		roles, err := store.ListRoles(ctx, tx)
		if err != nil {
			return err
		}
		for _, role := range roles {
			out = append(out, roleJSON{role.ID, role.Name, role.Permissions, role.IsBuiltin})
		}
		return nil
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": out})
}

func (s *Server) handleCreateAssignment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	userID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		RoleID    uuid.UUID  `json:"role_id"`
		ScopeType string     `json:"scope_type"`
		ScopeID   *uuid.UUID `json:"scope_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
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

	var id uuid.UUID
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := requireInTx(ctx, tx, auth.PermUsersManage, auth.TenantScope()); err != nil {
			return err
		}
		// Referenced role, user, and scope must all exist in this tenant
		// (RLS guarantees tenant locality; we check existence for clean
		// errors and to validate the scope reference).
		if _, err := store.GetRole(ctx, tx, req.RoleID); err != nil {
			return err
		}
		if _, err := store.GetUser(ctx, tx, userID); err != nil {
			return err
		}
		if st == auth.ScopeSite {
			if _, err := store.GetSite(ctx, tx, *req.ScopeID); err != nil {
				return err
			}
		}
		if st == auth.ScopeCustomer {
			customers, err := store.ListCustomers(ctx, tx)
			if err != nil {
				return err
			}
			found := false
			for _, c := range customers {
				if c.ID == *req.ScopeID {
					found = true
					break
				}
			}
			if !found {
				return store.ErrNotFound
			}
		}
		var err error
		if id, err = store.CreateAssignment(ctx, tx, p.TenantID, userID, req.RoleID, req.ScopeType, req.ScopeID); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "role_assignment.create", "user", userID, map[string]any{
			"role_id": req.RoleID, "scope_type": req.ScopeType, "scope_id": req.ScopeID,
		})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) handleDeleteAssignment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := requireInTx(ctx, tx, auth.PermUsersManage, auth.TenantScope()); err != nil {
			return err
		}
		if err := store.DeleteAssignment(ctx, tx, id); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "role_assignment.delete", "role_assignment", id, nil)
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
