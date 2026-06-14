package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
)

// maxAssistantTurns bounds the agentic loop so a misbehaving model can't
// spin forever; each turn is one model call plus any tool executions.
const maxAssistantTurns = 8

// assistantChatReq is the request body: the running conversation as a
// flat list of user/assistant text turns. The client keeps the history;
// the server is stateless.
type assistantChatReq struct {
	Messages []assistantMessage `json:"messages"`
}

type assistantMessage struct {
	Role    string `json:"role"`    // "user" | "assistant"
	Content string `json:"content"` // plain text
}

// assistantToolCall is a record of one tool the assistant invoked,
// returned to the UI for transparency.
type assistantToolCall struct {
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

func (s *Server) handleAssistantChat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)

	if s.Assistant == nil {
		writeError(w, http.StatusServiceUnavailable, "assistant is not configured")
		return
	}

	var req assistantChatReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages required")
		return
	}

	msgs := make([]anthropic.MessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Content == "" {
			continue
		}
		switch m.Role {
		case "assistant":
			msgs = append(msgs, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
		default:
			msgs = append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		}
	}
	if len(msgs) == 0 {
		writeError(w, http.StatusBadRequest, "messages required")
		return
	}

	reply, calls, err := s.runAssistant(ctx, p, msgs)
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

// runAssistant drives the agentic loop: call the model, execute any tool
// calls it requests against this server's API as the principal, feed the
// results back, and repeat until the model produces a final answer.
func (s *Server) runAssistant(ctx context.Context, p *auth.Principal, msgs []anthropic.MessageParam) (string, []assistantToolCall, error) {
	defs := assistantTools()
	tools := make([]anthropic.ToolUnionParam, len(defs))
	byName := make(map[string]assistantTool, len(defs))
	for i, t := range defs {
		tools[i] = t.def
		byName[t.def.OfTool.Name] = t
	}

	var calls []assistantToolCall

	for turn := 0; turn < maxAssistantTurns; turn++ {
		resp, err := s.Assistant.Client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     s.Assistant.Model,
			MaxTokens: 8000,
			System:    []anthropic.TextBlockParam{{Text: assistantSystemPrompt}},
			Tools:     tools,
			Messages:  msgs,
		})
		if err != nil {
			return "", calls, err
		}
		msgs = append(msgs, resp.ToParam())

		if resp.StopReason != anthropic.StopReasonToolUse {
			return assistantText(resp), calls, nil
		}

		// Execute every requested tool and feed the results back in one turn.
		var results []anthropic.ContentBlockParamUnion
		for _, block := range resp.Content {
			tu, ok := block.AsAny().(anthropic.ToolUseBlock)
			if !ok {
				continue
			}
			calls = append(calls, assistantToolCall{Name: tu.Name, Input: json.RawMessage(tu.JSON.Input.Raw())})
			out, isErr := s.runAssistantTool(ctx, p, byName, tu)
			results = append(results, anthropic.NewToolResultBlock(tu.ID, out, isErr))
		}
		if len(results) == 0 {
			return assistantText(resp), calls, nil
		}
		msgs = append(msgs, anthropic.NewUserMessage(results...))
	}

	return "I wasn't able to finish that within the allowed number of steps. Please narrow the request and try again.", calls, nil
}

// runAssistantTool resolves and executes one tool call, returning the
// result text and whether it should be reported to the model as an error.
func (s *Server) runAssistantTool(ctx context.Context, p *auth.Principal, byName map[string]assistantTool, tu anthropic.ToolUseBlock) (string, bool) {
	t, ok := byName[tu.Name]
	if !ok {
		return "unknown tool: " + tu.Name, true
	}
	var args map[string]any
	if raw := tu.JSON.Input.Raw(); raw != "" {
		_ = json.Unmarshal([]byte(raw), &args)
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
		// Surface the API's own error message; >=400 is reported as a tool
		// error so the model can adjust rather than treat it as data.
		return string(respBody), status >= 400
	}
	return string(respBody), false
}

func assistantText(resp *anthropic.Message) string {
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok && t.Text != "" {
			return t.Text
		}
	}
	return ""
}
