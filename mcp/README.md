# rmmagic MCP server

`rmmmcp` is a [Model Context Protocol](https://modelcontextprotocol.io)
server that connects AI agents to a rmmagic instance. It wraps the
existing control-plane REST API as MCP tools and speaks MCP over stdio,
so it plugs into Claude Desktop, the Claude Agent SDK, and any other
MCP-capable client.

The server adds **no privileges of its own**: it authenticates to the API
with a tenant API token, and the token's permissions and scope decide
what an agent can read or do. Mint a narrowly-scoped, read-only token if
you only want the agent to observe; grant `scripts.execute`,
`devices.manage`, or `alerts.manage` only when you want it to act.

## Configuration

Set two environment variables:

| Variable | Meaning |
|---|---|
| `RMM_MCP_SERVER_URL` | Base URL of the rmmagic server, e.g. `https://rmm.example.com` |
| `RMM_MCP_TOKEN` | A rmmagic API token (`rmm_...`), created on the dashboard's API Tokens page |

Logs go to stderr; stdout carries the JSON-RPC stream.

## Build & run

```sh
go -C mcp build -o rmmmcp ./cmd/rmmmcp

RMM_MCP_SERVER_URL=https://rmm.example.com \
RMM_MCP_TOKEN=rmm_xxxxxxxx \
  ./rmmmcp
```

## Client configuration

Add an `mcpServers` entry pointing at the binary, for example in a Claude
Desktop / Agent SDK config:

```json
{
  "mcpServers": {
    "rmmagic": {
      "command": "/path/to/rmmmcp",
      "env": {
        "RMM_MCP_SERVER_URL": "https://rmm.example.com",
        "RMM_MCP_TOKEN": "rmm_xxxxxxxx"
      }
    }
  }
}
```

## Tools

Each tool maps to one REST endpoint. Read-only tools need only the `*.read`
permissions; the rest require the matching management permission.

| Tool | Endpoint | Permission |
|---|---|---|
| `rmm_list_customers` | `GET /customers` | `org.read` |
| `rmm_list_sites` | `GET /customers/{id}/sites` | `org.read` |
| `rmm_list_devices` | `GET /devices` | `devices.read` |
| `rmm_get_device` | `GET /devices/{id}` | `devices.read` |
| `rmm_get_device_stats` | `GET /devices/{id}/stats` | `devices.read` |
| `rmm_get_device_inventory` | `GET /devices/{id}/inventory` | `devices.read` |
| `rmm_get_effective_policy` | `GET /devices/{id}/effective-policy` | `policies.read` |
| `rmm_set_device_tags` | `PUT /devices/{id}/tags` | `devices.manage` |
| `rmm_list_scripts` | `GET /scripts` | `scripts.read` |
| `rmm_get_script` | `GET /scripts/{id}` | `scripts.read` |
| `rmm_dispatch_script` | `POST /scripts/{id}/dispatch` | `scripts.execute` |
| `rmm_list_jobs` | `GET /jobs` | `scripts.read` |
| `rmm_get_job` | `GET /jobs/{id}` | `scripts.read` |
| `rmm_get_job_output` | `GET /jobs/{id}/output` | `scripts.read` |
| `rmm_list_schedules` | `GET /schedules` | `scripts.read` |
| `rmm_list_policies` | `GET /policies` | `policies.read` |
| `rmm_list_alerts` | `GET /alerts` | `alerts.read` |
| `rmm_get_alert` | `GET /alerts/{id}` | `alerts.read` |
| `rmm_ack_alert` | `POST /alerts/{id}/ack` | `alerts.manage` |
| `rmm_list_audit` | `GET /audit` | `audit.read` |

### Mass-action safeguard

`rmm_dispatch_script` honors the API's blast-radius guard. If a target
resolves to more devices than the server's threshold, the API replies with
`confirmation_required` and a `confirm_token`. Call the tool again with
that `confirm_token` to proceed — the agent therefore always sees the real
device count before anything runs.

## Design

- `internal/mcp` — a dependency-free MCP server over stdio (JSON-RPC 2.0,
  newline-delimited): `initialize`, `tools/list`, `tools/call`, `ping`.
- `internal/rmm` — the REST client (bearer auth, error surfacing).
- `internal/tools` — the tool definitions and their argument schemas.

The module uses only the Go standard library.
