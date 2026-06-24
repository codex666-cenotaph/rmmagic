# API v1

Base path `/api/v1`. All bodies JSON. Errors: `{"error": "message"}` with
4xx/5xx status. Browser auth via `rmm_session` HttpOnly cookie; API
clients via `Authorization: Bearer rmm_<token>`.

## Auth

| Method/Path | Body | Response |
|---|---|---|
| POST /auth/login | `{email, password}` | 200 `{mfa_required: bool}`; sets session cookie. 401 on bad credentials. |
| POST /auth/mfa/verify | `{code}` (TOTP or recovery code) | 200 `{}` — upgrades pending session. 401 bad code. |
| POST /auth/logout | – | 204 |
| GET /auth/me | – | 200 `{user: {id, email, mfa_enabled}, tenant: {id, name, slug}, grants: [{scope_type, scope_id, permissions: [string]}]}` |
| POST /auth/mfa/setup | – | 200 `{secret, otpauth_url}` (authed, MFA not yet enabled) |
| POST /auth/mfa/enable | `{code}` | 200 `{recovery_codes: [string]}` — shown once |

After login with `mfa_required: true`, only `/auth/mfa/verify` and
`/auth/logout` are usable until verification succeeds.

## Organization (customers → sites)

| Method/Path | Permission | Body / Response |
|---|---|---|
| GET /customers | org.read | `{customers: [{id, name, created_at}]}` (filtered to accessible scopes) |
| POST /customers | org.manage | `{name}` → 201 `{id, name, created_at}` |
| PATCH /customers/{id} | org.manage | `{name}` → 200 |
| DELETE /customers/{id} | org.manage | 204 (409 if it still has sites) |
| GET /customers/{id}/sites | org.read | `{sites: [{id, customer_id, name, timezone}]}` |
| POST /customers/{id}/sites | org.manage | `{name, timezone?}` → 201 |
| PATCH /sites/{id} | org.manage | `{name?, timezone?}` → 200 |
| DELETE /sites/{id} | org.manage | 204 |

## Users & roles

| Method/Path | Permission | Body / Response |
|---|---|---|
| GET /users | users.read | `{users: [{id, email, status, mfa_enabled, assignments: [{id, role_id, role_name, scope_type, scope_id}]}]}` |
| POST /users | users.manage | `{email, password}` → 201 `{id, email}` |
| PATCH /users/{id} | users.manage | `{status: "active"\|"disabled"}` → 200 |
| GET /roles | users.read | `{roles: [{id, name, permissions, is_builtin}]}` |
| POST /users/{id}/assignments | users.manage | `{role_id, scope_type: "tenant"\|"customer"\|"site", scope_id?}` → 201 `{id}` |
| DELETE /assignments/{id} | users.manage | 204 |

## API tokens

| Method/Path | Permission | Body / Response |
|---|---|---|
| GET /api-tokens | tokens.manage | `{tokens: [{id, name, permissions, scope_type, scope_id, last_used_at, expires_at, revoked_at, created_at}]}` |
| POST /api-tokens | tokens.manage | `{name, permissions: [string], scope_type?, scope_id?, expires_at?}` → 201 `{id, token}` — plaintext shown once |
| DELETE /api-tokens/{id} | tokens.manage | 204 (revokes) |

Requested permissions must be a subset of the caller's own.

## Devices

| Method/Path | Permission | Body / Response |
|---|---|---|
| GET /devices | devices.read | `{devices: [{id, site_id, site_name, customer_id, customer_name, hostname, os, arch, agent_version, status: "active"\|"decommissioned", online: bool, health: "healthy"\|"warning"\|"critical"\|"unknown", last_seen_at, created_at}]}` (scope-filtered) |
| GET /devices/{id} | devices.read | one device object (shape above); `health` is the worst of its checks |
| GET /devices/{id}/stats?since=RFC3339&until=RFC3339 | devices.read | `{samples: [{ts, cpu_pct, mem_used, mem_total, disks: [{mount, used, total}], net: {rx_bytes, tx_bytes}}]}` ascending by ts; default window = last hour |
| GET /devices/{id}/health | devices.read | `{health, checks: [{schedule_id, name, status, message, job_id, checked_at}]}` — latest result of each health check, worst first |
| POST /devices/{id}/decommission | devices.manage | 200 `{}` — revokes the device identity and disconnects a live agent |

## Enrollment tokens

| Method/Path | Permission | Body / Response |
|---|---|---|
| GET /enrollment-tokens | devices.enroll | `{tokens: [{id, site_id, site_name, expires_at, max_uses, use_count, revoked_at, created_at}]}` |
| POST /enrollment-tokens | devices.enroll | `{site_id, expires_at?, max_uses?}` → 201 `{id, token}` — plaintext `rmme_...` shown once. Defaults: 24h expiry, max_uses 1. |
| DELETE /enrollment-tokens/{id} | devices.enroll | 204 (revokes) |

