-- Reverse M6 app-deployment / auto-update schema.

DROP TABLE IF EXISTS device_updates;
DROP TABLE IF EXISTS agent_releases;

ALTER TABLE devices DROP COLUMN IF EXISTS update_channel;

-- Package jobs only exist with the columns added in the up migration;
-- remove them (and their outputs) before restoring the script-only NOT
-- NULLs, which they would otherwise violate.
DELETE FROM job_outputs WHERE job_id IN (SELECT id FROM jobs WHERE kind <> 'script');
DELETE FROM jobs WHERE kind <> 'script';

ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_kind_payload_ck;
ALTER TABLE jobs DROP COLUMN IF EXISTS spec;
ALTER TABLE jobs DROP COLUMN IF EXISTS kind;
ALTER TABLE jobs ALTER COLUMN script_id   SET NOT NULL;
ALTER TABLE jobs ALTER COLUMN script_body SET NOT NULL;
ALTER TABLE jobs ALTER COLUMN language    SET NOT NULL;
