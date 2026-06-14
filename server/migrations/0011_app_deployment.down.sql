UPDATE roles SET permissions = array_remove(permissions, 'apps.manage')
WHERE is_builtin = true AND name IN ('Owner', 'Admin');
UPDATE roles SET permissions = array_remove(permissions, 'apps.read')
WHERE is_builtin = true AND name IN ('Owner', 'Admin', 'Technician', 'Read-only');

DROP TABLE IF EXISTS app_deployment_rules;
DROP TABLE IF EXISTS app_packages;
