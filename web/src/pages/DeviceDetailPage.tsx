import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import * as api from "../api/client";
import { useAuth } from "../auth";
import {
  DeviceStatusBadge,
  ErrorText,
  fmtBytes,
  fmtRelative,
} from "../components/ui";

type Tab = "stats" | "inventory" | "alerts";

export function DeviceDetailPage() {
  const { id = "" } = useParams<{ id: string }>();
  const { can } = useAuth();
  const canManage = can("devices.manage");
  const qc = useQueryClient();
  const [tab, setTab] = useState<Tab>("stats");

  const device = useQuery({
    queryKey: ["devices", id],
    queryFn: () => api.getDevice(id),
    refetchInterval: 15_000,
  });
  const stats = useQuery({
    queryKey: ["device-stats", id],
    queryFn: () => api.getDeviceStats(id),
    refetchInterval: 30_000,
    enabled: tab === "stats",
  });
  const inventory = useQuery({
    queryKey: ["device-inventory", id],
    queryFn: () => api.getInventory(id),
    enabled: tab === "inventory",
  });
  const deviceAlerts = useQuery({
    queryKey: ["alerts", "device", id],
    queryFn: () => api.listAlerts({ limit: 50 }),
    enabled: tab === "alerts",
  });

  const decommissionMut = useMutation({
    mutationFn: () => api.decommissionDevice(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["devices"] });
      void qc.invalidateQueries({ queryKey: ["devices", id] });
    },
  });

  const refreshInvMut = useMutation({
    mutationFn: () => api.refreshInventory(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["device-inventory", id] }),
  });

  if (device.isLoading) return <p>Loading device…</p>;
  if (device.error)
    return (
      <p className="error">
        Failed to load device: {(device.error as Error).message}
      </p>
    );

  const d = device.data!;
  const samples = stats.data?.samples ?? [];
  const latest = samples.length > 0 ? samples[samples.length - 1] : null;
  const memMax = samples.reduce((m, s) => Math.max(m, s.mem_total), 0);

  return (
    <div>
      <p>
        <Link to="/devices">&larr; All devices</Link>
      </p>
      <h1>{d.hostname}</h1>
      <div className="card">
        <div className="device-meta">
          <span>
            {d.customer_name} <span className="muted">/ {d.site_name}</span>
          </span>
          <span>
            {d.os} <span className="muted">({d.arch})</span>
          </span>
          <span>
            agent <span className="muted">{d.agent_version}</span>
          </span>
          <span>
            <DeviceStatusBadge status={d.status} online={d.online} />
          </span>
          <span>
            last seen <span className="muted">{fmtRelative(d.last_seen_at)}</span>
          </span>
          {canManage && d.status !== "decommissioned" && (
            <button
              type="button"
              className="danger"
              disabled={decommissionMut.isPending}
              onClick={() => {
                if (
                  confirm(
                    `Decommission "${d.hostname}"? The agent will be disconnected and its identity revoked. This cannot be undone.`,
                  )
                )
                  decommissionMut.mutate();
              }}
            >
              Decommission
            </button>
          )}
        </div>
        <ErrorText error={decommissionMut.error} />
      </div>

      <div className="tabs">
        {(["stats", "inventory", "alerts"] as Tab[]).map((t) => (
          <button
            key={t}
            type="button"
            className={tab === t ? "tab active" : "tab"}
            onClick={() => setTab(t)}
          >
            {t.charAt(0).toUpperCase() + t.slice(1)}
          </button>
        ))}
      </div>

      {tab === "stats" && (
        <>
          <h2>Stats (last hour)</h2>
          {stats.isLoading && <p>Loading stats…</p>}
          <ErrorText error={stats.error} />
          {!stats.isLoading && !stats.error && samples.length === 0 && (
            <p className="muted">No data yet.</p>
          )}
          {samples.length > 0 && (
            <>
              <div className="chart card">
                <h3>CPU %</h3>
                <Sparkline
                  values={samples.map((s) => s.cpu_pct)}
                  yMin={0}
                  yMax={100}
                  fmtLabel={(v) => `${Math.round(v)}%`}
                />
              </div>
              <div className="chart card">
                <h3>Memory used</h3>
                <Sparkline
                  values={samples.map((s) => s.mem_used)}
                  yMin={0}
                  yMax={memMax > 0 ? memMax : 1}
                  fmtLabel={fmtBytes}
                />
              </div>
              <div className="chart card">
                <h3>Disk usage</h3>
                {latest && latest.disks.length === 0 && (
                  <p className="muted">No data yet.</p>
                )}
                {latest &&
                  latest.disks.map((disk) => {
                    const pct =
                      disk.total > 0 ? (disk.used / disk.total) * 100 : 0;
                    return (
                      <div key={disk.mount} className="disk-row">
                        <span className="disk-mount">{disk.mount}</span>
                        <span className="disk-bar">
                          <span
                            className="disk-bar-fill"
                            style={{ width: `${Math.min(pct, 100)}%` }}
                          />
                        </span>
                        <span className="muted">
                          {fmtBytes(disk.used)} / {fmtBytes(disk.total)} (
                          {pct.toFixed(0)}%)
                        </span>
                      </div>
                    );
                  })}
              </div>
            </>
          )}
        </>
      )}

      {tab === "inventory" && (
        <InventoryTab
          deviceId={id}
          canManage={canManage}
          data={inventory.data ?? null}
          isLoading={inventory.isLoading}
          error={inventory.error}
          onRefresh={() => refreshInvMut.mutate()}
          refreshing={refreshInvMut.isPending}
        />
      )}

      {tab === "alerts" && (
        <AlertsTab
          deviceId={id}
          alerts={(deviceAlerts.data?.alerts ?? []).filter(
            (a) => a.device_id === id,
          )}
          isLoading={deviceAlerts.isLoading}
          error={deviceAlerts.error}
        />
      )}
    </div>
  );
}

