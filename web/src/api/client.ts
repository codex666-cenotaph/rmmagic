// API client for rmmagic M1 surface. See docs/API.md.

const BASE = "/api/v1";

export class ApiError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const res = await fetch(BASE + path, {
    method,
    credentials: "include",
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    let message = res.statusText || `Request failed (${res.status})`;
    try {
      const data = (await res.json()) as { error?: string };
      if (data && typeof data.error === "string") message = data.error;
    } catch {
      // body wasn't JSON; keep default message
    }
    throw new ApiError(res.status, message);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

// ---- Types ----

export type ScopeType = "tenant" | "customer" | "site";

export interface Grant {
  scope_type: ScopeType;
  scope_id: string | null;
  permissions: string[];
}

export interface Me {
  user: { id: string; email: string; mfa_enabled: boolean };
  tenant: { id: string; name: string; slug: string };
  grants: Grant[];
}

export interface LoginResponse {
  mfa_required: boolean;
}

export interface MfaSetupResponse {
  secret: string;
  otpauth_url: string;
}

export interface MfaEnableResponse {
  recovery_codes: string[];
}

export interface Customer {
  id: string;
  name: string;
  created_at: string;
}

export interface Site {
  id: string;
  customer_id: string;
  name: string;
  timezone: string;
}

export interface RoleAssignment {
  id: string;
  role_id: string;
  role_name: string;
  scope_type: ScopeType;
  scope_id: string | null;
}

export interface User {
  id: string;
  email: string;
  status: "active" | "disabled";
  mfa_enabled: boolean;
  assignments: RoleAssignment[];
}

export interface Role {
  id: string;
  name: string;
  permissions: string[];
  is_builtin: boolean;
}

export interface ApiToken {
  id: string;
  name: string;
  permissions: string[];
  scope_type: ScopeType | null;
  scope_id: string | null;
  last_used_at: string | null;
  expires_at: string | null;
  revoked_at: string | null;
  created_at: string;
}

export type DeviceStatus = "active" | "decommissioned";

export interface Device {
  id: string;
  site_id: string;
  site_name: string;
  customer_id: string;
  customer_name: string;
  hostname: string;
  os: string;
  arch: string;
  agent_version: string;
  status: DeviceStatus;
  online: boolean;
  last_seen_at: string | null;
  created_at: string;
}

export interface DiskSample {
  mount: string;
  used: number;
  total: number;
}

export interface StatsSample {
  ts: string;
  cpu_pct: number;
  mem_used: number;
  mem_total: number;
  disks: DiskSample[];
  net: { rx_bytes: number; tx_bytes: number };
}

export interface EnrollmentToken {
  id: string;
  site_id: string;
  site_name: string;
  expires_at: string | null;
  max_uses: number;
  use_count: number;
  revoked_at: string | null;
  created_at: string;
}

export interface AuditEntry {
  id: string;
  actor_type: string;
  actor_id: string;
  action: string;
  target_type: string;
  target_id: string;
  ip: string;
  details: unknown;
  created_at: string;
}

// ---- Auth ----

export const login = (email: string, password: string) =>
  request<LoginResponse>("POST", "/auth/login", { email, password });

export const mfaVerify = (code: string) =>
  request<Record<string, never>>("POST", "/auth/mfa/verify", { code });

export const logout = () => request<void>("POST", "/auth/logout");

export const getMe = () => request<Me>("GET", "/auth/me");

export const mfaSetup = () => request<MfaSetupResponse>("POST", "/auth/mfa/setup");

export const mfaEnable = (code: string) =>
  request<MfaEnableResponse>("POST", "/auth/mfa/enable", { code });

// ---- Organization ----

export const listCustomers = () =>
  request<{ customers: Customer[] }>("GET", "/customers");

export const createCustomer = (name: string) =>
  request<Customer>("POST", "/customers", { name });

export const renameCustomer = (id: string, name: string) =>
  request<unknown>("PATCH", `/customers/${id}`, { name });

export const deleteCustomer = (id: string) =>
  request<void>("DELETE", `/customers/${id}`);

export const listSites = (customerId: string) =>
  request<{ sites: Site[] }>("GET", `/customers/${customerId}/sites`);

export const createSite = (customerId: string, name: string, timezone?: string) =>
  request<Site>(
    "POST",
    `/customers/${customerId}/sites`,
    timezone ? { name, timezone } : { name },
  );

export const updateSite = (
  id: string,
  patch: { name?: string; timezone?: string },
) => request<unknown>("PATCH", `/sites/${id}`, patch);

export const deleteSite = (id: string) => request<void>("DELETE", `/sites/${id}`);

// ---- Users & roles ----

export const listUsers = () => request<{ users: User[] }>("GET", "/users");

export const createUser = (email: string, password: string) =>
  request<{ id: string; email: string }>("POST", "/users", { email, password });

export const setUserStatus = (id: string, status: "active" | "disabled") =>
  request<unknown>("PATCH", `/users/${id}`, { status });

export const listRoles = () => request<{ roles: Role[] }>("GET", "/roles");

export const createAssignment = (
  userId: string,
  body: { role_id: string; scope_type: ScopeType; scope_id?: string },
) => request<{ id: string }>("POST", `/users/${userId}/assignments`, body);

export const deleteAssignment = (id: string) =>
  request<void>("DELETE", `/assignments/${id}`);

// ---- API tokens ----

export const listTokens = () =>
  request<{ tokens: ApiToken[] }>("GET", "/api-tokens");

export const createToken = (body: {
  name: string;
  permissions: string[];
  scope_type?: ScopeType;
  scope_id?: string;
  expires_at?: string;
}) => request<{ id: string; token: string }>("POST", "/api-tokens", body);

export const revokeToken = (id: string) =>
  request<void>("DELETE", `/api-tokens/${id}`);

// ---- Devices ----

export const listDevices = () =>
  request<{ devices: Device[] }>("GET", "/devices");

export const getDevice = (id: string) => request<Device>("GET", `/devices/${id}`);

export const getDeviceStats = (id: string, since?: string, until?: string) => {
  const q = new URLSearchParams();
  if (since) q.set("since", since);
  if (until) q.set("until", until);
  const qs = q.toString();
  return request<{ samples: StatsSample[] }>(
    "GET",
    `/devices/${id}/stats${qs ? `?${qs}` : ""}`,
  );
};

export const decommissionDevice = (id: string) =>
  request<Record<string, never>>("POST", `/devices/${id}/decommission`);

// ---- Enrollment tokens ----

export const listEnrollmentTokens = () =>
  request<{ tokens: EnrollmentToken[] }>("GET", "/enrollment-tokens");

export const createEnrollmentToken = (body: {
  site_id: string;
  expires_at?: string;
  max_uses?: number;
}) => request<{ id: string; token: string }>("POST", "/enrollment-tokens", body);

export const revokeEnrollmentToken = (id: string) =>
  request<void>("DELETE", `/enrollment-tokens/${id}`);

// ---- Audit ----

export const listAudit = (params: { limit?: number; before?: string }) => {
  const q = new URLSearchParams();
  if (params.limit) q.set("limit", String(params.limit));
  if (params.before) q.set("before", params.before);
  const qs = q.toString();
  return request<{ entries: AuditEntry[] }>(
    "GET",
    `/audit${qs ? `?${qs}` : ""}`,
  );
};
