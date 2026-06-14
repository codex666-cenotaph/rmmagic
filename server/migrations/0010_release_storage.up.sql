-- M6 follow-up: serve agent release binaries from the control plane so a
-- private source repo / auth-walled artifact host doesn't block updates.
--
-- storage_key references the binary in the server's blob storage (fs or
-- s3/minio). url becomes optional: server-hosted releases have no external
-- URL (agents download from /agent/v1/releases/{id}/download instead).
-- A release is created as metadata first, then its binary is uploaded
-- (sets storage_key); rollout refuses a release that has neither, so no
-- table-level location constraint is enforced here.

ALTER TABLE agent_releases ADD COLUMN storage_key text;
ALTER TABLE agent_releases ALTER COLUMN url DROP NOT NULL;
