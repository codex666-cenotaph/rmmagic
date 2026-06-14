package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Supported assistant providers.
const (
	providerAnthropic = "anthropic"
	providerMistral   = "mistral"
)

// defaultModel returns the default model id for a provider.
func defaultModel(provider string) string {
	switch provider {
	case providerMistral:
		return "mistral-large-latest"
	default:
		return string(anthropic.ModelClaudeOpus4_8)
	}
}

// toolExecutor runs one tool by name with the model's JSON arguments and
// returns the result text plus whether it should be reported as an error.
type toolExecutor func(ctx context.Context, name string, input json.RawMessage) (string, bool)

// provider runs the full agentic loop for one chat turn: it calls the
// model, executes any requested tools via exec, feeds results back, and
// returns the final reply plus the tool calls it made (for the UI).
type provider interface {
	chat(ctx context.Context, system string, tools []assistantTool, history []assistantMessage, exec toolExecutor) (string, []assistantToolCall, error)
}

// newProvider constructs a provider from settings. apiKey must be set.
func newProvider(name, apiKey, model string) (provider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("no API key configured")
	}
	if model == "" {
		model = defaultModel(name)
	}
	switch name {
	case providerAnthropic:
		c := anthropic.NewClient(option.WithAPIKey(apiKey))
		return &anthropicProvider{client: c, model: anthropic.Model(model)}, nil
	case providerMistral:
		return &mistralProvider{apiKey: apiKey, model: model,
			http: &http.Client{Timeout: 120 * time.Second}}, nil
	default:
		return nil, fmt.Errorf("unknown provider %q", name)
	}
}

// --- Anthropic ---

type anthropicProvider struct {
	client anthropic.Client
	model  anthropic.Model
}

func (p *anthropicProvider) chat(ctx context.Context, system string, tools []assistantTool, history []assistantMessage, exec toolExecutor) (string, []assistantToolCall, error) {
	toolDefs := make([]anthropic.ToolUnionParam, len(tools))
	for i, t := range tools {
		toolDefs[i] = anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
			Name:        t.def.name,
			Description: anthropic.String(t.def.description),
			InputSchema: anthropic.ToolInputSchemaParam{Properties: t.def.schema, Required: t.def.required},
		}}
	}

	msgs := make([]anthropic.MessageParam, 0, len(history))
	for _, m := range history {
		if m.Content == "" {
			continue
		}
		if m.Role == "assistant" {
			msgs = append(msgs, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
		} else {
			msgs = append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		}
	}

	var calls []assistantToolCall
	for turn := 0; turn < maxAssistantTurns; turn++ {
		resp, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     p.model,
			MaxTokens: 8000,
			System:    []anthropic.TextBlockParam{{Text: system}},
			Tools:     toolDefs,
			Messages:  msgs,
		})
		if err != nil {
			return "", calls, err
		}
		msgs = append(msgs, resp.ToParam())

		if resp.StopReason != anthropic.StopReasonToolUse {
			return anthropicText(resp), calls, nil
		}

		var results []anthropic.ContentBlockParamUnion
		for _, block := range resp.Content {
			tu, ok := block.AsAny().(anthropic.ToolUseBlock)
			if !ok {
				continue
			}
			input := json.RawMessage(tu.JSON.Input.Raw())
			calls = append(calls, assistantToolCall{Name: tu.Name, Input: input})
			out, isErr := exec(ctx, tu.Name, input)
			results = append(results, anthropic.NewToolResultBlock(tu.ID, out, isErr))
		}
		if len(results) == 0 {
			return anthropicText(resp), calls, nil
		}
		msgs = append(msgs, anthropic.NewUserMessage(results...))
	}
	return assistantStepLimitMsg, calls, nil
}

func anthropicText(resp *anthropic.Message) string {
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok && t.Text != "" {
			return t.Text
		}
	}
	return ""
}

// --- Mistral (OpenAI-compatible chat completions with tool calling) ---

type mistralProvider struct {
	apiKey string
	model  string
	http   *http.Client
}

const mistralEndpoint = "https://api.mistral.ai/v1/chat/completions"

// mistralMessage mirrors the OpenAI/Mistral chat message shape.
type mistralMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content"`
	ToolCalls  []mistralToolCall `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Name       string            `json:"name,omitempty"`
}

type mistralToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (p *mistralProvider) chat(ctx context.Context, system string, tools []assistantTool, history []assistantMessage, exec toolExecutor) (string, []assistantToolCall, error) {
	toolDefs := make([]map[string]any, len(tools))
	for i, t := range tools {
		params := map[string]any{"type": "object", "properties": t.def.schema}
		if len(t.def.required) > 0 {
			params["required"] = t.def.required
		}
		toolDefs[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.def.name,
				"description": t.def.description,
				"parameters":  params,
			},
		}
	}

	msgs := []mistralMessage{{Role: "system", Content: system}}
	for _, m := range history {
		if m.Content == "" {
			continue
		}
		role := "user"
		if m.Role == "assistant" {
			role = "assistant"
		}
		msgs = append(msgs, mistralMessage{Role: role, Content: m.Content})
	}

	var calls []assistantToolCall
	for turn := 0; turn < maxAssistantTurns; turn++ {
		msg, err := p.complete(ctx, msgs, toolDefs)
		if err != nil {
			return "", calls, err
		}
		msgs = append(msgs, msg)

		if len(msg.ToolCalls) == 0 {
			return msg.Content, calls, nil
		}
		for _, tc := range msg.ToolCalls {
			input := json.RawMessage(tc.Function.Arguments)
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			calls = append(calls, assistantToolCall{Name: tc.Function.Name, Input: input})
			out, _ := exec(ctx, tc.Function.Name, input)
			msgs = append(msgs, mistralMessage{
				Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: out,
			})
		}
	}
	return assistantStepLimitMsg, calls, nil
}

// complete performs one Mistral chat-completions call and returns the
// assistant message.
func (p *mistralProvider) complete(ctx context.Context, msgs []mistralMessage, tools []map[string]any) (mistralMessage, error) {
	body, _ := json.Marshal(map[string]any{
		"model":       p.model,
		"max_tokens":  8000,
		"messages":    msgs,
		"tools":       tools,
		"tool_choice": "auto",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mistralEndpoint, bytes.NewReader(body))
	if err != nil {
		return mistralMessage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		return mistralMessage{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mistralMessage{}, fmt.Errorf("mistral API HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(raw))
	}

	var parsed struct {
		Choices []struct {
			Message mistralMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return mistralMessage{}, fmt.Errorf("decode mistral response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return mistralMessage{}, fmt.Errorf("mistral returned no choices")
	}
	return parsed.Choices[0].Message, nil
}
