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
| GET /devices | devices.read | `{devices: [{id, site_id, site_name, customer_id, customer_name, hostname, os, arch, agent_version, status: "active"\|"decommissioned", online: bool, last_seen_at, created_at}]}` (scope-filtered) |
| GET /devices/{id} | devices.read | one device object (shape above) |
| GET /devices/{id}/stats?since=RFC3339&until=RFC3339 | devices.read | `{samples: [{ts, cpu_pct, mem_used, mem_total, disks: [{mount, used, total}], net: {rx_bytes, tx_bytes}}]}` ascending by ts; default window = last hour |
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
| GET /jobs?device_id= | scripts.read | `{jobs: [{id, script_id, script_name, device_id, hostname, command_id, status, timeout_s, language, parameters, schedule_id?, created_at, expires_at, sent_at?, started_at?, finished_at?}]}` newest first, scope-filtered |
| GET /jobs/{id} | scripts.read | one job object |
| GET /jobs/{id}/output | scripts.read | `{output, exit_code}` |

Job statuses: `pending` (queued, device offline) → `sent` → terminal
`succeeded`/`failed`/`timed_out`/`expired`. The worker sweeps queued
jobs past `expires_at` to `expired`.

## Schedules

Cron-style recurring dispatch, evaluated in UTC by the worker role.

| Method/Path | Permission | Body / Response |
|---|---|---|
| GET /schedules | scripts.read | `{schedules: [{id, script_id, script_name, name, cron, target, parameters, timeout_s, expires_in_s, enabled, next_run_at, last_run_at, created_at}]}` |
| POST /schedules | scripts.execute | `{script_id, name, cron, target, parameters?, timeout_s?, expires_in_s?, enabled?, confirm_token?}` → 201 `{id, next_run_at}` |
| GET /schedules/{id} | scripts.read | one schedule object |
| PUT /schedules/{id} | scripts.execute | same body as POST → 200 |
| DELETE /schedules/{id} | scripts.execute | 204 |

`cron` is a 5-field expression or `@hourly`/`@daily`/`@weekly`/`@monthly`.
Creating or updating a schedule applies the same blast-radius 409
confirmation as dispatch, using the target's current resolution. Each
firing creates one job per active device in the target and is audited as
`schedule.run` (actor `system`). Missed firings (worker down) are not
replayed; the next run is computed from the current time.

## Audit log

| Method/Path | Permission | Response |
|---|---|---|
| GET /audit?limit=50&before=RFC3339 | audit.read | `{entries: [{id, actor_type, actor_id, action, target_type, target_id, ip, details, created_at}]}` newest first |
