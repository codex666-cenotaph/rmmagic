-- M2: devices, enrollment tokens, device credentials, device stats.

CREATE TABLE enrollment_tokens (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    site_id     uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    token_hash  bytea NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    max_uses    integer NOT NULL DEFAULT 1 CHECK (max_uses > 0),
    use_count   integer NOT NULL DEFAULT 0,
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    revoked_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX enrollment_tokens_tenant_idx ON enrollment_tokens (tenant_id);

CREATE TABLE devices (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id),
    site_id       uuid NOT NULL REFERENCES sites(id),
    hostname      text NOT NULL,
    os            text NOT NULL,
    arch          text NOT NULL,
    agent_version text NOT NULL DEFAULT '',
    status        text NOT NULL DEFAULT 'active'
                  CHECK (status IN ('active', 'decommissioned')),
    last_seen_at  timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX devices_tenant_idx ON devices (tenant_id);
CREATE INDEX devices_site_idx ON devices (site_id);

-- History of device public keys; the active credential is the one with
-- revoked_at IS NULL. Fingerprint = sha256(pubkey).
CREATE TABLE device_credentials (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    device_id   uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    pubkey      bytea NOT NULL,
    fingerprint bytea NOT NULL UNIQUE,
    issued_at   timestamptz NOT NULL DEFAULT now(),
    revoked_at  timestamptz
);
CREATE INDEX device_credentials_device_idx ON device_credentials (device_id);

-- Hot telemetry. Partitioned by time; rollups and external TSDB come
-- with the telemetry-scaling work — the ingest path is already isolated
-- behind the store interface.
CREATE TABLE device_stats (
    tenant_id  uuid NOT NULL,
    device_id  uuid NOT NULL,
    ts         timestamptz NOT NULL,
    cpu_pct    real NOT NULL,
    mem_used   bigint NOT NULL,
    mem_total  bigint NOT NULL,
    disks      jsonb NOT NULL DEFAULT '[]',
    net        jsonb NOT NULL DEFAULT '{}',
    PRIMARY KEY (device_id, ts)
) PARTITION BY RANGE (ts);
CREATE INDEX device_stats_tenant_idx ON device_stats (tenant_id, ts);

-- Catch-all plus explicit partitions around "now"; the worker will own
-- forward partition creation later.
CREATE TABLE device_stats_default PARTITION OF device_stats DEFAULT;
DO $$
DECLARE
    m date := date_trunc('month', now())::date;
BEGIN
    FOR i IN 0..2 LOOP
        EXECUTE format(
            'CREATE TABLE device_stats_%s PARTITION OF device_stats
             FOR VALUES FROM (%L) TO (%L)',
            to_char(m + (i || ' months')::interval, 'YYYYMM'),
            m + (i || ' months')::interval,
            m + ((i + 1) || ' months')::interval);
    END LOOP;
END
$$;

-- ---------------------------------------------------------------------
-- Grants + RLS
-- ---------------------------------------------------------------------

GRANT SELECT, INSERT, UPDATE, DELETE ON enrollment_tokens, devices, device_credentials TO rmm_app;
-- Telemetry is append-only for the app role. Access always goes through
-- the partition parent, so no per-partition grants are needed.
GRANT SELECT, INSERT ON device_stats TO rmm_app;

DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'enrollment_tokens', 'devices', 'device_credentials', 'device_stats'
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

-- ---------------------------------------------------------------------
-- Pre-tenant lookups (same narrow SECURITY DEFINER pattern as 0002):
-- agent connections and enrollment happen before a tenant scope exists.
-- ---------------------------------------------------------------------

CREATE FUNCTION auth_lookup_enrollment_token(p_token_hash bytea)
RETURNS TABLE (
    token_id uuid, tenant_id uuid, site_id uuid,
    expires_at timestamptz, max_uses integer, use_count integer, revoked_at timestamptz
)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public
AS $$
    SELECT t.id, t.tenant_id, t.site_id, t.expires_at, t.max_uses, t.use_count, t.revoked_at
    FROM enrollment_tokens t WHERE t.token_hash = p_token_hash
$$;

CREATE FUNCTION auth_lookup_device(p_device_id uuid)
RETURNS TABLE (
    tenant_id uuid, status text, pubkey bytea
)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public
AS $$
    SELECT d.tenant_id, d.status, c.pubkey
    FROM devices d
    JOIN device_credentials c ON c.device_id = d.id AND c.revoked_at IS NULL
    WHERE d.id = p_device_id
$$;

REVOKE ALL ON FUNCTION auth_lookup_enrollment_token(bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_lookup_device(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION auth_lookup_enrollment_token(bytea) TO rmm_app;
GRANT EXECUTE ON FUNCTION auth_lookup_device(uuid) TO rmm_app;
