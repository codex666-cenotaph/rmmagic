-- M3 completion: job expiry, cron schedules, worker tenant iteration.
--
-- jobs.expires_at: after this time a queued (pending/sent) job must not
-- be started; the worker sweeps it to status 'expired' and the gateway
-- stops (re-)delivering it.
--
-- schedules: cron-style recurring dispatch of a script to a target
-- selector (explicit device list, one site, or one customer). The worker
-- claims due schedules with FOR UPDATE SKIP LOCKED so multiple worker
-- processes never double-fire.

ALTER TABLE jobs
    ADD COLUMN expires_at timestamptz NOT NULL DEFAULT now() + interval '24 hours';
-- Sweep index: the expiry scan only ever looks at queued jobs.
CREATE INDEX jobs_queued_expiry_idx ON jobs (expires_at)
    WHERE status IN ('pending', 'sent');

CREATE TABLE schedules (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid        NOT NULL REFERENCES tenants(id),
    script_id    uuid        NOT NULL REFERENCES scripts(id),
    name         text        NOT NULL CHECK (length(trim(name)) > 0),
    -- 5-field cron expression or @hourly/@daily/@weekly/@monthly, UTC.
    cron         text        NOT NULL,
    -- {"device_ids": [...]} | {"site_id": "..."} | {"customer_id": "..."}
    target       jsonb       NOT NULL,
    parameters   jsonb       NOT NULL DEFAULT '{}',
    timeout_s    int         NOT NULL DEFAULT 300 CHECK (timeout_s BETWEEN 1 AND 86400),
    -- per-run job expiry window
    expires_in_s int         NOT NULL DEFAULT 86400 CHECK (expires_in_s BETWEEN 60 AND 604800),
    enabled      boolean     NOT NULL DEFAULT true,
    next_run_at  timestamptz NOT NULL,
    last_run_at  timestamptz,
    created_by   uuid        REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE schedules ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON schedules USING (tenant_id = current_tenant_id());
CREATE INDEX ON schedules (tenant_id, name);
CREATE INDEX schedules_due_idx ON schedules (next_run_at) WHERE enabled;

-- Schedule-created jobs carry their origin for the run history UI.
ALTER TABLE jobs
    ADD COLUMN schedule_id uuid REFERENCES schedules(id) ON DELETE SET NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON schedules TO rmm_app;

-- The worker iterates tenants to run per-tenant maintenance (schedule
-- firing, job expiry) inside a normal RLS-scoped transaction per tenant.
-- Same narrow SECURITY DEFINER pattern as the auth_lookup_* helpers:
-- it exposes only active tenant IDs, never tenant data.
CREATE FUNCTION worker_list_tenants()
RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public
AS $$
    SELECT id FROM tenants WHERE status = 'active'
$$;
REVOKE ALL ON FUNCTION worker_list_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION worker_list_tenants() TO rmm_app;