Install command to display with a new token:
`rmmagent enroll --server https://<host> --token <token> && rmmagent run`

## Scripts

| Method/Path | Permission | Body / Response |
|---|---|---|
| GET /scripts?archived=true | scripts.read | `{scripts: [{id, name, description, language, body, parameters, version, archived, created_at, updated_at}]}` (default list excludes archived) |
| POST /scripts | scripts.manage | `{name, description?, language: "bash"\|"powershell"\|"python"\|"batch", body, parameters?: [{name, description?, default?, required?}]}` → 201 `{id}` |
| GET /scripts/{id} | scripts.read | one script object |
| PATCH /scripts/{id} | scripts.manage | same body as POST → 200 (bumps `version`) |
| DELETE /scripts/{id} | scripts.manage | 200 (archives; archived scripts cannot be dispatched) |
| POST /scripts/{id}/dispatch | scripts.execute | see below |

### Dispatch and the blast-radius safeguard

`POST /scripts/{id}/dispatch` body:
`{target, parameters?: {name: value}, timeout_s? (default 300), expires_in_s? (default 86400, max 604800), confirm_token?}`
with `target` exactly one of `{device_ids: [uuid]}`, `{site_id}`,
`{customer_id}` (legacy shorthand `{device_id}` still accepted). The
target expands to its **active** devices at dispatch time; targeting
devices outside the caller's scripts.execute scope fails the request.

If the target resolves to more than the blast-radius threshold
(default 25 devices), the response is **409**
`{confirmation_required: true, device_count, confirm_token}`; repeat the
identical request with `confirm_token` set to proceed. Tokens are bound
to tenant + script + exact target + count and expire after 5 minutes.

Success: 201 `{job_ids: [uuid], device_count}` (plus `job_id` when the
target was a single device). One job per device; offline devices get the
command when they reconnect, until `expires_at` passes.

## Jobs

| Method/Path | Permission | Response |
|---|---|---|
| GET /jobs?device_id= | scripts.read | `{jobs: [{id, kind, script_id?, script_name?, device_id, hostname, command_id, status, timeout_s, language?, parameters, spec?, schedule_id?, created_at, expires_at, sent_at?, started_at?, finished_at?}]}` newest first, scope-filtered |
| GET /jobs/{id} | scripts.read | one job object |
| GET /jobs/{id}/output | scripts.read | `{output, exit_code}` |

Job statuses: `pending` (queued, device offline) → `sent` → terminal
`succeeded`/`failed`/`timed_out`/`expired`. The worker sweeps queued
jobs past `expires_at` to `expired`.

## App deployment

Install or remove OS packages (apt/dnf, chosen per host) as jobs. Uses the
same dispatch pipeline, offline queue, and blast-radius 409 confirmation as
script dispatch. Package names are validated server-side.

| Method/Path | Permission | Body / Response |
|---|---|---|
| POST /apps/deploy | apps.deploy | `{operation: install\|remove, packages: [string], device_id? \| target, timeout_s?, expires_in_s?, confirm_token?}` → 201 `{job_ids, device_count}` (plus `job_id` for a single device) |

Package jobs appear in `/jobs` with `kind` = `package_install`/`package_remove`
and a `spec` of `{packages: [...]}` instead of a script.

### Rule-based deployment

The managed layer on top of `/apps/deploy`. **App packages** are reusable,
OS-specific app definitions (install spec + how to detect the app is already
present). **Deployment rules** bind a package to a scope
(tenant/customer/site/device) with optional tag and hostname filters. The
worker reconciles each enabled rule at most hourly: it resolves the scope,
applies the filters (the package OS is always enforced), skips devices that
already have the app (inventory detection) or that already have an install in
flight, and creates `package_install` jobs for the rest.

| Method/Path | Permission | Body / Response |
|---|---|---|
| GET /app-packages | apps.read | `?archived=true` to include archived → `{packages: [AppPackage]}` |
| POST /app-packages | apps.manage | `{name, description?, os: linux\|windows\|darwin, packages: [string], detection_names?: [string], timeout_s?}` → 201 `{id}` |
| GET /app-packages/{id} | apps.read | → `AppPackage` |
| PUT /app-packages/{id} | apps.manage | same body as POST → 204 |
| DELETE /app-packages/{id} | apps.manage | archive (soft-delete) → 204 |
| GET /deployment-rules | apps.read | → `{rules: [DeploymentRule]}` |
| POST /deployment-rules | apps.manage | `{package_id, name, scope_type, scope_id?, filters: {tags?, tags_match?: any\|all, hostname_regex?}, enabled}` → 201 `{id}` |
| GET /deployment-rules/{id} | apps.read | → `DeploymentRule` |
| PUT /deployment-rules/{id} | apps.manage | same body as POST → 204 |
| DELETE /deployment-rules/{id} | apps.manage | → 204 |

`AppPackage` stores `install` as `{packages: [...]}` and `detection` as
`{method: "package_name", names: [...]}` (empty `names` means "detect by the
install package names"). Deployment-created jobs carry no `created_by` (system
actor) and appear in `/jobs` like any other `package_install`.

