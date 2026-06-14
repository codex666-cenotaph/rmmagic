package auth

// Permission is a string constant naming one capability. Every API route
// must declare exactly one required permission; a route-table test
// enforces this so unguarded endpoints cannot exist by construction.
type Permission string

const (
	PermTenantManage   Permission = "tenant.manage"
	PermUsersRead      Permission = "users.read"
	PermUsersManage    Permission = "users.manage"
	PermOrgRead        Permission = "org.read"
	PermOrgManage      Permission = "org.manage"
	PermDevicesRead    Permission = "devices.read"
	PermDevicesManage  Permission = "devices.manage"
	PermDevicesEnroll  Permission = "devices.enroll"
	PermScriptsRead    Permission = "scripts.read"
	PermScriptsManage  Permission = "scripts.manage"
	PermScriptsExecute Permission = "scripts.execute"
	PermAppsDeploy     Permission = "apps.deploy"
	PermAgentUpdate    Permission = "agent.update"
	PermPoliciesRead   Permission = "policies.read"
	PermPoliciesManage Permission = "policies.manage"
	PermAlertsRead     Permission = "alerts.read"
	PermAlertsManage   Permission = "alerts.manage"
	PermShellConnect   Permission = "shell.connect"
	PermAuditRead      Permission = "audit.read"
	PermTokensManage   Permission = "tokens.manage"
)

// All enumerates every known permission; used for validation and for
// seeding the built-in Owner role.
func All() []Permission {
	return []Permission{
		PermTenantManage, PermUsersRead, PermUsersManage,
		PermOrgRead, PermOrgManage,
		PermDevicesRead, PermDevicesManage, PermDevicesEnroll,
		PermScriptsRead, PermScriptsManage, PermScriptsExecute,
		PermAppsDeploy, PermAgentUpdate,
		PermPoliciesRead, PermPoliciesManage,
		PermAlertsRead, PermAlertsManage,
		PermShellConnect, PermAuditRead, PermTokensManage,
	}
}

// Builtin role names seeded for every tenant.
const (
	RoleOwner      = "Owner"
	RoleAdmin      = "Admin"
	RoleTechnician = "Technician"
	RoleReadOnly   = "Read-only"
)

// BuiltinRolePermissions defines the permission sets of the built-in
// roles. Owner additionally gets tenant.manage and tokens.manage.
func BuiltinRolePermissions() map[string][]Permission {
	readOnly := []Permission{
		PermUsersRead, PermOrgRead, PermDevicesRead,
		PermScriptsRead, PermPoliciesRead, PermAlertsRead,
	}
	technician := append([]Permission{
		PermScriptsExecute, PermAppsDeploy, PermShellConnect,
		PermDevicesManage, PermDevicesEnroll, PermAlertsManage,
	}, readOnly...)
	admin := append([]Permission{
		PermUsersManage, PermOrgManage, PermScriptsManage,
		PermPoliciesManage, PermAuditRead, PermAgentUpdate,
	}, technician...)
	owner := append([]Permission{PermTenantManage, PermTokensManage}, admin...)
	return map[string][]Permission{
		RoleOwner:      owner,
		RoleAdmin:      admin,
		RoleTechnician: technician,
		RoleReadOnly:   readOnly,
	}
}
