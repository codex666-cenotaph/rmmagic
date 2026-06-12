-- M3: Script library and job dispatch.
-- scripts: versioned library of runnable scripts.
-- jobs: each dispatch of a script to one device, with an idempotent
--       command_id (= job UUID) so agents deduplicate redelivery.
-- job_outputs: captured output, written once on completion.

CREATE TABLE scripts (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid        NOT NULL REFERENCES tenants(id),
    name        text        NOT NULL CHECK (length(trim(name)) > 0),
    description text        NOT NULL DEFAULT '',
    language    text        NOT NULL CHECK (language IN ('bash','powershell','python','batch')),
    body        text        NOT NULL,
    -- parameters: [{name, description, default, required}]
    parameters  jsonb       NOT NULL DEFAULT '[]',
    version     int         NOT NULL DEFAULT 1,
    archived_at timestamptz,
    created_by  uuid        REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE scripts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON scripts USING (tenant_id = current_tenant_id());
CREATE INDEX ON scripts (tenant_id, archived_at, name);

CREATE TABLE jobs (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid        NOT NULL REFERENCES tenants(id),
    script_id   uuid        NOT NULL REFERENCES scripts(id),
    device_id   uuid        NOT NULL REFERENCES devices(id),
    -- command_id is stable across redeliveries; agents use it as the
    -- idempotency key in their local journal.
    command_id  text        NOT NULL DEFAULT gen_random_uuid()::text UNIQUE,
    status      text        NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending','sent','running','succeeded','failed','timed_out','expired')),
    timeout_s   int         NOT NULL DEFAULT 300 CHECK (timeout_s BETWEEN 1 AND 86400),
    -- snapshot of the script at dispatch time so edits don't change in-flight jobs.
    script_body text        NOT NULL,
    language    text        NOT NULL,
    parameters  jsonb       NOT NULL DEFAULT '{}',
    created_by  uuid        REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    sent_at     timestamptz,
    started_at  timestamptz,
    finished_at timestamptz
);
ALTER TABLE jobs ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON jobs USING (tenant_id = current_tenant_id());
CREATE INDEX ON jobs (device_id, status, created_at DESC);
CREATE INDEX ON jobs (tenant_id, created_at DESC);

CREATE TABLE job_outputs (
    job_id    uuid PRIMARY KEY REFERENCES jobs(id),
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    -- combined stdout+stderr captured by the agent (≤ 1 MiB)
    output    text NOT NULL DEFAULT '',
    exit_code int
);
ALTER TABLE job_outputs ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON job_outputs USING (tenant_id = current_tenant_id());

GRANT SELECT, INSERT, UPDATE ON scripts    TO rmm_app;
GRANT SELECT, INSERT, UPDATE ON jobs       TO rmm_app;
GRANT SELECT, INSERT          ON job_outputs TO rmm_app;
