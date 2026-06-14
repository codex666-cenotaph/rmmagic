-- M6: App deployment (apt/dnf package jobs) and signed agent auto-update.
--
-- jobs gains a `kind` discriminator so the existing dispatch/offline-queue/
-- result machinery carries package install/remove jobs alongside scripts.
-- Script-only columns become nullable; package jobs use `spec` instead.
--
-- agent_releases is a GLOBAL catalog (no tenant_id, no RLS): the signed
-- binaries every tenant's agents update to. Rows hold the download URL,
-- sha256, and a detached Ed25519 signature the agent verifies against its
-- embedded trusted public key(s) before swapping binaries.
--
-- device_updates tracks the latest rollout offered to each device and the
-- phase the agent reports back over the channel (offer/verify/swap/rollback).

-- --- package jobs fold into the jobs table ---------------------------------

ALTER TABLE jobs
    ADD COLUMN kind text NOT NULL DEFAULT 'script'
        CHECK (kind IN ('script', 'package_install', 'package_remove'));

-- Package jobs have no script; relax the script-only NOT NULLs and add the
-- generic spec payload (e.g. {"packages": ["nginx", "curl"]}).
ALTER TABLE jobs ALTER COLUMN script_id   DROP NOT NULL;
ALTER TABLE jobs ALTER COLUMN script_body DROP NOT NULL;
ALTER TABLE jobs ALTER COLUMN language    DROP NOT NULL;
ALTER TABLE jobs ADD COLUMN spec jsonb;

-- Integrity: a script job must carry its script snapshot; a package job
-- must carry a spec. Enforced in one CHECK so neither kind can be malformed.
ALTER TABLE jobs ADD CONSTRAINT jobs_kind_payload_ck CHECK (
    (kind = 'script'
        AND script_id IS NOT NULL AND script_body IS NOT NULL AND language IS NOT NULL)
    OR
    (kind IN ('package_install', 'package_remove') AND spec IS NOT NULL)
);

-- --- global signed-release catalog ----------------------------------------

CREATE TABLE agent_releases (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- update channel; devices follow one channel (devices.update_channel).
    channel      text        NOT NULL DEFAULT 'stable'
                 CHECK (channel IN ('stable', 'beta')),
    version      text        NOT NULL CHECK (length(trim(version)) > 0),
    os           text        NOT NULL CHECK (length(trim(os)) > 0),
    arch         text        NOT NULL CHECK (length(trim(arch)) > 0),
    -- HTTPS location the agent downloads the binary from.
    url          text        NOT NULL CHECK (url LIKE 'https://%' OR url LIKE 'http://%'),
    -- lowercase hex sha256 of the binary; verified before swap.
    sha256       text        NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    -- base64 detached Ed25519 signature over the raw binary bytes.
    signature    text        NOT NULL CHECK (length(trim(signature)) > 0),
    size_bytes   bigint      NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    notes        text        NOT NULL DEFAULT '',
    -- who registered it (tenant user id; no FK — this is a global table).
    created_by   uuid,
    created_at   timestamptz NOT NULL DEFAULT now(),
    -- one published binary per (channel, os, arch, version).
    UNIQUE (channel, os, arch, version)
);
-- No RLS: this catalog is intentionally global. Index the lookup the
-- rollout/offer path makes most: newest release for a channel+platform.
CREATE INDEX agent_releases_lookup_idx
    ON agent_releases (channel, os, arch, created_at DESC);

-- --- per-device update channel + rollout tracking -------------------------

ALTER TABLE devices
    ADD COLUMN update_channel text NOT NULL DEFAULT 'stable'
        CHECK (update_channel IN ('stable', 'beta'));

CREATE TABLE device_updates (
    device_id  uuid        PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    tenant_id  uuid        NOT NULL REFERENCES tenants(id),
    -- the version the device was offered / is moving to.
    version    text        NOT NULL,
    phase      text        NOT NULL DEFAULT 'offered'
               CHECK (phase IN ('offered', 'downloading', 'verified',
                                'applied', 'rolled_back', 'failed')),
    error      text        NOT NULL DEFAULT '',
    offered_by uuid,
    offered_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE device_updates ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON device_updates USING (tenant_id = current_tenant_id());

GRANT SELECT, INSERT, UPDATE          ON agent_releases TO rmm_app;
GRANT SELECT, INSERT, UPDATE, DELETE  ON device_updates  TO rmm_app;
