-- M6 follow-up: serve agent release binaries from the control plane so a
-- private source repo / auth-walled artifact host doesn't block updates.
--
-- storage_key references the binary in the server's blob storage (fs or
-- s3/minio). url becomes optional: server-hosted releases have no external
-- URL (agents download from /agent/v1/releases/{id}/download instead).
-- A release is created as metadata first, then its binary is uploaded
-- (sets storage_key); rollout refuses a release that has neither, so no
-- table-level location constraint is enforced here.
--
-- Idempotent: this migration may have already run as 0009 on a server that
-- was then upgraded (migration renumbered after merging main's 0007_device_tags).

ALTER TABLE agent_releases ADD COLUMN IF NOT EXISTS storage_key text;
DO $$ BEGIN
  IF (SELECT is_nullable FROM information_schema.columns
      WHERE table_name='agent_releases' AND column_name='url') = 'NO' THEN
    ALTER TABLE agent_releases ALTER COLUMN url DROP NOT NULL;
  END IF;
END $$;
