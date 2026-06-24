package api

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

// assistantAD is the secrets.Box associated-data context for sealing a
// tenant's assistant API key, binding the ciphertext to the tenant so a
// sealed key can't be reused under another.
func assistantAD(tenantID uuid.UUID) []byte {
	return append([]byte("assistant-api-key"), tenantID[:]...)
}

// assistantSettingsJSON is the safe, outward view: it never includes the
// API key, only whether one is stored.
type assistantSettingsJSON struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	KeySet   bool   `json:"key_set"`
}

func (s *Server) handleGetAssistantSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)

	out := assistantSettingsJSON{Provider: providerAnthropic}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := requireInTx(ctx, tx, auth.PermTenantManage, auth.TenantScope()); err != nil {
			return err
		}
		settings, found, err := store.GetAssistantSettings(ctx, tx)
		if err != nil {
			return err
		}
		if found {
			out = assistantSettingsJSON{
				Enabled:  settings.Enabled,
				Provider: settings.Provider,
				Model:    settings.Model,
				KeySet:   len(settings.APIKeyEnc) > 0,
			}
		}
		return nil
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleUpdateAssistantSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)

	var req struct {
		Enabled  bool   `json:"enabled"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
		// APIKey, when non-nil, replaces the stored key; nil/omitted keeps it.
		APIKey *string `json:"api_key"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Provider = strings.TrimSpace(req.Provider)
	if req.Provider != providerAnthropic && req.Provider != providerMistral {
		writeError(w, http.StatusBadRequest, "provider must be anthropic or mistral")
		return
	}
	req.Model = strings.TrimSpace(req.Model)

	var sealed []byte
	if req.APIKey != nil {
		key := strings.TrimSpace(*req.APIKey)
		if key != "" {
			var err error
			sealed, err = s.Box.Seal([]byte(key), assistantAD(p.TenantID))
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal error")
				return
			}
		}
	}

	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if err := requireInTx(ctx, tx, auth.PermTenantManage, auth.TenantScope()); err != nil {
			return err
		}
		if err := store.UpsertAssistantSettings(ctx, tx, p.TenantID,
			req.Enabled, req.Provider, req.Model, sealed); err != nil {
			return err
		}
		return recordAudit(ctx, tx, "assistant.settings", "tenant", p.TenantID, map[string]any{
			"enabled": req.Enabled, "provider": req.Provider, "model": req.Model,
			"key_updated": sealed != nil,
		})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
