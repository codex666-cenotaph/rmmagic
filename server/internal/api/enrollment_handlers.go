package api

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

type enrollmentTokenJSON struct {
	ID        uuid.UUID  `json:"id"`
	SiteID    uuid.UUID  `json:"site_id"`
	SiteName  string     `json:"site_name"`
	ExpiresAt time.Time  `json:"expires_at"`
	MaxUses   int        `json:"max_uses"`
	UseCount  int        `json:"use_count"`
	RevokedAt *time.Time `json:"revoked_at"`
	CreatedAt time.Time  `json:"created_at"`
}

func (s *Server) handleListEnrollmentTokens(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)

	out := []enrollmentTokenJSON{}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		tokens, err := store.ListEnrollmentTokens(ctx, tx)
		if err != nil {
			return err
		}
		for _, t := range tokens {
			// Show only tokens whose site the caller can enroll into.
			if requireInTx(ctx, tx, auth.PermDevicesEnroll, auth.Scope{Type: auth.ScopeSite, ID: t.SiteID}) != nil {
				continue
			}
			out = append(out, enrollmentTokenJSON{t.ID, t.SiteID, t.SiteName,
				t.ExpiresAt, t.MaxUses, t.UseCount, t.RevokedAt, t.CreatedAt})
		}
		return nil
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

func (s *Server) handleCreateEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	var req struct {
		SiteID    uuid.UUID  `json:"site_id"`
		ExpiresAt *time.Time `json:"expires_at"`
		MaxUses   int        `json:"max_uses"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.MaxUses == 0 {
		req.MaxUses = 1
	}
	if req.MaxUses < 1 || req.MaxUses > 10000 {
		writeError(w, http.StatusBadRequest, "max_uses must be 1-10000")
		return
	}
	expires := time.Now().Add(24 * time.Hour)
	if req.ExpiresAt != nil {
		expires = *req.ExpiresAt
	}
	if expires.Before(time.Now()) || time.Until(expires) > 90*24*time.Hour {
		writeError(w, http.StatusBadRequest, "expires_at must be in the future (max 90 days)")
		return
	}

	token, hash, err := auth.NewEnrollmentToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var id uuid.UUID
	err = s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		// Site must exist in-tenant before its ID is used for authz.
		if _, err := store.GetSite(ctx, tx, req.SiteID); err != nil {
			return err
		}
		if err := requireInTx(ctx, tx, auth.PermDevicesEnroll, auth.Scope{Type: auth.ScopeSite, ID: req.SiteID}); err != nil {
			return err
		}
		var err error
		if id, err = store.CreateEnrollmentToken(ctx, tx, p.TenantID, req.SiteID,
			p.UserID, hash, expires, req.MaxUses); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "enrollment_token.create", "enrollment_token", id, map[string]any{
			"site_id": req.SiteID, "max_uses": req.MaxUses, "expires_at": expires,
		})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "token": token})
}

func (s *Server) handleRevokeEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		tokens, err := store.ListEnrollmentTokens(ctx, tx)
		if err != nil {
			return err
		}
		var siteID uuid.UUID
		found := false
		for _, t := range tokens {
			if t.ID == id {
				siteID, found = t.SiteID, true
				break
			}
		}
		if !found {
			return store.ErrNotFound
		}
		if err := requireInTx(ctx, tx, auth.PermDevicesEnroll, auth.Scope{Type: auth.ScopeSite, ID: siteID}); err != nil {
			return err
		}
		if err := store.RevokeEnrollmentToken(ctx, tx, id); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "enrollment_token.revoke", "enrollment_token", id, nil)
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
