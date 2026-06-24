# In-dashboard AI assistant

The dashboard ships an optional "Ask AI" assistant: a chat panel
(`web/src/components/Assistant.tsx`) backed by `POST /api/v1/assistant/chat`.
It lets technicians inspect and manage the fleet in natural language —
"which devices are offline?", "run the disk-cleanup script on web-01",
"acknowledge the disk alert on db-02".

## How it works

```
Dashboard chat ──► POST /api/v1/assistant/chat ──► Claude API (tools)
                          │                              │
                          │   tool calls execute in-process against this
                          ▼   server's own REST handlers, as the signed-in
                   internal dispatch ─────────────────► user's Principal
```

- The endpoint requires an authenticated session (`PermSelf`). The
  conversation is stateless — the client sends the full message history each
  call.
- The server runs an agentic loop with the Claude API (default model
  `claude-opus-4-8`), exposing a focused tool set (devices, stats, inventory,
  alerts, scripts, jobs, policies, customers/sites).
- **Authorization is delegated, not bypassed.** Each tool the model calls is
  replayed against the server's *own* API handlers via an in-process dispatch
  (`assistant_dispatch.go`) carrying the chatting user's `Principal`. So the
  assistant can read or do exactly what that user could do through the
  dashboard — the same RBAC, scope, and RLS checks apply — and nothing more.
  The mass-action blast-radius confirmation flow is honoured too.

## Configuring it

There are two ways to configure the assistant; per-tenant settings take
precedence over the environment fallback.

### Per-tenant, from the dashboard (recommended)

A tenant admin (`tenant.manage`) configures it under **Settings → AI
assistant**: enable/disable, pick a **provider**, set the **model**, and paste
the **API key**. Settings are stored per tenant; the API key is sealed with the
server master key (`secrets.Box`) and is never returned to the browser. Backed
by `GET`/`PUT /api/v1/assistant/settings`.

| Provider | Default model | Notes |
|---|---|---|
| `anthropic` | `claude-opus-4-8` | Uses the Anthropic Messages API + tool use. |
| `mistral` | `mistral-large-latest` | Uses Mistral's chat-completions API + function calling. |

The agentic loop is provider-agnostic (`providers.go`): the same tool set and
internal-dispatch authorization apply to both. Only the model call and
tool/function encoding differ per provider.

### Environment fallback

If a tenant has no settings (or hasn't enabled the assistant), the server falls
back to an Anthropic key from the environment:

| Variable | Meaning |
|---|---|
| `RMM_ANTHROPIC_API_KEY` | Anthropic API key. When neither this nor per-tenant settings are present, the endpoint returns 503 and the panel shows an "unavailable" message. |
| `RMM_ASSISTANT_MODEL` | Optional model override for the fallback (default `claude-opus-4-8`). |

```sh
RMM_ANTHROPIC_API_KEY=sk-ant-... \
  go -C server run ./cmd/rmmserver --roles=api,gateway,worker
```

Either way, the key lives only on the server; it is never sent to the browser,
and the model never sees the user's session or any credential.

## Relationship to the MCP server

This is the in-product path. For *external* agents (Claude Desktop, claude.ai
connectors, the Agent SDK), use the standalone MCP server in `mcp/` — see
`mcp/README.md`. Both surfaces wrap the same REST API and both delegate
authorization to it; they differ only in who drives the loop and where it runs.
