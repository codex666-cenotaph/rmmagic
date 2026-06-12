package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	var channels []store.NotificationChannel
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		channels, err = store.ListChannels(ctx, tx)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": channels})
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	var req struct {
		Name   string          `json:"name"`
		Type   string          `json:"type"`
		Config json.RawMessage `json:"config"`
		Secret string          `json:"secret"` // webhook signing secret; empty for email
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || len(req.Name) > 120 {
		writeError(w, http.StatusBadRequest, "name required (max 120 chars)")
		return
	}
	if req.Type != "email" && req.Type != "webhook" {
		writeError(w, http.StatusBadRequest, "type must be email or webhook")
		return
	}
	if len(req.Config) == 0 {
		req.Config = json.RawMessage("{}")
	}

	// Validate config shape.
	switch req.Type {
	case "email":
		var cfg struct {
			Recipients []string `json:"recipients"`
		}
		if err := json.Unmarshal(req.Config, &cfg); err != nil || len(cfg.Recipients) == 0 {
			writeError(w, http.StatusBadRequest, "email config requires at least one recipient")
			return
		}
	case "webhook":
		var cfg struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(req.Config, &cfg); err != nil || cfg.URL == "" {
			writeError(w, http.StatusBadRequest, "webhook config requires url")
			return
		}
	}

	id := uuid.New()
	var secretEnc []byte
	if req.Type == "webhook" && req.Secret != "" && s.Box != nil {
		var err error
		secretEnc, err = s.Box.Seal([]byte(req.Secret), id[:])
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		return store.CreateChannel(ctx, tx, p.TenantID, id, req.Name, req.Type, req.Config, secretEnc, &p.UserID)
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		return store.DeleteChannel(ctx, tx, id)
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
