package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

// maxAssistantTurns bounds the agentic loop so a misbehaving model can't
// spin forever; each turn is one model call plus any tool executions.
const maxAssistantTurns = 8

const assistantStepLimitMsg = "I wasn't able to finish that within the allowed number of steps. Please narrow the request and try again."

// assistantChatReq is the request body: the running conversation as a flat
// list of user/assistant text turns. The client keeps the history; the
// server is stateless.
type assistantChatReq struct {
	Messages []assistantMessage `json:"messages"`
}

type assistantMessage struct {
	Role    string `json:"role"`    // "user" | "assistant"
	Content string `json:"content"` // plain text
}

// assistantToolCall records one tool the assistant invoked, returned to
// the UI for transparency.
type assistantToolCall struct {
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// resolveProvider determines the effective assistant configuration for the
// tenant: per-tenant DB settings take precedence, falling back to the
// environment-configured default. It returns (nil, nil) when the assistant
// is not configured/enabled for this tenant.
func (s *Server) resolveProvider(ctx context.Context, p *auth.Principal) (provider, error) {
	var (
		settings store.AssistantSettings
		found    bool
	)
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var e error
		settings, found, e = store.GetAssistantSettings(ctx, tx)
		return e
	})
	if err != nil {
		return nil, err
	}

	if found && settings.Enabled {
		apiKey := ""
		if len(settings.APIKeyEnc) > 0 {
			plain, err := s.Box.Open(settings.APIKeyEnc, assistantAD(p.TenantID))
			if err != nil {
				return nil, err
			}
			apiKey = string(plain)
		}
		if apiKey == "" {
			return nil, nil
		}
		return newProvider(settings.Provider, apiKey, settings.Model)
	}

	// Environment fallback (Anthropic key from RMM_ANTHROPIC_API_KEY).
	if s.Assistant != nil && s.Assistant.APIKey != "" {
		return newProvider(s.Assistant.Provider, s.Assistant.APIKey, s.Assistant.Model)
	}
	return nil, nil
}

func (s *Server) handleAssistantChat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)

	prov, err := s.resolveProvider(ctx, p)
	if err != nil {
		s.Log.Error("assistant provider setup failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if prov == nil {
		writeError(w, http.StatusServiceUnavailable, "assistant is not configured")
		return
	}

	var req assistantChatReq
	if !decodeJSON(w, r, &req) {
		return
	}
	hasContent := false
	for _, m := range req.Messages {
		if m.Content != "" {
			hasContent = true
			break
		}
	}
	if !hasContent {
		writeError(w, http.StatusBadRequest, "messages required")
		return
	}

	// Tool set + executor bound to this user's principal.
	tools := assistantTools()
	byName := make(map[string]assistantTool, len(tools))
	for _, t := range tools {
		byName[t.def.name] = t
	}
	exec := func(ctx context.Context, name string, input json.RawMessage) (string, bool) {
		t, ok := byName[name]
		if !ok {
			return "unknown tool: " + name, true
		}
		var args map[string]any
		if len(input) > 0 {
			_ = json.Unmarshal(input, &args)
		}
		if args == nil {
			args = map[string]any{}
		}
		method, path, body, err := t.build(args)
		if err != nil {
			return err.Error(), true
		}
		status, respBody := s.executeInternal(ctx, p, method, path, body)
		if status < 200 || status >= 300 {
			return string(respBody), status >= 400
		}
		return string(respBody), false
	}

	reply, calls, err := prov.chat(ctx, assistantSystemPrompt, tools, req.Messages, exec)
	if err != nil {
		s.Log.Error("assistant chat failed", "error", err)
		writeError(w, http.StatusBadGateway, "assistant request failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reply":      reply,
		"tool_calls": calls,
	})
}
