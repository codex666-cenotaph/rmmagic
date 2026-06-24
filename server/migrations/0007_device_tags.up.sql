-- M4+: device tags + tag-scoped monitoring policies.
--
-- Tags label devices by role (e.g. 'server') independent of their org
-- placement. Policies can then target a tag instead of a tenant/site:
-- a policy scoped to tag 'server' with an offline rule fires whenever
-- any tagged device goes offline, which is how always-on servers get an
-- outage alert that sleeping desktops/laptops do not.

ALTER TABLE devices ADD COLUMN tags text[] NOT NULL DEFAULT '{}';
-- GIN index so "devices carrying tag X" stays cheap as the fleet grows.
CREATE INDEX devices_tags_idx ON devices USING gin (tags);

-- Tag-scoped policies target a tag name rather than an org node, so the
-- target lives in scope_tag (scope_id stays NULL for them). Widen the
-- scope_type whitelist and replace the old tenant-only scope_id check
-- with one that covers all five scope kinds.
ALTER TABLE policies ADD COLUMN scope_tag text;

ALTER TABLE policies DROP CONSTRAINT policies_scope_type_check;
ALTER TABLE policies ADD CONSTRAINT policies_scope_type_check
    CHECK (scope_type IN ('tenant','customer','site','device','tag'));

ALTER TABLE policies DROP CONSTRAINT policies_check;
ALTER TABLE policies ADD CONSTRAINT policies_scope_target_check CHECK (
    (scope_type = 'tenant'
        AND scope_id IS NULL AND scope_tag IS NULL)
    OR (scope_type IN ('customer','site','device')
        AND scope_id IS NOT NULL AND scope_tag IS NULL)
    OR (scope_type = 'tag'
        AND scope_id IS NULL AND scope_tag IS NOT NULL
        AND length(trim(scope_tag)) > 0)
);
