DROP FUNCTION IF EXISTS auth_lookup_device(uuid);
DROP FUNCTION IF EXISTS auth_lookup_enrollment_token(bytea);
DROP TABLE IF EXISTS device_stats CASCADE;
DROP TABLE IF EXISTS device_credentials;
DROP TABLE IF EXISTS devices;
DROP TABLE IF EXISTS enrollment_tokens;
