// API client for rmmagic M1 surface. See docs/API.md.

const BASE = "/api/v1";

export class ApiError extends Error {
  status: number;
  // Structured error payload, e.g. the 409 blast-radius confirmation.
  data: unknown;

  constructor(status: number, message: string, data?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.data = data;
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
    let data: unknown;
    try {
      data = await res.json();
      const err = data as { error?: string };
      if (err && typeof err.error === "string") message = err.error;
    } catch {
      // body wasn't JSON; keep default message
    }
    throw new ApiError(res.status, message, data);
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
  tags: string[];
  update_channel: ReleaseChannel;
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

export const setDeviceTags = (id: string, tags: string[]) =>
  request<{ tags: string[] }>("PUT", `/devices/${id}/tags`, { tags });

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

// ---- Scripts, jobs, schedules ----

export type ScriptLanguage = "bash" | "powershell" | "python" | "batch";

export interface ScriptParameterDef {
  name: string;
  description?: string;
  default?: string;
  required?: boolean;
}

export interface Script {
  id: string;
  name: string;
  description: string;
  language: ScriptLanguage;
  body: string;
  parameters: ScriptParameterDef[];
  version: number;
  archived: boolean;
  created_at: string;
  updated_at: string;
}

export interface JobTarget {
  device_ids?: string[];
  site_id?: string;
  customer_id?: string;
}

export type JobStatus =
  | "pending"
  | "sent"
  | "running"
  | "succeeded"
  | "failed"
  | "timed_out"
  | "expired";

export type JobKind = "script" | "package_install" | "package_remove";

export interface Job {
  id: string;
  kind: JobKind;
  script_id?: string;
  script_name?: string;
  device_id: string;
  hostname: string;
  command_id: string;
  status: JobStatus;
  timeout_s: number;
  language?: ScriptLanguage;
  parameters: Record<string, string>;
  spec?: { packages?: string[] };
  schedule_id?: string;
  created_at: string;
  expires_at: string;
  sent_at?: string;
  started_at?: string;
  finished_at?: string;
}

export interface Schedule {
  id: string;
  script_id: string;
  script_name: string;
  name: string;
  cron: string;
  target: JobTarget;
  parameters: Record<string, string>;
  timeout_s: number;
  expires_in_s: number;
  enabled: boolean;
  next_run_at: string;
  last_run_at: string | null;
  created_at: string;
}

// Thrown payload of a 409 blast-radius response.
export interface DispatchConfirmation {
  confirmation_required: true;
  device_count: number;
  confirm_token: string;
}

export function confirmationFrom(err: unknown): DispatchConfirmation | null {
  if (err instanceof ApiError && err.status === 409) {
    const d = err.data as Partial<DispatchConfirmation> | undefined;
    if (d?.confirmation_required && typeof d.confirm_token === "string") {
      return d as DispatchConfirmation;
    }
  }
  return null;
}

export const listScripts = (archived = false) =>
  request<{ scripts: Script[] }>(
    "GET",
    `/scripts${archived ? "?archived=true" : ""}`,
  );

export const getScript = (id: string) => request<Script>("GET", `/scripts/${id}`);

export const createScript = (body: {
  name: string;
  description: string;
  language: ScriptLanguage;
  body: string;
  parameters: ScriptParameterDef[];
}) => request<{ id: string }>("POST", "/scripts", body);

export const updateScript = (
  id: string,
  body: {
    name: string;
    description: string;
    language: ScriptLanguage;
    body: string;
    parameters: ScriptParameterDef[];
  },
) => request<unknown>("PATCH", `/scripts/${id}`, body);

export const archiveScript = (id: string) =>
  request<unknown>("DELETE", `/scripts/${id}`);

export const dispatchScript = (
  scriptId: string,
  body: {
    target: JobTarget;
    parameters?: Record<string, string>;
    timeout_s?: number;
    expires_in_s?: number;
    confirm_token?: string;
  },
) =>
  request<{ job_ids: string[]; device_count: number }>(
    "POST",
    `/scripts/${scriptId}/dispatch`,
    body,
  );

export const listJobs = (deviceId?: string) =>
  request<{ jobs: Job[] }>(
    "GET",
    `/jobs${deviceId ? `?device_id=${deviceId}` : ""}`,
  );

export const getJobOutput = (id: string) =>
  request<{ output: string; exit_code: number | null }>(
    "GET",
    `/jobs/${id}/output`,
  );

export const listSchedules = () =>
  request<{ schedules: Schedule[] }>("GET", "/schedules");

export interface ScheduleBody {
  script_id: string;
  name: string;
  cron: string;
  target: JobTarget;
  parameters?: Record<string, string>;
  timeout_s?: number;
  expires_in_s?: number;
  enabled?: boolean;
  confirm_token?: string;
}

export const createSchedule = (body: ScheduleBody) =>
  request<{ id: string; next_run_at: string }>("POST", "/schedules", body);

export const updateSchedule = (id: string, body: ScheduleBody) =>
  request<{ id: string; next_run_at: string }>("PUT", `/schedules/${id}`, body);

export const deleteSchedule = (id: string) =>
  request<void>("DELETE", `/schedules/${id}`);

// ---- Inventory ----

export interface HardwareDisk {
  device: string;
  mount: string;
  fstype: string;
  total: number;
}

export interface HardwareNIC {
  name: string;
  mac: string;
  ips: string[];
}

export interface Hardware {
  hostname: string;
  platform: string;
  platform_version: string;
  kernel_version: string;
  virtualization?: string;
  cpu_model: string;
  cpu_cores: number;
  mem_total: number;
  disks: HardwareDisk[];
  nics: HardwareNIC[];
}

export interface Package {
  name: string;
  version: string;
  arch?: string;
}

export interface ServiceState {
  name: string;
  state: string;
}

export interface Inventory {
  hw: Hardware | null;
  hw_collected_at: string | null;
  packages: Package[];
  sw_collected_at: string | null;
  services: ServiceState[];
  services_updated_at: string | null;
}

export const getInventory = (deviceId: string) =>
  request<Inventory>("GET", `/devices/${deviceId}/inventory`);

export const refreshInventory = (deviceId: string) =>
  request<{ requested: boolean }>(
    "POST",
    `/devices/${deviceId}/inventory/refresh`,
  );

// ---- Policies ----

export type PolicyScopeType = "tenant" | "customer" | "site" | "device" | "tag";

export interface Policy {
  id: string;
  name: string;
  scope_type: PolicyScopeType;
  scope_id: string | null;
  scope_tag: string | null;
  enabled: boolean;
  rules: PolicyRules;
  channel_ids: string[];
  created_at: string;
  updated_at: string;
}

export interface ThresholdRule {
  threshold: number;
  severity?: "warning" | "critical";
}

export interface DiskRule {
  threshold: number;
  mounts?: string[];
  severity?: "warning" | "critical";
}

export interface OfflineRule {
  after_s: number;
  severity?: "warning" | "critical";
}

export interface ServiceRule {
  services: string[];
  severity?: "warning" | "critical";
}

export interface PolicyRules {
  cpu_pct?: ThresholdRule;
  mem_pct?: ThresholdRule;
  disk_pct?: DiskRule;
  offline?: OfflineRule;
  service_down?: ServiceRule;
}

export interface PolicyBody {
  name: string;
  scope_type: PolicyScopeType;
  scope_id?: string;
  scope_tag?: string;
  enabled: boolean;
  rules: PolicyRules;
  channel_ids: string[];
}

export const listPolicies = () =>
  request<{ policies: Policy[] }>("GET", "/policies");

export const getPolicy = (id: string) =>
  request<Policy>("GET", `/policies/${id}`);

export const createPolicy = (body: PolicyBody) =>
  request<{ id: string }>("POST", "/policies", body);

export const updatePolicy = (id: string, body: PolicyBody) =>
  request<void>("PUT", `/policies/${id}`, body);

export const deletePolicy = (id: string) =>
  request<void>("DELETE", `/policies/${id}`);

// ---- Alerts ----

export type AlertStatus = "firing" | "resolved";

export interface Alert {
  id: string;
  device_id: string;
  hostname: string;
  site_id: string;
  customer_id: string;
  policy_id: string | null;
  rule_type: string;
  dedup_key: string;
  severity: "warning" | "critical";
  message: string;
  details: Record<string, unknown>;
  channel_ids: string[];
  status: AlertStatus;
  fired_at: string;
  resolved_at: string | null;
  acked_by: string | null;
  acked_at: string | null;
}

export const listAlerts = (params?: {
  status?: string;
  device_id?: string;
  limit?: number;
}) => {
  const q = new URLSearchParams();
  if (params?.status) q.set("status", params.status);
  if (params?.device_id) q.set("device_id", params.device_id);
  if (params?.limit) q.set("limit", String(params.limit));
  const qs = q.toString();
  return request<{ alerts: Alert[] }>("GET", `/alerts${qs ? `?${qs}` : ""}`);
};

export const getAlert = (id: string) =>
  request<Alert>("GET", `/alerts/${id}`);

export const ackAlert = (id: string) =>
  request<void>("POST", `/alerts/${id}/ack`);

// ---- Notification Channels ----

export type ChannelType = "email" | "webhook";

export interface Channel {
  id: string;
  name: string;
  type: ChannelType;
  config: Record<string, unknown>;
  created_at: string;
}

export interface ChannelBody {
  name: string;
  type: ChannelType;
  config: Record<string, unknown>;
  secret?: string;
}

export const listChannels = () =>
  request<{ channels: Channel[] }>("GET", "/channels");

export const createChannel = (body: ChannelBody) =>
  request<{ id: string }>("POST", "/channels", body);

export const updateChannel = (id: string, body: ChannelBody) =>
  request<void>("PUT", `/channels/${id}`, body);

export const deleteChannel = (id: string) =>
  request<void>("DELETE", `/channels/${id}`);

// ---- App deployment (apt/dnf package jobs) ----

export type PackageOperation = "install" | "remove";

export const deployApp = (body: {
  operation: PackageOperation;
  packages: string[];
  target: JobTarget;
  timeout_s?: number;
  expires_in_s?: number;
  confirm_token?: string;
}) =>
  request<{ job_ids: string[]; device_count: number }>(
    "POST",
    "/apps/deploy",
    body,
  );

// ---- Agent releases & auto-update ----

export type ReleaseChannel = "stable" | "beta";

export interface AgentRelease {
  id: string;
  channel: ReleaseChannel;
  version: string;
  os: string;
  arch: string;
  url?: string;
  has_binary: boolean;
  sha256: string;
  signature: string;
  size_bytes: number;
  notes: string;
  created_at: string;
}

export interface DeviceUpdate {
  device_id: string;
  version: string;
  phase:
    | "offered"
    | "downloading"
    | "verified"
    | "applied"
    | "rolled_back"
    | "failed";
  error?: string;
  offered_at: string;
  updated_at: string;
}

export const listReleases = (channel?: ReleaseChannel) =>
  request<{ releases: AgentRelease[] }>(
    "GET",
    `/agent-releases${channel ? `?channel=${channel}` : ""}`,
  );

export const createRelease = (body: {
  channel: ReleaseChannel;
  version: string;
  os: string;
  arch: string;
  url?: string;
  sha256: string;
  signature: string;
  size_bytes?: number;
  notes?: string;
}) => request<{ id: string }>("POST", "/agent-releases", body);

// uploadReleaseBinary streams the signed binary to the server, which stores
// it and serves it to agents behind device auth. Multipart, so it bypasses
// the JSON `request` helper.
export const uploadReleaseBinary = async (id: string, file: File) => {
  const form = new FormData();
  form.append("file", file);
  const res = await fetch(`${BASE}/agent-releases/${id}/binary`, {
    method: "POST",
    credentials: "include",
    body: form,
  });
  if (!res.ok) {
    let message = res.statusText || `Upload failed (${res.status})`;
    try {
      const data = (await res.json()) as { error?: string };
      if (data?.error) message = data.error;
    } catch {
      /* keep default */
    }
    throw new ApiError(res.status, message);
  }
  return (await res.json()) as { size_bytes: number };
};

export const rolloutRelease = (
  id: string,
  body: { target: JobTarget; confirm_token?: string },
) =>
  request<{ version: string; matched: number; online_offered: number }>(
    "POST",
    `/agent-releases/${id}/rollout`,
    body,
  );

export const listDeviceUpdates = () =>
  request<{ updates: DeviceUpdate[] }>("GET", "/device-updates");

export const setUpdateChannel = (deviceId: string, channel: ReleaseChannel) =>
  request<unknown>("POST", `/devices/${deviceId}/update-channel`, { channel });

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
