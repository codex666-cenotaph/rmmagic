-- AI assistant settings: per-tenant configuration for the in-dashboard
-- assistant. The API key is stored sealed (secrets.Box), never in plain
-- text, and never returned by the API. One row per tenant.
CREATE TABLE assistant_settings (
    tenant_id   uuid PRIMARY KEY REFERENCES tenants(id),
    enabled     boolean NOT NULL DEFAULT false,
    provider    text NOT NULL DEFAULT 'anthropic'
                    CHECK (provider IN ('anthropic', 'mistral')),
    model       text NOT NULL DEFAULT '',
    api_key_enc bytea,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

GRANT SELECT, INSERT, UPDATE, DELETE ON assistant_settings TO rmm_app;

ALTER TABLE assistant_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE assistant_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON assistant_settings TO rmm_app
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());
