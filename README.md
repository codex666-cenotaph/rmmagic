# rmmagic

European RMM alternative — a multi-tenant SaaS RMM (Remote Monitoring &
Management) platform for MSPs. Go control plane + Go endpoint agent
(Linux first), React dashboard, security-first design.

## Layout

| Path | What |
|---|---|
| `server/` | Control plane: REST API, agent gateway (WSS), worker/scheduler — one binary, role flags |
| `agent/` | Endpoint agent: enrollment, heartbeat, script/package execution, remote shell |
| `shared/` | Protocol types and version info shared by server and agent |
| `proto/` | Protobuf definitions of the agent↔server wire protocol |
| `web/` | React + TypeScript dashboard |
| `docs/` | Architecture decisions, threat model, plan |

## Development

Requirements: Go 1.24+, Docker, Node 22+ (dashboard), and optionally
`buf`, `golang-migrate`, `staticcheck`.

```sh
make dev-stack    # postgres, nats, minio, mailpit
make migrate-up   # apply database migrations
make build        # build all Go modules
make test         # run all tests
```

Run the server (all roles in one process):

```sh
cd server && go run ./cmd/rmmserver --roles=api,gateway,worker
```

## Architecture

See `docs/PLAN.md` for the MVP architecture, data model, security
design (device identity, RLS tenant isolation, signed auto-updates,
RBAC, audit logging), and milestone breakdown.
