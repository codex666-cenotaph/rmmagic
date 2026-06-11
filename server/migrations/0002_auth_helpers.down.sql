DROP FUNCTION IF EXISTS auth_lookup_api_token(bytea);
DROP FUNCTION IF EXISTS auth_lookup_session(bytea);
DROP FUNCTION IF EXISTS auth_lookup_user(citext);
DROP INDEX IF EXISTS users_email_unique;
