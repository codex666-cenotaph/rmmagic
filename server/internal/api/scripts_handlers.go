package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

var validLanguages = map[string]bool{
	"bash": true, "powershell": true, "python": true, "batch": true,
}

type scriptJSON struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Language    string          `json:"language"`
	Body        string          `json:"body"`
	Parameters  json.RawMessage `json:"parameters"`
	Version     int             `json:"version"`
	Archived    bool            `json:"archived"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

func toScriptJSON(s store.Script) scriptJSON {
	params := s.Parameters
	if params == nil {
		params = json.RawMessage("[]")
	}
	return scriptJSON{
		ID:          s.ID,
		Name:        s.Name,
		Description: s.Description,
		Language:    s.Language,
		Body:        s.Body,
		Parameters:  params,
		Version:     s.Version,
		Archived:    s.ArchivedAt != nil,
		CreatedAt:   s.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   s.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func (s *Server) handleListScripts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	archived := r.URL.Query().Get("archived") == "true"
	var scripts []store.Script
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		scripts, err = store.ListScripts(ctx, tx, archived)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]scriptJSON, 0, len(scripts))
	for _, sc := range scripts {
		out = append(out, toScriptJSON(sc))
	}
	writeJSON(w, http.StatusOK, map[string]any{"scripts": out})
}

func (s *Server) handleGetScript(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var sc store.Script
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		sc, err = store.GetScript(ctx, tx, id)
		return err
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toScriptJSON(sc))
}

type scriptBody struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Language    string          `json:"language"`
	Body        string          `json:"body"`
	Parameters  json.RawMessage `json:"parameters"`
}

func (b *scriptBody) validate() string {
	if strings.TrimSpace(b.Name) == "" {
		return "name is required"
	}
	if !validLanguages[b.Language] {
		return "language must be bash, powershell, python, or batch"
	}
	if strings.TrimSpace(b.Body) == "" {
		return "body is required"
	}
	if b.Parameters == nil {
		b.Parameters = json.RawMessage("[]")
	}
	var tmp any
	if err := json.Unmarshal(b.Parameters, &tmp); err != nil {
		return "parameters must be a JSON array"
	}
	return ""
}

func (s *Server) handleCreateScript(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	var req scriptBody
	if !decodeJSON(w, r, &req) {
		return
	}
	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var id uuid.UUID
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var err error
		id, err = store.CreateScript(ctx, tx, p.TenantID, p.UserID,
			req.Name, req.Description, req.Language, req.Body, req.Parameters)
		if err != nil {
			return err
		}
		return recordAudit(ctx, tx, "script.create", "script", id, map[string]any{"name": req.Name})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) handleUpdateScript(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req scriptBody
	if !decodeJSON(w, r, &req) {
		return
	}
	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := store.UpdateScript(ctx, tx, id,
			req.Name, req.Description, req.Language, req.Body, req.Parameters); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "script.update", "script", id, map[string]any{"name": req.Name})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleArchiveScript(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := store.ArchiveScript(ctx, tx, id); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "script.archive", "script", id, nil)
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}