function InventoryTab({
  canManage,
  data,
  isLoading,
  error,
  onRefresh,
  refreshing,
}: {
  deviceId: string;
  canManage: boolean;
  data: api.Inventory | null;
  isLoading: boolean;
  error: unknown;
  onRefresh: () => void;
  refreshing: boolean;
}) {
  const [pkgFilter, setPkgFilter] = useState("");

  if (isLoading) return <p>Loading inventory…</p>;
  if (error) return <ErrorText error={error} />;

  const hw = data?.hw;
  const packages = data?.packages ?? [];
  const services = data?.services ?? [];
  const filteredPkgs = pkgFilter
    ? packages.filter((p) =>
        p.name.toLowerCase().includes(pkgFilter.toLowerCase()),
      )
    : packages;

  return (
    <div>
      <div className="toolbar">
        <span className="muted">
          {data?.hw_collected_at
            ? `Collected ${fmtRelative(data.hw_collected_at)}`
            : "No inventory collected yet"}
        </span>
        {canManage && (
          <button
            type="button"
            disabled={refreshing}
            onClick={onRefresh}
            title="Request the agent to re-collect inventory now"
          >
            {refreshing ? "Requesting…" : "Refresh now"}
          </button>
        )}
      </div>

      {hw && (
        <div className="card">
          <h3>Hardware</h3>
          <table className="kv-table">
            <tbody>
              <tr>
                <th>Hostname</th>
                <td>{hw.hostname}</td>
              </tr>
              <tr>
                <th>Platform</th>
                <td>
                  {hw.platform} {hw.platform_version}
                </td>
              </tr>
              <tr>
                <th>Kernel</th>
                <td>{hw.kernel_version}</td>
              </tr>
              {hw.virtualization && (
                <tr>
                  <th>Virtualization</th>
                  <td>{hw.virtualization}</td>
                </tr>
              )}
              <tr>
                <th>CPU</th>
                <td>
                  {hw.cpu_model} ({hw.cpu_cores} cores)
                </td>
              </tr>
              <tr>
                <th>Memory</th>
                <td>{fmtBytes(hw.mem_total)}</td>
              </tr>
            </tbody>
          </table>

          {hw.disks.length > 0 && (
            <>
              <h4>Disks</h4>
              <table className="data-table">
                <thead>
                  <tr>
                    <th>Device</th>
                    <th>Mount</th>
                    <th>FS</th>
                    <th>Size</th>
                  </tr>
                </thead>
                <tbody>
                  {hw.disks.map((disk) => (
                    <tr key={disk.mount}>
                      <td className="muted">{disk.device}</td>
                      <td>{disk.mount}</td>
                      <td className="muted">{disk.fstype}</td>
                      <td>{fmtBytes(disk.total)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </>
          )}

          {hw.nics.length > 0 && (
            <>
              <h4>Network interfaces</h4>
              <table className="data-table">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>MAC</th>
                    <th>IPs</th>
                  </tr>
                </thead>
                <tbody>
                  {hw.nics.map((nic) => (
                    <tr key={nic.name}>
                      <td>{nic.name}</td>
                      <td className="muted mono">{nic.mac}</td>
                      <td className="muted">{nic.ips.join(", ")}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </>
          )}
        </div>
      )}

      {packages.length > 0 && (
        <div className="card">
          <h3>
            Packages{" "}
            <span className="muted">
              ({packages.length})
              {data?.sw_collected_at
                ? ` — collected ${fmtRelative(data.sw_collected_at)}`
                : ""}
            </span>
          </h3>
          <input
            className="filter-input"
            placeholder="Filter packages…"
            value={pkgFilter}
            onChange={(e) => setPkgFilter(e.target.value)}
          />
          <table className="data-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Version</th>
                <th>Arch</th>
              </tr>
            </thead>
            <tbody>
              {filteredPkgs.slice(0, 500).map((pkg) => (
                <tr key={pkg.name + "@" + pkg.version}>
                  <td>{pkg.name}</td>
                  <td className="muted">{pkg.version}</td>
                  <td className="muted">{pkg.arch ?? ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {filteredPkgs.length > 500 && (
            <p className="muted">
              Showing 500 of {filteredPkgs.length} packages. Narrow the filter
              to see more.
            </p>
          )}
        </div>
      )}

      {services.length > 0 && (
        <div className="card">
          <h3>
            Services{" "}
            <span className="muted">
              ({services.length})
              {data?.services_updated_at
                ? ` — updated ${fmtRelative(data.services_updated_at)}`
                : ""}
            </span>
          </h3>
          <table className="data-table">
            <thead>
              <tr>
                <th>Service</th>
                <th>State</th>
              </tr>
            </thead>
            <tbody>
              {services.map((svc) => (
                <tr key={svc.name}>
                  <td>{svc.name}</td>
                  <td>
                    <span
                      className={
                        svc.state === "running"
                          ? "badge badge-ok"
                          : "badge badge-warn"
                      }
                    >
                      {svc.state}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {!hw && packages.length === 0 && services.length === 0 && (
        <p className="muted">
          No inventory collected yet. The agent uploads on start and every 12
          hours.
        </p>
      )}
    </div>
  );
}

function AlertsTab({
  alerts,
  isLoading,
  error,
}: {
  deviceId: string;
  alerts: api.Alert[];
  isLoading: boolean;
  error: unknown;
}) {
  const qc = useQueryClient();
  const ackMut = useMutation({
    mutationFn: (alertId: string) => api.ackAlert(alertId),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["alerts"] }),
  });

  if (isLoading) return <p>Loading alerts…</p>;
  if (error) return <ErrorText error={error} />;
  if (alerts.length === 0) return <p className="muted">No alerts for this device.</p>;

  return (
    <div className="card">
      <table className="data-table">
        <thead>
          <tr>
            <th>Rule</th>
            <th>Severity</th>
            <th>Message</th>
            <th>Status</th>
            <th>Fired</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {alerts.map((a) => (
            <tr key={a.id}>
              <td className="muted">{a.rule_type}</td>
              <td>
                <span
                  className={
                    a.severity === "critical"
                      ? "badge badge-error"
                      : "badge badge-warn"
                  }
                >
                  {a.severity}
                </span>
              </td>
              <td>{a.message}</td>
              <td>
                <span
                  className={
                    a.status === "firing"
                      ? "badge badge-error"
                      : "badge badge-ok"
                  }
                >
                  {a.status}
                </span>
              </td>
              <td className="muted">{fmtRelative(a.fired_at)}</td>
              <td>
                {a.status === "firing" && !a.acked_at && (
                  <button
                    type="button"
                    disabled={ackMut.isPending}
                    onClick={() => ackMut.mutate(a.id)}
                  >
                    Ack
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Sparkline({
  values,
  yMin,
  yMax,
  fmtLabel,
}: {
  values: number[];
  yMin: number;
  yMax: number;
  fmtLabel: (v: number) => string;
}) {
  const W = 300;
  const H = 80;
  if (values.length === 0) return <p className="muted">No data yet.</p>;
  const span = yMax - yMin || 1;
  const points = values
    .map((v, i) => {
      const x = values.length === 1 ? W / 2 : (i / (values.length - 1)) * W;
      const y = H - ((Math.min(Math.max(v, yMin), yMax) - yMin) / span) * H;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
  return (
    <div className="chart-body">
      <svg
        className="sparkline"
        viewBox={`0 0 ${W} ${H}`}
        preserveAspectRatio="none"
        role="img"
      >
        <polyline
          points={points}
          fill="none"
          stroke="#2b6cb0"
          strokeWidth="1.5"
          vectorEffect="non-scaling-stroke"
        />
      </svg>
      <div className="chart-labels">
        <span>{fmtLabel(yMax)}</span>
        <span>{fmtLabel(yMin)}</span>
      </div>
    </div>
  );
}
