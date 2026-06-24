import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import * as api from "../api/client";
import { useAuth } from "../auth";
import {
  compareVersions,
  DeviceStatusBadge,
  ErrorText,
  fmtRelative,
} from "../components/ui";
import { CHART, Donut, StatCard } from "../components/charts";

export function DevicesPage() {
  const { can } = useAuth();
  const canManage = can("devices.manage");
  const qc = useQueryClient();
  const [filter, setFilter] = useState("");
  const devices = useQuery({
    queryKey: ["devices"],
    queryFn: api.listDevices,
    refetchInterval: 15_000,
  });

  const decommissionMut = useMutation({
    mutationFn: (id: string) => api.decommissionDevice(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["devices"] }),
  });

  const list = useMemo(
    () => devices.data?.devices ?? [],
    [devices.data],
  );

  // Fleet totals for the summary graphs.
  const summary = useMemo(() => {
    const active = list.filter((d) => d.status !== "decommissioned");
    const online = active.filter((d) => d.online).length;
    const offline = active.length - online;
    const decommissioned = list.length - active.length;
    const newest =
      active
        .map((d) => d.agent_version)
        .filter((v) => v && v !== "unknown")
        .sort(compareVersions)
        .at(-1) ?? null;
    const outdated = newest
      ? active.filter(
          (d) =>
            d.agent_version &&
            d.agent_version !== "unknown" &&
            compareVersions(d.agent_version, newest) < 0,
        ).length
      : 0;
    return { active, online, offline, decommissioned, newest, outdated };
  }, [list]);

  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return list;
    return list.filter((d) =>
      [d.hostname, d.customer_name, d.site_name, d.os, d.agent_version, ...(d.tags ?? [])]
        .join(" ")
        .toLowerCase()
        .includes(q),
    );
  }, [list, filter]);

  if (devices.isLoading) return <p>Loading devices…</p>;
  if (devices.error)
    return (
      <p className="error">
        Failed to load devices: {(devices.error as Error).message}
      </p>
    );

  return (
    <div>
      <h1>Devices</h1>

      {list.length === 0 ? (
        <p className="muted">No devices enrolled yet.</p>
      ) : (
        <>
          <div className="summary-row">
            <div className="panel summary-chart">
              <h2>Status</h2>
              <Donut
                size={132}
                thickness={16}
                centerLabel="devices"
                segments={[
                  { label: "Online", value: summary.online, color: CHART.online },
                  {
                    label: "Offline",
                    value: summary.offline,
                    color: CHART.offline,
                  },
                  {
                    label: "Decommissioned",
                    value: summary.decommissioned,
                    color: CHART.decommissioned,
                  },
                ]}
              />
            </div>
            <div className="mini-stats">
              <StatCard
                label="Total devices"
                value={`${summary.active.length}`}
                sub={
                  summary.decommissioned > 0
                    ? `${summary.decommissioned} decommissioned`
                    : "all active"
                }
                tone="accent"
              />
              <StatCard
                label="Online"
                value={`${summary.online}`}
                sub="reporting now"
                tone="ok"
              />
              <StatCard
                label="Offline"
                value={`${summary.offline}`}
                sub={summary.offline > 0 ? "needs attention" : "all reporting"}
                tone={summary.offline > 0 ? "warn" : "ok"}
              />
              <StatCard
                label="Outdated agents"
                value={`${summary.outdated}`}
                sub={summary.newest ? `latest ${summary.newest}` : "—"}
                tone={summary.outdated > 0 ? "warn" : "ok"}
              />
            </div>
          </div>

          <div className="toolbar">
            <input
              className="filter-input"
              placeholder="Filter by hostname, customer, OS…"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
            <span className="muted">
              {filtered.length === list.length
                ? `${list.length} devices`
                : `${filtered.length} of ${list.length}`}
            </span>
          </div>

          <div className="card table-card">
            <table className="data">
              <thead>
                <tr>
                  <th>Hostname</th>
                  <th>Customer / Site</th>
                  <th>OS</th>
                  <th>Agent</th>
                  <th>Status</th>
                  <th>Last seen</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((d) => {
                  const outdated =
                    summary.newest &&
                    d.status !== "decommissioned" &&
                    d.agent_version &&
                    d.agent_version !== "unknown" &&
                    compareVersions(d.agent_version, summary.newest) < 0;
                  const dotClass =
                    d.status === "decommissioned"
                      ? "decom"
                      : d.online
                        ? "online"
                        : "offline";
                  return (
                    <tr key={d.id}>
                      <td>
                        <Link to={`/devices/${d.id}`} className="device-link">
                          {d.hostname}
                        </Link>
                        {(d.tags ?? []).length > 0 && (
                          <span className="tag-row inline">
                            {d.tags.map((t) => (
                              <span
                                key={t}
                                className={
                                  t === "server" ? "badge badge-ok" : "badge"
                                }
                              >
                                {t}
                              </span>
                            ))}
                          </span>
                        )}
                      </td>
                      <td>
                        {d.customer_name}{" "}
                        <span className="muted">/ {d.site_name}</span>
                      </td>
                      <td>
                        {d.os} <span className="muted">({d.arch})</span>
                      </td>
                      <td>
                        <span className="mono">{d.agent_version}</span>
                        {outdated && (
                          <span
                            className="badge badge-warn agent-update"
                            title={`Update available: ${summary.newest}`}
                          >
                            update
                          </span>
                        )}
                      </td>
                      <td>
                        <span className="status-cell">
                          <span
                            className={`status-dot ${dotClass}`}
                            aria-hidden="true"
                          />
                          <DeviceStatusBadge
                            status={d.status}
                            online={d.online}
                          />
                        </span>
                      </td>
                      <td className="muted">{fmtRelative(d.last_seen_at)}</td>
                      <td>
                        {canManage && d.status !== "decommissioned" && (
                          <button
                            type="button"
                            className="danger"
                            onClick={() => {
                              if (
                                confirm(
                                  `Decommission "${d.hostname}"? The agent will be disconnected and its identity revoked. This cannot be undone.`,
                                )
                              )
                                decommissionMut.mutate(d.id);
                            }}
                          >
                            Decommission
                          </button>
                        )}
                      </td>
                    </tr>
                  );
                })}
                {filtered.length === 0 && (
                  <tr>
                    <td colSpan={7} className="muted">
                      No devices match “{filter}”.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </>
      )}
      <ErrorText error={decommissionMut.error} />
    </div>
  );
}
