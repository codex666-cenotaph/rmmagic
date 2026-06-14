package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

// channelJSON is the snake_case wire shape the dashboard expects. Using a
// DTO (rather than tagging the store struct) also guarantees the sealed
// webhook secret (NotificationChannel.SecretEnc) is never serialized.
type channelJSON struct {
	ID        uuid.UUID       `json:"id"`
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Config    json.RawMessage `json:"config"`
	CreatedAt time.Time       `json:"created_at"`
}

func toChannelJSON(c store.NotificationChannel) channelJSON {
	cfg := c.Config
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	return channelJSON{ID: c.ID, Name: c.Name, Type: c.Type, Config: cfg, CreatedAt: c.CreatedAt}
}

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
	out := make([]channelJSON, 0, len(channels))
	for _, c := range channels {
		out = append(out, toChannelJSON(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
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
		// Deliveries are always HMAC-signed; a webhook channel without a
		// secret could never deliver.
		if len(req.Secret) < 16 || len(req.Secret) > 200 {
			writeError(w, http.StatusBadRequest, "webhook channels require a signing secret (16-200 chars)")
			return
		}
	}

	id := uuid.New()
	var secretEnc []byte
	if req.Type == "webhook" {
		var err error
		secretEnc, err = s.Box.Seal([]byte(req.Secret), id[:])
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := store.CreateChannel(ctx, tx, p.TenantID, id, req.Name, req.Type, req.Config, secretEnc, &p.UserID); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "channel.create", "channel", id,
			map[string]any{"name": req.Name, "type": req.Type})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Name   string          `json:"name"`
		Type   string          `json:"type"`
		Config json.RawMessage `json:"config"`
		Secret string          `json:"secret"`
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
		if req.Secret != "" && (len(req.Secret) < 16 || len(req.Secret) > 200) {
			writeError(w, http.StatusBadRequest, "webhook signing secret must be 16-200 chars (or empty to keep current)")
			return
		}
	}

	var secretEnc []byte
	if req.Type == "webhook" && req.Secret != "" {
		var err error
		secretEnc, err = s.Box.Seal([]byte(req.Secret), id[:])
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := store.UpdateChannel(ctx, tx, id, req.Name, req.Type, req.Config, secretEnc); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "channel.update", "channel", id,
			map[string]any{"name": req.Name, "type": req.Type})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := store.DeleteChannel(ctx, tx, id); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "channel.delete", "channel", id, map[string]any{})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
