-- Drop tag-scoped policies before narrowing the constraint back, else
-- re-adding the old whitelist would fail on existing 'tag' rows.
DELETE FROM policies WHERE scope_type = 'tag';

ALTER TABLE policies DROP CONSTRAINT policies_scope_target_check;
ALTER TABLE policies ADD CONSTRAINT policies_check
    CHECK ((scope_type = 'tenant') = (scope_id IS NULL));

ALTER TABLE policies DROP CONSTRAINT policies_scope_type_check;
ALTER TABLE policies ADD CONSTRAINT policies_scope_type_check
    CHECK (scope_type IN ('tenant','customer','site','device'));

ALTER TABLE policies DROP COLUMN scope_tag;

DROP INDEX devices_tags_idx;
ALTER TABLE devices DROP COLUMN tags;
