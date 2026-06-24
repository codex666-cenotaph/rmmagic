-- Health checks: reuse scripts + schedules to derive a per-device health
-- state (healthy / warning / critical). A schedule becomes a "health
-- check" when its check_type is not 'none'. The schedule fires its script
-- through the normal job pipeline; when each job completes the result is
-- mapped to a health status and recorded per (device, schedule). A
-- device's overall health is the worst of its checks.

-- How a check schedule's job result maps to a health status:
--   none      -- ordinary schedule, no health interpretation
--   exit_code -- exit 0 => healthy, code in warning_exit_codes => warning,
--                any other (incl. timeout/failure) => critical
--   output    -- a "HEALTH=healthy|warning|critical" token in the script's
--                stdout sets the status (last match wins); none => unknown
ALTER TABLE schedules
    ADD COLUMN check_type         text  NOT NULL DEFAULT 'none'
        CHECK (check_type IN ('none', 'exit_code', 'output')),
    ADD COLUMN warning_exit_codes int[] NOT NULL DEFAULT '{}';

-- Latest health result for one check on one device. Overwritten in place
-- each run; history lives in jobs/job_outputs (job_id points at the run).
CREATE TABLE device_health (
    tenant_id   uuid        NOT NULL REFERENCES tenants(id),
    device_id   uuid        NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    schedule_id uuid        NOT NULL REFERENCES schedules(id) ON DELETE CASCADE,
    status      text        NOT NULL CHECK (status IN ('healthy', 'warning', 'critical', 'unknown')),
    message     text        NOT NULL DEFAULT '',
    job_id      uuid        REFERENCES jobs(id) ON DELETE SET NULL,
    checked_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, schedule_id)
);
ALTER TABLE device_health ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON device_health USING (tenant_id = current_tenant_id());
CREATE INDEX device_health_device_idx ON device_health (device_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON device_health TO rmm_app;
