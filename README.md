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

Bootstrap the first tenant (privileged DB connection; password via env
so it stays out of shell args):

```sh
export RMM_DATABASE_URL='postgres://rmm:rmm-dev-only@localhost:5432/rmm?sslmode=disable'
RMM_BOOTSTRAP_PASSWORD='choose-a-long-password' \
  go -C server run ./cmd/rmmserver bootstrap \
  --tenant "My MSP" --slug my-msp --email you@example.com
```

Run the server (all roles in one process) and the dashboard:

```sh
RMM_MASTER_KEY=$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n') \
RMM_COOKIE_SECURE=false \
  go -C server run ./cmd/rmmserver --roles=api,gateway,worker
cd web && npm install && npm run dev   # http://localhost:5173, proxies /api
```

Enroll a Linux endpoint: create an enrollment token in the dashboard
(Enrollment page), then on the endpoint:

```sh
go -C agent build -o rmmagent ./cmd/rmmagent
sudo ./rmmagent enroll --server http://localhost:8080 --token rmme_...
sudo ./rmmagent run        # use --state-dir for non-root development
```

For a real endpoint, `deploy/install-agent.sh` does this end to end —
it builds/installs the binary, prompts for the server URL and token, and
optionally installs a systemd unit so the agent runs at boot:

```sh
sudo deploy/install-agent.sh                       # guided, interactive
sudo deploy/install-agent.sh \
  --server https://rmm.example.com --token rmme_... --service   # unattended
```

The device appears on the Devices page within seconds (heartbeat) and
charts fill in as stats arrive (60s interval). Decommissioning from the
dashboard revokes the device identity and disconnects the agent.

Integration tests (live RLS tenant-isolation probes, full API flows):

```sh
make test-integration   # resets the dev database schema
```

## Architecture

See `docs/PLAN.md` for the MVP architecture, data model, security
design (device identity, RLS tenant isolation, signed auto-updates,
RBAC, audit logging), and milestone breakdown.
