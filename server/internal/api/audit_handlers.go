package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeError(w, http.StatusBadRequest, "limit must be 1-200")
			return
		}
		limit = n
	}
	before := time.Now().Add(time.Second)
	if v := r.URL.Query().Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "before must be RFC3339")
			return
		}
		before = t
	}

	type entryJSON struct {
		ID         uuid.UUID       `json:"id"`
		ActorType  string          `json:"actor_type"`
		ActorID    *uuid.UUID      `json:"actor_id"`
		Action     string          `json:"action"`
		TargetType *string         `json:"target_type"`
		TargetID   *uuid.UUID      `json:"target_id"`
		IP         *string         `json:"ip"`
		Details    json.RawMessage `json:"details"`
		CreatedAt  time.Time       `json:"created_at"`
	}
	out := []entryJSON{}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := requireInTx(ctx, tx, auth.PermAuditRead, auth.TenantScope()); err != nil {
			return err
		}
		entries, err := store.ListAudit(ctx, tx, before, limit)
		if err != nil {
			return err
		}
		for _, e := range entries {
			out = append(out, entryJSON{e.ID, e.ActorType, e.ActorID, e.Action,
				e.TargetType, e.TargetID, e.IP, e.Details, e.CreatedAt})
		}
		return nil
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}
