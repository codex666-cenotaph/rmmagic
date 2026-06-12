# API v1 — M1 surface

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

## Audit log

| Method/Path | Permission | Response |
|---|---|---|
| GET /audit?limit=50&before=RFC3339 | audit.read | `{entries: [{id, actor_type, actor_id, action, target_type, target_id, ip, details, created_at}]}` newest first |
