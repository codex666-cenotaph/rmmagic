-- Reverse release blob storage. Server-hosted releases (no external url)
-- cannot satisfy the restored NOT NULL, so drop them first.
DELETE FROM agent_releases WHERE url IS NULL;
ALTER TABLE agent_releases ALTER COLUMN url SET NOT NULL;
ALTER TABLE agent_releases DROP COLUMN IF EXISTS storage_key;