## Agent updates

Signed auto-update. `agent_releases` is a **global** catalog (shared across
tenants) of binaries the agent verifies (sha256 + a detached Ed25519
signature against an embedded trusted key) before atomically swapping and
restarting; a watchdog rolls back to the previous binary if the new one
fails to reconnect. Rollouts and per-device update state are tenant-scoped.

| Method/Path | Permission | Body / Response |
|---|---|---|
| GET /agent-releases?channel= | devices.read | `{releases: [{id, channel, version, os, arch, url?, has_binary, sha256, signature, size_bytes, notes, created_at}]}` newest first |
| POST /agent-releases | agent.update | `{channel, version, os, arch, url?, sha256, signature, size_bytes?, notes?}` → 201 `{id}`. `url` is optional — omit it for a server-hosted release and upload the binary next |
| POST /agent-releases/{id}/binary | agent.update | multipart `file=<binary>` → 200 `{size_bytes}`. Stores the binary in server blob storage; the upload's sha256 must equal the release's registered `sha256` |
| POST /agent-releases/{id}/rollout | agent.update | `{device_id? \| target, confirm_token?}` → 200 `{version, matched, online_offered}`. 400 if the release has no binary/url yet |
| GET /device-updates | devices.read | `{updates: [{device_id, version, phase, error?, offered_at, updated_at}]}` |
| POST /devices/{id}/update-channel | devices.manage | `{channel: stable\|beta}` → 200 |

Rollout offers the release only to targeted devices whose `os`/`arch` match
it. Update phases (reported by the agent): `offered` → `downloading` →
`verified` → `applied`, or `failed`/`rolled_back`. On `applied` the device's
recorded `agent_version` advances. The release pipeline
(`.github/workflows/release.yml`) builds, Ed25519-signs, and publishes
binaries to a GitHub Release plus an `agent_releases.json` registration
manifest.

**Binary hosting (private repos):** a release may be **server-hosted** —
register metadata, then upload the binary via `POST /agent-releases/{id}/binary`.
The control plane stores it (filesystem by default; S3/MinIO when
`RMM_S3_ENDPOINT` is set) and serves it to agents at the device-authenticated
endpoint below, so the source repo / artifact host can stay private and
agents need no extra credentials.

| Method/Path | Auth | Response |
|---|---|---|
| GET /agent/v1/releases/{id}/download | device Ed25519 signature over the request path (`X-Device-Id`/`X-Timestamp`/`X-Signature`) | the binary (`application/octet-stream`) |

The `UpdateOffer.url` is this relative path for server-hosted releases (the
agent resolves it against its server URL and signs the request) or an
absolute URL for externally-hosted releases (fetched unauthenticated).

## Schedules

Cron-style recurring dispatch, evaluated in UTC by the worker role.

| Method/Path | Permission | Body / Response |
|---|---|---|
| GET /schedules | scripts.read | `{schedules: [{id, script_id, script_name, name, cron, target, parameters, timeout_s, expires_in_s, enabled, check_type, warning_exit_codes, next_run_at, last_run_at, created_at}]}` |
| POST /schedules | scripts.execute | `{script_id, name, cron, target, parameters?, timeout_s?, expires_in_s?, enabled?, check_type?, warning_exit_codes?, confirm_token?}` → 201 `{id, next_run_at}` |
| GET /schedules/{id} | scripts.read | one schedule object |
| PUT /schedules/{id} | scripts.execute | same body as POST → 200 |
| DELETE /schedules/{id} | scripts.execute | 204 |

`cron` is a 5-field expression or `@hourly`/`@daily`/`@weekly`/`@monthly`.
Creating or updating a schedule applies the same blast-radius 409
confirmation as dispatch, using the target's current resolution. Each
firing creates one job per active device in the target and is audited as
`schedule.run` (actor `system`). Missed firings (worker down) are not
replayed; the next run is computed from the current time.

### Health checks

A schedule with `check_type` other than `none` is a **health check**: it
runs its script through the normal job pipeline, and each completed job's
result is mapped to a per-device health state (`healthy` / `warning` /
`critical`). A device's overall `health` is the worst of its checks;
devices with no check results report `unknown`.

- `check_type: "exit_code"` — exit `0` is healthy, any code listed in
  `warning_exit_codes` is a warning, anything else (including timeout or
  failure) is critical.
- `check_type: "output"` — the script prints a `HEALTH=healthy|warning|critical`
  token on stdout (case-insensitive, `ok`/`pass`/`warn`/`fail` aliases
  accepted); the last match wins. No token yields `unknown`.

Results are read via `GET /devices/{id}/health`.

## Audit log

| Method/Path | Permission | Response |
|---|---|---|
| GET /audit?limit=50&before=RFC3339 | audit.read | `{entries: [{id, actor_type, actor_id, action, target_type, target_id, ip, details, created_at}]}` newest first |
