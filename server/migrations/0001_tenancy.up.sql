-- Tenancy foundation: MSP (tenant) -> customer -> site hierarchy, users,
-- RBAC, API tokens, audit log.
--
-- Isolation strategy: app-level scoping is the primary mechanism; row
-- level security is the backstop. The application connects as the
-- non-superuser role "rmm_app" and runs
--   SET LOCAL app.tenant_id = '<uuid>'
-- inside every transaction (LOCAL only — connections are pooled).
-- A missing or wrong setting makes tenant-scoped queries return nothing
-- instead of leaking cross-tenant data.

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE tenants (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    slug        text NOT NULL UNIQUE,
    status      text NOT NULL DEFAULT 'active'
                CHECK (status IN ('active', 'suspended')),
    mfa_required boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE customers (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    name        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX customers_tenant_idx ON customers (tenant_id);

CREATE TABLE sites (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    customer_id uuid NOT NULL REFERENCES customers(id),
    name        text NOT NULL,
    timezone    text NOT NULL DEFAULT 'UTC',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sites_tenant_idx ON sites (tenant_id);
CREATE INDEX sites_customer_idx ON sites (customer_id);

CREATE TABLE users (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    email           citext NOT NULL,
    password_hash   text NOT NULL,           -- argon2id encoded
    mfa_secret_enc  bytea,                   -- AES-256-GCM envelope-encrypted TOTP secret
    mfa_enabled     boolean NOT NULL DEFAULT false,
    status          text NOT NULL DEFAULT 'active'
                    CHECK (status IN ('invited', 'active', 'disabled')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, email)
);
CREATE INDEX users_tenant_idx ON users (tenant_id);

CREATE TABLE mfa_recovery_codes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash   text NOT NULL,
    used_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mfa_recovery_codes_user_idx ON mfa_recovery_codes (user_id);

-- Roles are named permission sets. Built-in roles have is_builtin=true
-- and are seeded per tenant at creation time.
CREATE TABLE roles (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    name        text NOT NULL,
    permissions text[] NOT NULL,
    is_builtin  boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

-- A role assignment grants a role at a scope: the whole tenant, one
-- customer, or one site. Effective permissions are the union over all
-- assignments whose scope contains the target resource.
CREATE TABLE role_assignments (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id     uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    scope_type  text NOT NULL CHECK (scope_type IN ('tenant', 'customer', 'site')),
    scope_id    uuid,  -- NULL only when scope_type = 'tenant'
    created_at  timestamptz NOT NULL DEFAULT now(),
    CHECK ((scope_type = 'tenant') = (scope_id IS NULL))
);
CREATE INDEX role_assignments_user_idx ON role_assignments (user_id);

CREATE TABLE api_tokens (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            text NOT NULL,
    token_hash      bytea NOT NULL UNIQUE,   -- sha256 of the secret part
    permissions     text[] NOT NULL,         -- subset of the owner's permissions
    scope_type      text NOT NULL DEFAULT 'tenant'
                    CHECK (scope_type IN ('tenant', 'customer', 'site')),
    scope_id        uuid,
    last_used_at    timestamptz,
    expires_at      timestamptz,
    revoked_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX api_tokens_tenant_idx ON api_tokens (tenant_id);

CREATE TABLE sessions (
    token_hash  bytea PRIMARY KEY,           -- sha256 of the session token
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mfa_passed  boolean NOT NULL DEFAULT false,
    ip          inet,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

-- Append-only audit log. The app role gets INSERT and SELECT only;
-- UPDATE/DELETE are not granted to anyone but the migration owner.
-- Partitioned monthly by created_at.
CREATE TABLE audit_log (
    id          uuid NOT NULL DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL,
    actor_type  text NOT NULL CHECK (actor_type IN ('user', 'api_token', 'device', 'system')),
    actor_id    uuid,
    action      text NOT NULL,               -- e.g. 'user.login', 'script.execute'
    target_type text,
    target_id   uuid,
    ip          inet,
    details     jsonb NOT NULL DEFAULT '{}', -- secrets must be redacted before insert
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);
CREATE INDEX audit_log_tenant_time_idx ON audit_log (tenant_id, created_at DESC);

-- Initial partitions; the worker creates future ones ahead of time.
CREATE TABLE audit_log_default PARTITION OF audit_log DEFAULT;

-- ---------------------------------------------------------------------
-- Application role + row level security
-- ---------------------------------------------------------------------

DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'rmm_app') THEN
        CREATE ROLE rmm_app NOLOGIN;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO rmm_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON
    tenants, customers, sites, users, mfa_recovery_codes,
    roles, role_assignments, api_tokens, sessions
TO rmm_app;
-- Audit log is append-only for the app.
GRANT SELECT, INSERT ON audit_log, audit_log_default TO rmm_app;

-- current_tenant_id() returns NULL when unset, which makes every RLS
-- predicate false: forgetting to set the tenant fails closed.
CREATE FUNCTION current_tenant_id() RETURNS uuid
    LANGUAGE sql STABLE
    AS $$ SELECT nullif(current_setting('app.tenant_id', true), '')::uuid $$;

DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'customers', 'sites', 'users', 'mfa_recovery_codes', 'roles',
        'role_assignments', 'api_tokens', 'sessions', 'audit_log'
    ] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I TO rmm_app
             USING (tenant_id = current_tenant_id())
             WITH CHECK (tenant_id = current_tenant_id())', t);
    END LOOP;
END
$$;

-- tenants itself: the app may only see the active tenant row. Auth
-- bootstrap (login: email -> tenant) uses SECURITY DEFINER helpers or a
-- separate unscoped role; never broad SELECT on tenant-scoped tables.
ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenants FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_self ON tenants TO rmm_app
    USING (id = current_tenant_id())
    WITH CHECK (id = current_tenant_id());
