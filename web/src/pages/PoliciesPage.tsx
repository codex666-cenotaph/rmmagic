import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as api from "../api/client";
import { useAuth } from "../auth";
import { ErrorText } from "../components/ui";

export function PoliciesPage() {
  const { can } = useAuth();
  const canManage = can("policies.manage");
  const qc = useQueryClient();
  const [showNewPolicy, setShowNewPolicy] = useState(false);
  const [editingPolicy, setEditingPolicy] = useState<api.Policy | null>(null);
  const [showNewChannel, setShowNewChannel] = useState(false);
  const [editingChannel, setEditingChannel] = useState<api.Channel | null>(null);

  const policies = useQuery({
    queryKey: ["policies"],
    queryFn: api.listPolicies,
  });
  const channels = useQuery({
    queryKey: ["channels"],
    queryFn: api.listChannels,
  });

  const deletePolicyMut = useMutation({
    mutationFn: (id: string) => api.deletePolicy(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["policies"] }),
  });
  const deleteChannelMut = useMutation({
    mutationFn: (id: string) => api.deleteChannel(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["channels"] }),
  });

  const channelMap: Record<string, string> = {};
  for (const ch of channels.data?.channels ?? []) {
    channelMap[ch.id] = ch.name;
  }

  return (
    <div>
      <h1>Monitoring Policies</h1>

      <section>
        <div className="toolbar">
          <h2>Policies</h2>
          {canManage && (
            <button type="button" onClick={() => setShowNewPolicy(true)}>
              New policy
            </button>
          )}
        </div>
        <ErrorText error={policies.error} />
        {policies.isLoading && <p>Loading…</p>}
        {(showNewPolicy || editingPolicy) && (
          <PolicyForm
            policy={editingPolicy ?? undefined}
            channels={channels.data?.channels ?? []}
            onClose={() => {
              setShowNewPolicy(false);
              setEditingPolicy(null);
            }}
            onSaved={() => {
              setShowNewPolicy(false);
              setEditingPolicy(null);
              void qc.invalidateQueries({ queryKey: ["policies"] });
            }}
          />
        )}
        {(policies.data?.policies ?? []).length === 0 && !policies.isLoading && (
          <p className="muted">No policies yet.</p>
        )}
        {(policies.data?.policies ?? []).map((pol) => (
          <PolicyCard
            key={pol.id}
            policy={pol}
            channelMap={channelMap}
            canManage={canManage}
            onEdit={() => setEditingPolicy(pol)}
            onDelete={() => {
              if (confirm(`Delete policy "${pol.name}"?`))
                deletePolicyMut.mutate(pol.id);
            }}
          />
        ))}
      </section>

      <section style={{ marginTop: "2rem" }}>
        <div className="toolbar">
          <h2>Notification Channels</h2>
          {canManage && (
            <button type="button" onClick={() => setShowNewChannel(true)}>
              New channel
            </button>
          )}
        </div>
        <ErrorText error={channels.error} />
        {channels.isLoading && <p>Loading…</p>}
        {(showNewChannel || editingChannel) && (
          <ChannelForm
            channel={editingChannel ?? undefined}
            onClose={() => {
              setShowNewChannel(false);
              setEditingChannel(null);
            }}
            onSaved={() => {
              setShowNewChannel(false);
              setEditingChannel(null);
              void qc.invalidateQueries({ queryKey: ["channels"] });
            }}
          />
        )}
        {(channels.data?.channels ?? []).length === 0 && !channels.isLoading && (
          <p className="muted">No notification channels yet.</p>
        )}
        <div className="card">
          <table className="data-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Type</th>
                <th>Config</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {(channels.data?.channels ?? []).map((ch) => (
                <tr key={ch.id}>
                  <td>{ch.name}</td>
                  <td>{ch.type}</td>
                  <td className="muted">
                    {ch.type === "email"
                      ? ((ch.config as { recipients?: string[] }).recipients ?? []).join(", ")
                      : (ch.config as { url?: string }).url ?? ""}
                  </td>
                  <td>
                    {canManage && (
                      <>
                        <button
                          type="button"
                          disabled={deleteChannelMut.isPending}
                          onClick={() => setEditingChannel(ch)}
                        >
                          Edit
                        </button>
                        {" "}
                        <button
                          type="button"
                          className="danger"
                          disabled={deleteChannelMut.isPending}
                          onClick={() => {
                            if (confirm(`Delete channel "${ch.name}"?`))
                              deleteChannelMut.mutate(ch.id);
                          }}
                        >
                          Delete
                        </button>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}

function PolicyCard({
  policy,
  channelMap,
  canManage,
  onEdit,
  onDelete,
}: {
  policy: api.Policy;
  channelMap: Record<string, string>;
  canManage: boolean;
  onEdit: () => void;
  onDelete: () => void;
}) {
  // Guard against a policy row with missing/empty rules so one bad
  // record can't take down the whole page.
  const rules = policy.rules ?? {};
  const ruleLines: string[] = [];
  if (rules.cpu_pct)
    ruleLines.push(`CPU ≥ ${rules.cpu_pct.threshold}% (${rules.cpu_pct.severity ?? "warning"})`);
  if (rules.mem_pct)
    ruleLines.push(`Memory ≥ ${rules.mem_pct.threshold}% (${rules.mem_pct.severity ?? "warning"})`);
  if (rules.disk_pct)
    ruleLines.push(
      `Disk ≥ ${rules.disk_pct.threshold}%${rules.disk_pct.mounts?.length ? ` on ${rules.disk_pct.mounts.join(", ")}` : ""} (${rules.disk_pct.severity ?? "warning"})`,
    );
  if (rules.offline)
    ruleLines.push(
      `Offline after ${rules.offline.after_s}s (${rules.offline.severity ?? "warning"})`,
    );
  if (rules.service_down)
    ruleLines.push(
      `Service down: ${rules.service_down.services.join(", ")} (${rules.service_down.severity ?? "warning"})`,
    );

  const chNames = policy.channel_ids
    .map((id) => channelMap[id] ?? id)
    .join(", ");

  return (
    <div className="card">
      <div className="card-header">
        <strong>{policy.name}</strong>
        <span className="muted">
          {policy.scope_type}
          {policy.scope_type === "tag" && policy.scope_tag
            ? ` (${policy.scope_tag})`
            : policy.scope_id
              ? ` (${policy.scope_id})`
              : ""}
        </span>
        <span className={policy.enabled ? "badge badge-ok" : "badge"}>
          {policy.enabled ? "enabled" : "disabled"}
        </span>
        {canManage && (
          <>
            <button type="button" onClick={onEdit}>
              Edit
            </button>
            {" "}
            <button type="button" className="danger" onClick={onDelete}>
              Delete
            </button>
          </>
        )}
      </div>
      <div className="card-body">
        {ruleLines.length > 0 && (
          <ul className="rule-list">
            {ruleLines.map((l) => (
              <li key={l}>{l}</li>
            ))}
          </ul>
        )}
        {chNames && (
          <p className="muted">
            Notify: <span>{chNames}</span>
          </p>
        )}
      </div>
    </div>
  );
}

function PolicyForm({
  policy,
  channels,
  onClose,
  onSaved,
}: {
  policy?: api.Policy;
  channels: api.Channel[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const qc = useQueryClient();
  const isEditing = !!policy;

  const [name, setName] = useState(policy?.name ?? "");
  const [scopeType, setScopeType] = useState<api.PolicyScopeType>(
    policy?.scope_type ?? "tenant"
  );
  const [scopeCustomerId, setScopeCustomerId] = useState("");
  const [scopeSiteId, setScopeSiteId] = useState("");
  const [scopeDeviceId, setScopeDeviceId] = useState("");
  const [scopeTag, setScopeTag] = useState(policy?.scope_tag ?? "");
  const [enabled, setEnabled] = useState(policy?.enabled ?? true);
  const [cpuEnabled, setCpuEnabled] = useState(!!policy?.rules?.cpu_pct);
  const [cpuThreshold, setCpuThreshold] = useState(
    String(policy?.rules?.cpu_pct?.threshold ?? "80")
  );
  const [memEnabled, setMemEnabled] = useState(!!policy?.rules?.mem_pct);
  const [memThreshold, setMemThreshold] = useState(
    String(policy?.rules?.mem_pct?.threshold ?? "80")
  );
  const [diskEnabled, setDiskEnabled] = useState(!!policy?.rules?.disk_pct);
  const [diskThreshold, setDiskThreshold] = useState(
    String(policy?.rules?.disk_pct?.threshold ?? "85")
  );
  const [offlineEnabled, setOfflineEnabled] = useState(!!policy?.rules?.offline);
  const [offlineAfterS, setOfflineAfterS] = useState(
    String(policy?.rules?.offline?.after_s ?? "300")
  );
  const [channelIDs, setChannelIDs] = useState<string[]>(
    policy?.channel_ids ?? []
  );
  const [err, setErr] = useState("");

  // Scope targets, loaded lazily as the scope type requires them.
  const customers = useQuery({
    queryKey: ["customers"],
    queryFn: api.listCustomers,
    enabled: scopeType === "customer" || scopeType === "site",
  });
  const sites = useQuery({
    queryKey: ["sites", scopeCustomerId],
    queryFn: () => api.listSites(scopeCustomerId),
    enabled: scopeType === "site" && scopeCustomerId !== "",
  });
  const devices = useQuery({
    queryKey: ["devices"],
    queryFn: api.listDevices,
    enabled: scopeType === "device",
  });

  const mut = useMutation({
    mutationFn: async (body: api.PolicyBody) => {
      if (isEditing) {
        await api.updatePolicy(policy!.id, body);
      } else {
        await api.createPolicy(body);
      }
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["policies"] });
      onSaved();
    },
    onError: (e) => setErr((e as Error).message),
  });

  function scopeID(): string {
    switch (scopeType) {
      case "customer":
        return scopeCustomerId;
      case "site":
        return scopeSiteId;
      case "device":
        return scopeDeviceId;
      default:
        return "";
    }
  }

  function submit(ev: React.FormEvent) {
    ev.preventDefault();
    setErr("");
    const tag = scopeTag.trim().toLowerCase();
    if (scopeType === "tag" && !tag) {
      setErr("Enter a tag (e.g. server) for this policy");
      return;
    }
    const id = scopeID();
    if (scopeType !== "tenant" && scopeType !== "tag" && !id) {
      setErr(`Select a ${scopeType} for this policy`);
      return;
    }
    const rules: api.PolicyRules = {};
    if (cpuEnabled) rules.cpu_pct = { threshold: Number(cpuThreshold) };
    if (memEnabled) rules.mem_pct = { threshold: Number(memThreshold) };
    if (diskEnabled) rules.disk_pct = { threshold: Number(diskThreshold) };
    if (offlineEnabled) rules.offline = { after_s: Number(offlineAfterS) };
    mut.mutate({
      name,
      scope_type: scopeType,
      scope_id: scopeType === "tenant" || scopeType === "tag" ? undefined : id,
      scope_tag: scopeType === "tag" ? tag : undefined,
      enabled,
      rules,
      channel_ids: channelIDs,
    });
  }

  return (
    <form className="card form" onSubmit={submit}>
      <h3>{isEditing ? "Edit policy" : "New policy"}</h3>
      <label>
        Name
        <input value={name} onChange={(e) => setName(e.target.value)} required />
      </label>
      <label>
        Scope
        <select
          value={scopeType}
          onChange={(e) => {
            setScopeType(e.target.value as api.PolicyScopeType);
            setScopeCustomerId("");
            setScopeSiteId("");
            setScopeDeviceId("");
            setScopeTag("");
          }}
        >
          <option value="tenant">Tenant (all devices)</option>
          <option value="customer">Customer</option>
          <option value="site">Site</option>
          <option value="tag">Tag (e.g. servers)</option>
          <option value="device">Device</option>
        </select>
      </label>

      {scopeType === "tag" && (
        <label>
          Tag
          <input
            value={scopeTag}
            onChange={(e) => setScopeTag(e.target.value)}
            placeholder="server"
            required
          />
          <span className="muted">
            Applies to every device carrying this tag. Pair with an
            “Offline after” rule so tagged servers alert when they drop off.
          </span>
        </label>
      )}

      {(scopeType === "customer" || scopeType === "site") && (
        <label>
          Customer
          <select
            value={scopeCustomerId}
            onChange={(e) => {
              setScopeCustomerId(e.target.value);
              setScopeSiteId("");
            }}
            required
          >
            <option value="">Select…</option>
            {(customers.data?.customers ?? []).map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </label>
      )}

      {scopeType === "site" && (
        <label>
          Site
          <select
            value={scopeSiteId}
            onChange={(e) => setScopeSiteId(e.target.value)}
            disabled={!scopeCustomerId}
            required
          >
            <option value="">
              {scopeCustomerId ? "Select…" : "Pick a customer first"}
            </option>
            {(sites.data?.sites ?? []).map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </label>
      )}

      {scopeType === "device" && (
        <label>
          Device
          <select
            value={scopeDeviceId}
            onChange={(e) => setScopeDeviceId(e.target.value)}
            required
          >
            <option value="">Select…</option>
            {(devices.data?.devices ?? []).map((d) => (
              <option key={d.id} value={d.id}>
                {d.hostname}
              </option>
            ))}
          </select>
        </label>
      )}
      <label>
        <input
          type="checkbox"
          checked={enabled}
          onChange={(e) => setEnabled(e.target.checked)}
        />{" "}
        Enabled
      </label>

      <fieldset>
        <legend>Rules</legend>
        <label>
          <input
            type="checkbox"
            checked={cpuEnabled}
            onChange={(e) => setCpuEnabled(e.target.checked)}
          />{" "}
          CPU ≥{" "}
          <input
            type="number"
            min={1}
            max={100}
            style={{ width: "4em" }}
            value={cpuThreshold}
            disabled={!cpuEnabled}
            onChange={(e) => setCpuThreshold(e.target.value)}
          />
          %
        </label>
        <label>
          <input
            type="checkbox"
            checked={memEnabled}
            onChange={(e) => setMemEnabled(e.target.checked)}
          />{" "}
          Memory ≥{" "}
          <input
            type="number"
            min={1}
            max={100}
            style={{ width: "4em" }}
            value={memThreshold}
            disabled={!memEnabled}
            onChange={(e) => setMemThreshold(e.target.value)}
          />
          %
        </label>
        <label>
          <input
            type="checkbox"
            checked={diskEnabled}
            onChange={(e) => setDiskEnabled(e.target.checked)}
          />{" "}
          Disk ≥{" "}
          <input
            type="number"
            min={1}
            max={100}
            style={{ width: "4em" }}
            value={diskThreshold}
            disabled={!diskEnabled}
            onChange={(e) => setDiskThreshold(e.target.value)}
          />
          %
        </label>
        <label>
          <input
            type="checkbox"
            checked={offlineEnabled}
            onChange={(e) => setOfflineEnabled(e.target.checked)}
          />{" "}
          Offline after{" "}
          <input
            type="number"
            min={90}
            style={{ width: "5em" }}
            value={offlineAfterS}
            disabled={!offlineEnabled}
            onChange={(e) => setOfflineAfterS(e.target.value)}
          />
          s
        </label>
      </fieldset>

      {channels.length > 0 && (
        <fieldset>
          <legend>Notify via</legend>
          {channels.map((ch) => (
            <label key={ch.id}>
              <input
                type="checkbox"
                checked={channelIDs.includes(ch.id)}
                onChange={(e) =>
                  setChannelIDs((prev) =>
                    e.target.checked
                      ? [...prev, ch.id]
                      : prev.filter((id) => id !== ch.id),
                  )
                }
              />{" "}
              {ch.name} ({ch.type})
            </label>
          ))}
        </fieldset>
      )}

      {err && <p className="error">{err}</p>}
      <div className="form-actions">
        <button type="submit" disabled={mut.isPending}>
          {isEditing ? "Save" : "Create"}
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </form>
  );
}

function ChannelForm({
  channel,
  onClose,
  onSaved,
}: {
  channel?: api.Channel;
  onClose: () => void;
  onSaved: () => void;
}) {
  const qc = useQueryClient();
  const isEditing = !!channel;

  const [name, setName] = useState(channel?.name ?? "");
  const [type, setType] = useState<api.ChannelType>(
    channel?.type ?? "email"
  );
  const [recipients, setRecipients] = useState(
    channel?.type === "email"
      ? ((channel.config as { recipients?: string[] }).recipients ?? []).join("\n")
      : ""
  );
  const [webhookURL, setWebhookURL] = useState(
    channel?.type === "webhook"
      ? (channel.config as { url?: string }).url ?? ""
      : ""
  );
  const [secret, setSecret] = useState("");
  const [err, setErr] = useState("");

  const mut = useMutation({
    mutationFn: async (body: api.ChannelBody) => {
      if (isEditing) {
        await api.updateChannel(channel!.id, body);
      } else {
        await api.createChannel(body);
      }
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["channels"] });
      onSaved();
    },
    onError: (e) => setErr((e as Error).message),
  });

  function submit(ev: React.FormEvent) {
    ev.preventDefault();
    setErr("");
    let config: Record<string, unknown>;
    if (type === "email") {
      const r = recipients
        .split(/[,\n]/)
        .map((s) => s.trim())
        .filter(Boolean);
      if (r.length === 0) {
        setErr("At least one recipient required");
        return;
      }
      config = { recipients: r };
    } else {
      if (!webhookURL) {
        setErr("URL required");
        return;
      }
      if (secret && secret.length < 16) {
        setErr("Webhook signing secret must be at least 16 characters");
        return;
      }
      if (!isEditing && !secret) {
        setErr("Webhook deliveries are HMAC-signed; a secret is required");
        return;
      }
      config = { url: webhookURL };
    }
    mut.mutate({ name, type, config, secret: secret || undefined });
  }

  return (
    <form className="card form" onSubmit={submit}>
      <h3>{isEditing ? "Edit channel" : "New channel"}</h3>
      <label>
        Name
        <input value={name} onChange={(e) => setName(e.target.value)} required />
      </label>
      <label>
        Type
        <select
          value={type}
          onChange={(e) => setType(e.target.value as api.ChannelType)}
          disabled={isEditing}
        >
          <option value="email">Email</option>
          <option value="webhook">Webhook</option>
        </select>
      </label>

      {type === "email" ? (
        <label>
          Recipients (comma or newline separated)
          <textarea
            value={recipients}
            onChange={(e) => setRecipients(e.target.value)}
            rows={3}
          />
        </label>
      ) : (
        <>
          <label>
            Webhook URL
            <input
              type="url"
              value={webhookURL}
              onChange={(e) => setWebhookURL(e.target.value)}
            />
          </label>
          <label>
            Signing secret (min 16 chars; deliveries are HMAC-signed)
            <input
              type="password"
              value={secret}
              onChange={(e) => setSecret(e.target.value)}
              minLength={16}
              placeholder={isEditing ? "Leave blank to keep current secret" : ""}
              required={!isEditing}
            />
          </label>
        </>
      )}

      {err && <p className="error">{err}</p>}
      <div className="form-actions">
        <button type="submit" disabled={mut.isPending}>
          {isEditing ? "Save" : "Create"}
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </form>
  );
}
