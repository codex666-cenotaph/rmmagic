UPDATE roles
SET permissions = array_remove(permissions, 'agent.update')
WHERE is_builtin = true
  AND name IN ('Owner', 'Admin');
