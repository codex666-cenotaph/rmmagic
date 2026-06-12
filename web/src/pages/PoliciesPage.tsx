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
  const [showNewChannel, setShowNewChannel] = useState(false);

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
        {showNewPolicy && (
          <PolicyForm
            channels={channels.data?.channels ?? []}
            onClose={() => setShowNewPolicy(false)}
            onSaved={() => {
              setShowNewPolicy(false);
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
        {showNewChannel && (
          <ChannelForm
            onClose={() => setShowNewChannel(false)}
            onSaved={() => {
              setShowNewChannel(false);
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
  onDelete,
}: {
  policy: api.Policy;
  channelMap: Record<string, string>;
  canManage: boolean;
  onDelete: () => void;
}) {
  const rules = policy.rules;
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
          {policy.scope_id ? ` (${policy.scope_id})` : ""}
        </span>
        <span className={policy.enabled ? "badge badge-ok" : "badge"}>
          {policy.enabled ? "enabled" : "disabled"}
        </span>
        {canManage && (
          <button type="button" className="danger" onClick={onDelete}>
            Delete
          </button>
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
  channels,
  onClose,
  onSaved,
}: {
  channels: api.Channel[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [scopeType, setScopeType] = useState<api.PolicyScopeType>("tenant");
  const [enabled, setEnabled] = useState(true);
  const [cpuEnabled, setCpuEnabled] = useState(false);
  const [cpuThreshold, setCpuThreshold] = useState("80");
  const [memEnabled, setMemEnabled] = useState(false);
  const [memThreshold, setMemThreshold] = useState("80");
  const [diskEnabled, setDiskEnabled] = useState(false);
  const [diskThreshold, setDiskThreshold] = useState("85");
  const [offlineEnabled, setOfflineEnabled] = useState(false);
  const [offlineAfterS, setOfflineAfterS] = useState("300");
  const [channelIDs, setChannelIDs] = useState<string[]>([]);
  const [err, setErr] = useState("");

  const mut = useMutation({
    mutationFn: (body: api.PolicyBody) => api.createPolicy(body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["policies"] });
      onSaved();
    },
    onError: (e) => setErr((e as Error).message),
  });

  function submit(ev: React.FormEvent) {
    ev.preventDefault();
    setErr("");
    const rules: api.PolicyRules = {};
    if (cpuEnabled) rules.cpu_pct = { threshold: Number(cpuThreshold) };
    if (memEnabled) rules.mem_pct = { threshold: Number(memThreshold) };
    if (diskEnabled) rules.disk_pct = { threshold: Number(diskThreshold) };
    if (offlineEnabled) rules.offline = { after_s: Number(offlineAfterS) };
    mut.mutate({ name, scope_type: scopeType, enabled, rules, channel_ids: channelIDs });
  }

  return (
    <form className="card form" onSubmit={submit}>
      <h3>New policy</h3>
      <label>
        Name
        <input value={name} onChange={(e) => setName(e.target.value)} required />
      </label>
      <label>
        Scope
        <select
          value={scopeType}
          onChange={(e) => setScopeType(e.target.value as api.PolicyScopeType)}
        >
          <option value="tenant">Tenant (all devices)</option>
          <option value="customer">Customer</option>
          <option value="site">Site</option>
          <option value="device">Device</option>
        </select>
      </label>
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
          Create
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </form>
  );
}

function ChannelForm({
  onClose,
  onSaved,
}: {
  onClose: () => void;
  onSaved: () => void;
}) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [type, setType] = useState<api.ChannelType>("email");
  const [recipients, setRecipients] = useState("");
  const [webhookURL, setWebhookURL] = useState("");
  const [secret, setSecret] = useState("");
  const [err, setErr] = useState("");

  const mut = useMutation({
    mutationFn: (body: api.ChannelBody) => api.createChannel(body),
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
      config = { url: webhookURL };
    }
    mut.mutate({ name, type, config, secret: secret || undefined });
  }

  return (
    <form className="card form" onSubmit={submit}>
      <h3>New channel</h3>
      <label>
        Name
        <input value={name} onChange={(e) => setName(e.target.value)} required />
      </label>
      <label>
        Type
        <select
          value={type}
          onChange={(e) => setType(e.target.value as api.ChannelType)}
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
            Signing secret (optional)
            <input
              type="password"
              value={secret}
              onChange={(e) => setSecret(e.target.value)}
            />
          </label>
        </>
      )}

      {err && <p className="error">{err}</p>}
      <div className="form-actions">
        <button type="submit" disabled={mut.isPending}>
          Create
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </form>
  );
}
