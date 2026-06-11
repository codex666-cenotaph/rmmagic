# rmmagic — SaaS RMM Platform MVP Plan

## Context

Greenfield build of a multi-tenant SaaS RMM (Remote Monitoring & Management) platform for MSPs, targeting Windows/macOS/Linux endpoints (Linux agent first) and eventual scale of millions of endpoints. Security is the top product value — RMMs are a prime ransomware vector (Kaseya 2021), so MSPs will judge the product on identity, auditability, and blast-radius controls as much as features. The repo is currently empty (README only).

**Agreed MVP scope** (original list + gaps identified in discussion):
multi-tenant dashboard/config platform, secure Linux agent with full lifecycle (enrollment, auto-update, decommission), endpoint statistics, HW/SW inventory, script deployment, app deployment, alerting/monitoring policies, remote shell, RBAC + MFA + audit logging, hardened APIs.

**Locked decisions:** Go backend, Go agent, persistent WebSocket over TLS:443 (protobuf frames) for the agent channel + HTTPS for bulk telemetry, per-device identity (keypair/cert issued at enrollment), tenancy hierarchy MSP → Customer → Site → Device with policy inheritance in the schema from day one.

## Repository Layout (monorepo, Go workspace)

```
go.work, Makefile, docker-compose.yml        # dev stack: postgres, nats, minio, mailpit
proto/rmm/v1/                                # protobuf agent↔server protocol (buf toolchain)
server/                                      # Go module
  cmd/rmmserver/main.go                      # single binary, --roles=api,gateway,worker
  internal/{api,gateway,auth,ca,store,jobs,alerts,telemetry,audit,events}/
  migrations/                                # golang-migrate SQL
agent/                                       # Go module (minimal deps = small attack surface)
  cmd/rmmagent/main.go
  internal/{conn,enroll,collect,exec,shell,update,platform}/
  packaging/                                 # nfpm deb/rpm, systemd unit, install.sh
shared/                                      # generated proto Go code, protocol constants
web/                                         # React + TS + Vite, TanStack Query, Tailwind+shadcn/ui, xterm.js
deploy/  docs/                               # Dockerfiles, ADRs, threat model
```

## Architecture

Logical services in one physical binary at MVP (split later via `--roles` flag):
- **API service** — REST (chi + huma for spec-first validation/OpenAPI → typed TS client). Session-cookie auth for dashboard, API tokens for public API.
- **Agent Gateway** — terminates persistent agent WSS connections (`coder/websocket`), device auth, in-memory connection registry. Horizontal scale later: `device_id → gateway` mapping in NATS KV, commands routed via NATS subjects. Agents never see the broker.
- **Worker/Scheduler** — cron job scheduling, alert evaluation, offline detection, notifications, stats rollups. Postgres `FOR UPDATE SKIP LOCKED` queues (no extra infra).

Infra: Postgres 16 (pgx/v5 + sqlc), NATS embedded at MVP behind an `events` interface, S3-compatible storage (MinIO dev) for agent binaries/scripts/shell recordings, slog JSON logging, Prometheus metrics.

**Protocol:** WebSocket (not gRPC) — better firewall/proxy traversal on 443, simpler LB story; protobuf envelope with oneof payloads (Heartbeat, CommandRequest/Result, ShellData, UpdateOffer). Stats/inventory batched over HTTPS POST with the same device credential.

## Data Model (key tables)

All tenant-owned tables carry `tenant_id`. **Isolation = app-level scoping primary + Postgres RLS backstop**: store layer does `SET LOCAL app.tenant_id` per transaction (never session-level — pgx pool), RLS policies enforce it, non-superuser DB role so RLS applies.

`tenants`, `customers`, `sites`, `devices` (status, last_seen, hw fingerprint), `device_credentials` (cert serials, revocation), `enrollment_tokens` (hashed, expiry, max_uses), `users` (argon2id, encrypted TOTP secret), `roles`/`role_assignments` (scope_type: tenant|customer|site), `api_tokens` (hashed, scoped permissions), `scripts`/`script_versions` (params_schema jsonb), `jobs` (target_selector, schedule, blast_radius_ack, rollout jsonb), `job_runs`/`job_run_devices` (per-device status/exit_code/output_ref, expires_at), `policies` (inheritance via device→site→customer→tenant merge), `alerts` (dedup_key, fire/resolve/ack), `notification_channels`, `device_stats` (day-partitioned + hourly rollups), `inventory_hw`/`inventory_sw`, `shell_sessions` (recording_ref), `audit_log` (append-only via grants, monthly partitions, redacted details), `agent_releases` (global: version, sha256, signature).

## Security Architecture

