-- M4: HW/SW inventory, monitoring policies, alerts, notifications.
--
-- inventory_hw / inventory_sw: one row per device, replaced on each
--   agent upload (start, every 12h, INVENTORY_REFRESH command).
-- device_services: latest systemd service states, refreshed with every
--   stats upload so service-down evaluation works on fresh data.
-- policies: monitoring rules at tenant/customer/site/device scope; the
--   worker merges them per device with the most specific scope winning
--   per rule type.
-- alerts: lifecycle rows; dedup enforced by a partial unique index on
--   the open (firing) alert per dedup_key.
-- notification_channels + notification_deliveries: email / signed
--   webhook fan-out via a Postgres outbox (FOR UPDATE SKIP LOCKED).

CREATE TABLE inventory_hw (
    device_id    uuid PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    tenant_id    uuid NOT NULL REFERENCES tenants(id),
    data         jsonb NOT NULL DEFAULT '{}',
    collected_at timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX inventory_hw_tenant_idx ON inventory_hw (tenant_id);

CREATE TABLE inventory_sw (
    device_id    uuid PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    tenant_id    uuid NOT NULL REFERENCES tenants(id),
    -- [{name, version, arch}]
    packages     jsonb NOT NULL DEFAULT '[]',
    collected_at timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX inventory_sw_tenant_idx ON inventory_sw (tenant_id);

CREATE TABLE device_services (
    device_id  uuid PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    -- [{name, state}] from systemd; state e.g. running|exited|failed
    services   jsonb NOT NULL DEFAULT '[]',
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX device_services_tenant_idx ON device_services (tenant_id);

CREATE TABLE notification_channels (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    name       text NOT NULL CHECK (length(trim(name)) > 0),
    type       text NOT NULL CHECK (type IN ('email','webhook')),
    -- email: {recipients: [addr]}; webhook: {url}
    config     jsonb NOT NULL DEFAULT '{}',
    -- webhook HMAC signing secret, envelope-encrypted (secrets.Box)
    secret_enc bytea,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notification_channels_tenant_idx ON notification_channels (tenant_id);

CREATE TABLE policies (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    name        text NOT NULL CHECK (length(trim(name)) > 0),
    scope_type  text NOT NULL CHECK (scope_type IN ('tenant','customer','site','device')),
    scope_id    uuid,
    enabled     boolean NOT NULL DEFAULT true,
    -- {cpu_pct: {threshold, severity?}, mem_pct: {...}, disk_pct:
    --  {threshold, mounts?, severity?}, offline: {after_s, severity?},
    --  service_down: {services, severity?}}
    rules       jsonb NOT NULL DEFAULT '{}',
    channel_ids uuid[] NOT NULL DEFAULT '{}',
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CHECK ((scope_type = 'tenant') = (scope_id IS NULL))
);
CREATE INDEX policies_tenant_idx ON policies (tenant_id, scope_type);

CREATE TABLE alerts (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    device_id   uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    policy_id   uuid REFERENCES policies(id) ON DELETE SET NULL,
    rule_type   text NOT NULL CHECK (rule_type IN ('cpu_pct','mem_pct','disk_pct','offline','service_down')),
    -- device_id:rule_type[:mount|:service] — identity of the condition
    dedup_key   text NOT NULL,
    severity    text NOT NULL DEFAULT 'warning' CHECK (severity IN ('warning','critical')),
    message     text NOT NULL,
    details     jsonb NOT NULL DEFAULT '{}',
    -- channels captured at fire time so resolution notifies the same set
    channel_ids uuid[] NOT NULL DEFAULT '{}',
    status      text NOT NULL DEFAULT 'firing' CHECK (status IN ('firing','resolved')),
    fired_at    timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    acked_by    uuid REFERENCES users(id) ON DELETE SET NULL,
    acked_at    timestamptz
);
-- Dedup: at most one open alert per condition.
CREATE UNIQUE INDEX alerts_dedup_firing ON alerts (tenant_id, dedup_key) WHERE status = 'firing';
CREATE INDEX alerts_tenant_status_idx ON alerts (tenant_id, status, fired_at DESC);
CREATE INDEX alerts_device_idx ON alerts (device_id, fired_at DESC);

CREATE TABLE notification_deliveries (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    alert_id        uuid NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    channel_id      uuid NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    event           text NOT NULL CHECK (event IN ('fired','resolved')),
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sent','failed')),
    attempts        integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    sent_at         timestamptz
);
CREATE INDEX deliveries_due_idx ON notification_deliveries (tenant_id, next_attempt_at)
    WHERE status = 'pending';

-- ---------------------------------------------------------------------
-- Grants + RLS
-- ---------------------------------------------------------------------

GRANT SELECT, INSERT, UPDATE          ON inventory_hw, inventory_sw, device_services TO rmm_app;
GRANT SELECT, INSERT, UPDATE, DELETE  ON notification_channels, policies             TO rmm_app;
-- Alerts and deliveries are never deleted by the app role (history).
GRANT SELECT, INSERT, UPDATE          ON alerts, notification_deliveries             TO rmm_app;

DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'inventory_hw', 'inventory_sw', 'device_services',
        'notification_channels', 'policies', 'alerts', 'notification_deliveries'
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
