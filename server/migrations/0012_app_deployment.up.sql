-- M7: rule-based app deployment.
--
-- Replaces the "deploy packages to a target" one-shot (handleDeployApp) as the
-- primary workflow with two centrally-managed objects:
--
--   app_packages          a reusable, OS-specific app definition: what to
--                         install (install spec, forwarded verbatim to the
--                         agent as a package_install job) and how to tell it
--                         is already present (detection: by package/app name
--                         or an explicit name list).
--
--   app_deployment_rules  binds one package to a scope (tenant / customer /
--                         site / device) with tag and hostname filters. The
--                         worker reconciles each enabled rule hourly: it
--                         resolves the scope, applies the filters, skips
--                         devices that already have the app (detection) or
--                         that already have an install in flight, and creates
--                         package_install jobs for the rest.
--
-- The ad-hoc POST /apps/deploy path stays for one-off installs; rules are the
-- managed, continuously-reconciled layer on top of the same job pipeline.

CREATE TABLE app_packages (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid        NOT NULL REFERENCES tenants(id),
    name         text        NOT NULL CHECK (length(trim(name)) > 0),
    description  text        NOT NULL DEFAULT '',
    -- target platform; a deployment rule only ever offers a package to
    -- devices reporting this os (devices.os).
    os           text        NOT NULL DEFAULT 'linux'
                 CHECK (os IN ('linux', 'windows', 'darwin')),
    -- what to install: {"packages": ["nginx", "curl"]}, forwarded as the
    -- package_install job spec.
    install      jsonb       NOT NULL DEFAULT '{"packages":[]}',
    -- how to tell it is already installed:
    --   {"method": "package_name", "names": ["nginx"]}
    -- an empty names list falls back to install.packages (i.e. "by app name").
    detection    jsonb       NOT NULL DEFAULT '{"method":"package_name","names":[]}',
    timeout_s    int         NOT NULL DEFAULT 600 CHECK (timeout_s BETWEEN 1 AND 86400),
    archived_at  timestamptz,
    created_by   uuid        REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE app_packages ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON app_packages USING (tenant_id = current_tenant_id());
CREATE INDEX app_packages_tenant_idx ON app_packages (tenant_id, name);

CREATE TABLE app_deployment_rules (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid        NOT NULL REFERENCES tenants(id),
    package_id   uuid        NOT NULL REFERENCES app_packages(id) ON DELETE CASCADE,
    name         text        NOT NULL CHECK (length(trim(name)) > 0),
    -- scope this rule targets; mirrors the policies scoping model minus tag
    -- (tag selection lives in filters so it can compose with a scope).
    scope_type   text        NOT NULL CHECK (scope_type IN ('tenant','customer','site','device')),
    scope_id     uuid,
    -- {"tags": ["server"], "tags_match": "any"|"all", "hostname_regex": "..."}
    filters      jsonb       NOT NULL DEFAULT '{}',
    enabled      boolean     NOT NULL DEFAULT true,
    -- worker reconciliation bookkeeping: claimed hourly with SKIP LOCKED.
    last_run_at  timestamptz,
    created_by   uuid        REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    -- tenant scope carries no id; every other scope must name its target.
    CHECK ((scope_type = 'tenant') = (scope_id IS NULL))
);
ALTER TABLE app_deployment_rules ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON app_deployment_rules USING (tenant_id = current_tenant_id());
CREATE INDEX app_deployment_rules_tenant_idx ON app_deployment_rules (tenant_id, name);
-- Due-scan index: the worker only looks at enabled rules ordered by staleness.
CREATE INDEX app_deployment_rules_due_idx ON app_deployment_rules (last_run_at) WHERE enabled;

GRANT SELECT, INSERT, UPDATE, DELETE ON app_packages         TO rmm_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON app_deployment_rules TO rmm_app;

-- Back-fill the new permissions onto the already-seeded built-in roles so
-- existing tenants can read/manage packages and rules. Idempotent.
UPDATE roles SET permissions = array_append(permissions, 'apps.read')
WHERE is_builtin = true
  AND name IN ('Owner', 'Admin', 'Technician', 'Read-only')
  AND NOT ('apps.read' = ANY (permissions));
UPDATE roles SET permissions = array_append(permissions, 'apps.manage')
WHERE is_builtin = true
  AND name IN ('Owner', 'Admin')
  AND NOT ('apps.manage' = ANY (permissions));
