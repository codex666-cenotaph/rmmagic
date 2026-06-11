-- Auth bootstrap helpers. Login and session/token resolution happen
-- before the tenant is known, but RLS (correctly) blocks unscoped
-- queries from rmm_app. These SECURITY DEFINER functions are the only
-- sanctioned holes: each is a narrow point lookup by unique key that
-- returns the tenant_id needed to establish the scoped transaction.

-- Email becomes globally unique: login identifies the user (and thus
-- the tenant) by email alone.
CREATE UNIQUE INDEX users_email_unique ON users (email);

CREATE FUNCTION auth_lookup_user(p_email citext)
RETURNS TABLE (
    user_id uuid, tenant_id uuid, password_hash text,
    mfa_enabled boolean, status text, tenant_status text
)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public
AS $$
    SELECT u.id, u.tenant_id, u.password_hash, u.mfa_enabled, u.status, t.status
    FROM users u JOIN tenants t ON t.id = u.tenant_id
    WHERE u.email = p_email
$$;

CREATE FUNCTION auth_lookup_session(p_token_hash bytea)
RETURNS TABLE (
    tenant_id uuid, user_id uuid, mfa_passed boolean, expires_at timestamptz
)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public
AS $$
    SELECT s.tenant_id, s.user_id, s.mfa_passed, s.expires_at
    FROM sessions s
    WHERE s.token_hash = p_token_hash AND s.expires_at > now()
$$;

CREATE FUNCTION auth_lookup_api_token(p_token_hash bytea)
RETURNS TABLE (
    token_id uuid, tenant_id uuid, user_id uuid, permissions text[],
    scope_type text, scope_id uuid, expires_at timestamptz, revoked_at timestamptz
)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public
AS $$
    SELECT t.id, t.tenant_id, t.user_id, t.permissions,
           t.scope_type, t.scope_id, t.expires_at, t.revoked_at
    FROM api_tokens t
    WHERE t.token_hash = p_token_hash
$$;

REVOKE ALL ON FUNCTION auth_lookup_user(citext) FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_lookup_session(bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_lookup_api_token(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION auth_lookup_user(citext) TO rmm_app;
GRANT EXECUTE ON FUNCTION auth_lookup_session(bytea) TO rmm_app;
GRANT EXECUTE ON FUNCTION auth_lookup_api_token(bytea) TO rmm_app;