- **Enrollment:** site-scoped token (plaintext shown once, hash stored) → agent generates Ed25519/P-256 keypair locally (root-owned `0600`) → server validates token (rate-limited, constant-time) → internal device CA (stdlib x509; CA key in KMS/encrypted) signs ~30-day client cert encoding tenant/device IDs, auto-renewed over the channel. mTLS at gateway (TLS passthrough); fallback design = signed-challenge device-keypair auth at app layer (same identity, drop-in).
- **Decommission:** revoke → drop connection, serial in revocation set checked at handshake, optional self-uninstall command.
- **User auth:** argon2id (`alexedwards/argon2id`), server-side sessions, TOTP MFA (`pquerna/otp`) + recovery codes, per-tenant mandatory-MFA option.
- **RBAC:** `authz.Require(ctx, perm, scope)` in every handler, enforced by a route-table test asserting every route declares a permission. Built-in roles: Owner/Admin/Technician/Read-only.
- **Mass-action safeguard:** jobs targeting >N devices (default 25) require a server-issued confirmation nonce showing resolved device count; schema supports staged rollout day one.
- **Audit:** middleware + explicit `audit.Record()` on all mutations, logins, MFA, shell, jobs, enrollment; secret-tagged fields redacted; append-only grants.
- **Secrets:** AES-256-GCM envelope encryption (master key env/KMS) for MFA/webhook/SMTP secrets; slog redaction hook; never log auth/enrollment bodies.
- **Signed updates:** CI builds linux amd64/arm64, signs with minisign/Ed25519 release key; agent embeds trusted public keys (rotation list); verify sha256+signature before atomic swap (`renameio`); keep `.prev` binary + watchdog rollback on failed post-update health check; deb/rpm GPG-signed.
- **API hardening:** huma schema validation, per-route + per-IP rate limits (auth/enrollment strictest), CORS locked, security headers, request size caps, idempotency keys on job creation.

## Agent Design

Single static binary, root, systemd-hardened unit. Persistent WS with jittered backoff, 30–60s heartbeats (offline after 3 missed). At-least-once command delivery with idempotent command IDs + local bbolt journal of executed IDs/buffered results. Collectors via `shirou/gopsutil/v4` + dpkg/rpm parsing. Scripts: declared interpreter, `0700` temp dir, params as env vars (schema-validated server-side), timeout + output cap + process-group kill. Remote shell: `creack/pty`, multiplexed over the existing WS by session ID, teed to asciinema-style S3 recording, idle timeout, `shell.connect` permission. App deployment: apt/dnf job type via `internal/platform` interface (Linux impl only at MVP, interface cross-platform). Packaging: goreleaser + nfpm.

## Milestones (each independently demoable)

- **M0 — Scaffold:** go.work, modules, Makefile, compose stack, CI (lint/test/build/govulncheck/gosec/gitleaks), buf, web shell.
- **M1 — Tenancy + auth + RBAC + audit:** migrations with RLS, bootstrap CLI, login/sessions/MFA, API tokens, RBAC engine + route-permission test, audit middleware; dashboard org-tree + user/role CRUD. *Demo: scoped technician + audit trail.*
- **M2 — Enrollment + heartbeat + stats:** device CA, enrollment endpoint + token UI, gateway, online/offline, stats ingest + partitioned tables, revocation; device list/detail with charts. *Demo: one-line install → live device; revoke kills it.*
- **M3 — Scripts + jobs:** script library w/ versioning + params, target selector + blast-radius confirmation, dispatcher with offline queue/expiry, agent idempotent exec, cron scheduling, output capture + run UI. *Demo: parameterized script across a site, nightly schedule.*
- **M4 — Inventory + alerting:** HW/SW inventory + UI, policy inheritance resolution, threshold/offline/service-down evaluation, alert lifecycle + dedup, email + signed webhooks. *Demo: customer-level disk threshold fires email.*
- **M5 — Remote shell:** PTY bridge, xterm.js, S3 session recording + playback, permission + audit.
- **M6 — App deployment + auto-update:** apt/dnf jobs; release pipeline (goreleaser + minisign + S3 + agent_releases), offer/verify/swap/rollback, update channels in UI. *Demo: roll agent vNext to one site.*
- **M7 — Hardening:** rate limits everywhere, fuzz enrollment/ingest parsers, cross-tenant probe suite, threat-model doc, 10k-agent swarm load test, backup/restore runbook, published OpenAPI docs.

## Verification

- sqlc queries tested against real Postgres (`testcontainers-go`), no DB mocks.
- **Tenant-isolation suite (CI on every PR):** two seeded tenants, every endpoint probed cross-tenant (expect 403/404), raw-SQL probes proving RLS blocks buggy queries.
- Authz coverage test over the route table.
- Agent↔gateway protocol tests in-process (real WS + test CA): reconnect, offline queue, idempotent redelivery.
- `make e2e`: compose stack + agent in Debian container; harness drives enroll → stats → script → alert → shell → signed update. Nightly + release CI.
- `cmd/agentsim` swarm simulator for gateway/ingest load.
- Threat-model reviews at end of M2 (identity) and M6 (update pipeline); external pen test before GA.

## Highest-Risk Areas

1. **mTLS termination at the edge** — fiddly behind cloud LBs; design the signed-challenge fallback into M2, review CA key custody early.
2. **Auto-update rollback** — a bad update bricks the management channel; the `.prev` watchdog must be tested ruthlessly.
3. **Command delivery semantics** — at-least-once + idempotency across gateway restarts; race bugs = scripts running twice on customer machines. Invest in protocol tests at M3.
4. **RLS + connection pooling** — `SET LOCAL` per transaction only; cross-tenant probe suite is the guardrail.
5. **Telemetry volume** — partitioning/rollups buy time; keep ingest behind an interface so TimescaleDB/ClickHouse can slot in without touching agents.

## First files to create (in order of leverage)

1. `proto/rmm/v1/agent.proto` — protocol envelope everything depends on
2. `server/migrations/0001_tenancy.sql` — tenancy schema + RLS
3. `server/internal/auth/authz.go` — RBAC engine used by every handler
4. `server/internal/gateway/gateway.go` — agent connections, device auth, command routing
5. `agent/internal/enroll/enroll.go` — enrollment + device identity
