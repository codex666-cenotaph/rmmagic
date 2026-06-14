-- M6 follow-up: the agent.update permission was added to the built-in
-- Owner/Admin role definitions after these roles were already seeded for
-- existing tenants. Back-fill it so release registration / rollout (which
-- require agent.update) become usable. Idempotent; safe to re-run.
UPDATE roles
SET permissions = array_append(permissions, 'agent.update')
WHERE is_builtin = true
  AND name IN ('Owner', 'Admin')
  AND NOT ('agent.update' = ANY (permissions));
