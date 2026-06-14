-- M5: interactive remote shell sessions.
--
-- shell_sessions: one row per PTY session a technician opens against a
--   device. The terminal stream is bridged browser<->server<->agent and
--   teed to an asciinema-format recording in object storage; recording_ref
--   is the storage key, set when the session is finalized. Rows are never
--   deleted by the app role (audit/history), mirroring alerts.
--   bytes_in/out account terminal traffic for the session summary.

CREATE TABLE shell_sessions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id),
    device_id     uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    -- the user (or API token owner) who opened the session
    opened_by     uuid REFERENCES users(id) ON DELETE SET NULL,
    status        text NOT NULL DEFAULT 'active'
                      CHECK (status IN ('active','closed','error')),
    cols          integer NOT NULL DEFAULT 80,
    rows          integer NOT NULL DEFAULT 24,
    client_ip     inet,
    recording_ref text,
    bytes_in      bigint NOT NULL DEFAULT 0,
    bytes_out     bigint NOT NULL DEFAULT 0,
    error         text,
    started_at    timestamptz NOT NULL DEFAULT now(),
    ended_at      timestamptz
);
CREATE INDEX shell_sessions_device_idx ON shell_sessions (device_id, started_at DESC);
CREATE INDEX shell_sessions_tenant_idx ON shell_sessions (tenant_id, started_at DESC);

-- ---------------------------------------------------------------------
-- Grants + RLS
-- ---------------------------------------------------------------------

-- History rows: the app role creates and finalizes sessions but never
-- deletes them.
GRANT SELECT, INSERT, UPDATE ON shell_sessions TO rmm_app;

ALTER TABLE shell_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE shell_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON shell_sessions TO rmm_app
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());
