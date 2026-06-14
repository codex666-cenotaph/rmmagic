import { ReactNode, useId, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import * as api from "../api/client";
import { useAuth } from "../auth";
import {
  DeviceStatusBadge,
  ErrorText,
  fmtBytes,
  fmtRelative,
  fmtTime,
  Modal,
} from "../components/ui";
import { StatCard } from "../components/charts";
import { CastPlayer, LiveTerminal } from "../components/Terminal";

type Tab = "stats" | "inventory" | "alerts" | "shell";

export function DeviceDetailPage() {
  const { id = "" } = useParams<{ id: string }>();
  const { can } = useAuth();
  const canManage = can("devices.manage");
  const canShell = can("shell.connect");
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
    queryFn: () => api.listAlerts({ device_id: id, limit: 100 }),
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
  const decommissioned = d.status === "decommissioned";
  const dotClass = decommissioned ? "decom" : d.online ? "online" : "offline";

  return (
    <div className="device-page">
      <div className="hero-breadcrumb">
        <Link to="/devices">&larr; All devices</Link>
      </div>

      <div className="device-hero card">
        <div className="hero-main">
          <div className="hero-title">
            <span className={`status-dot ${dotClass}`} aria-hidden="true" />
            <h1>{d.hostname}</h1>
            <DeviceStatusBadge status={d.status} online={d.online} />
          </div>
          <div className="hero-meta">
            <MetaItem label="Customer" value={d.customer_name} />
            <MetaItem label="Site" value={d.site_name} />
            <MetaItem label="Operating system" value={`${d.os} (${d.arch})`} />
            <MetaItem label="Agent version" value={d.agent_version} mono />
            <MetaItem label="Last seen" value={fmtRelative(d.last_seen_at)} />
            <MetaItem label="Enrolled" value={fmtRelative(d.created_at)} />
          </div>
        </div>
        {canManage && !decommissioned && (
          <div className="hero-actions">
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
          </div>
        )}
      </div>
      <ErrorText error={decommissionMut.error} />

      <div className="tabs">
        {(
          [
            "stats",
            "inventory",
            "alerts",
            ...(canShell ? (["shell"] as Tab[]) : []),
          ] as Tab[]
        ).map((t) => (
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
        <StatsTab
          samples={stats.data?.samples ?? []}
          isLoading={stats.isLoading}
          error={stats.error}
        />
      )}

      {tab === "inventory" && (
        <InventoryTab
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
          alerts={deviceAlerts.data?.alerts ?? []}
          isLoading={deviceAlerts.isLoading}
          error={deviceAlerts.error}
        />
      )}

      {tab === "shell" && canShell && (
        <ShellTab
          deviceId={id}
          online={d.online}
          active={!decommissioned}
        />
      )}
    </div>
  );
}

function ShellTab({
  deviceId,
  online,
  active,
}: {
  deviceId: string;
  online: boolean;
  active: boolean;
}) {
  const qc = useQueryClient();
  const [live, setLive] = useState(false);
  const [playing, setPlaying] = useState<api.ShellSession | null>(null);

  const sessions = useQuery({
    queryKey: ["shell-sessions", deviceId],
    queryFn: () => api.listShellSessions(deviceId),
    enabled: !live,
  });

  const refresh = () =>
    void qc.invalidateQueries({ queryKey: ["shell-sessions", deviceId] });

  if (live) {
    return (
      <div>
        <div className="toolbar">
          <span className="muted">Live session — type to interact.</span>
          <button
            type="button"
            className="danger"
            onClick={() => {
              setLive(false);
              refresh();
            }}
          >
            End session
          </button>
        </div>
        <LiveTerminal deviceId={deviceId} />
      </div>
    );
  }

  const list = sessions.data?.sessions ?? [];

  return (
    <div>
      <div className="toolbar">
        <span className="muted">
          {online && active
            ? "Open an interactive root shell on this device."
            : "Device must be online to start a shell."}
        </span>
        <button
          type="button"
          disabled={!online || !active}
          onClick={() => setLive(true)}
          title={
            online && active
              ? "Start a remote shell session"
              : "Device is offline"
          }
        >
          Start shell
        </button>
      </div>

      {sessions.isLoading ? (
        <p>Loading sessions…</p>
      ) : sessions.error ? (
        <ErrorText error={sessions.error} />
      ) : list.length === 0 ? (
        <p className="muted">No shell sessions recorded yet.</p>
      ) : (
        <div className="card">
          <h3>Session history</h3>
          <table className="data-table">
            <thead>
              <tr>
                <th>Started</th>
                <th>Status</th>
                <th>Duration</th>
                <th>Traffic</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {list.map((s) => (
                <tr key={s.id}>
                  <td>{fmtTime(s.started_at)}</td>
                  <td>
                    <span
                      className={
                        s.status === "error"
                          ? "badge badge-error"
                          : s.status === "active"
                            ? "badge badge-warn"
                            : "badge badge-ok"
                      }
                    >
                      {s.status}
                    </span>
                  </td>
                  <td className="muted">{sessionDuration(s)}</td>
                  <td className="muted">
                    ↓{fmtBytes(s.bytes_out)} ↑{fmtBytes(s.bytes_in)}
                  </td>
                  <td>
                    {s.has_recording && (
                      <button type="button" onClick={() => setPlaying(s)}>
                        ▶ Play
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {playing && (
        <Modal
          title={`Recording — ${fmtTime(playing.started_at)}`}
          onClose={() => setPlaying(null)}
        >
          <CastPlayer sessionId={playing.id} />
          <div className="modal-actions">
            <button type="button" onClick={() => setPlaying(null)}>
              Close
            </button>
          </div>
        </Modal>
      )}
    </div>
  );
}

function sessionDuration(s: api.ShellSession): string {
  if (!s.ended_at) return "—";
  const ms = new Date(s.ended_at).getTime() - new Date(s.started_at).getTime();
  if (Number.isNaN(ms) || ms < 0) return "—";
  const sec = Math.round(ms / 1000);
  if (sec < 60) return `${sec}s`;
  return `${Math.floor(sec / 60)}m ${sec % 60}s`;
}

function InventoryTab({
  canManage,
  data,
  isLoading,
  error,
  onRefresh,
  refreshing,
}: {
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

function MetaItem({
  label,
  value,
  mono,
}: {
  label: string;
  value: ReactNode;
  mono?: boolean;
}) {
  return (
    <div className="meta-item">
      <span className="meta-label">{label}</span>
      <span className={mono ? "meta-value mono" : "meta-value"}>{value}</span>
    </div>
  );
}

function StatsTab({
  samples,
  isLoading,
  error,
}: {
  samples: api.StatsSample[];
  isLoading: boolean;
  error: unknown;
}) {
  if (isLoading) return <p>Loading stats…</p>;
  if (error) return <ErrorText error={error} />;
  if (samples.length === 0) {
    return (
      <div className="panel">
        <p className="muted">
          No telemetry yet. Charts fill in as the agent reports (≈60s interval).
        </p>
      </div>
    );
  }

  const latest = samples[samples.length - 1];
  const memMax = samples.reduce((m, s) => Math.max(m, s.mem_total), 0);
  const memPct = latest.mem_total > 0 ? (latest.mem_used / latest.mem_total) * 100 : 0;
  const disks = latest.disks ?? [];
  const peakDisk = disks.reduce(
    (best, dk) => {
      const pct = dk.total > 0 ? (dk.used / dk.total) * 100 : 0;
      return pct > best.pct ? { pct, mount: dk.mount } : best;
    },
    { pct: 0, mount: "—" },
  );

  return (
    <div className="stats-tab">
      <div className="stat-grid">
        <StatCard
          label="CPU"
          value={`${Math.round(latest.cpu_pct)}%`}
          sub="current utilization"
          tone={latest.cpu_pct >= 90 ? "error" : latest.cpu_pct >= 70 ? "warn" : "ok"}
        />
        <StatCard
          label="Memory"
          value={`${Math.round(memPct)}%`}
          sub={`${fmtBytes(latest.mem_used)} / ${fmtBytes(latest.mem_total)}`}
          tone={memPct >= 90 ? "error" : memPct >= 75 ? "warn" : "ok"}
        />
        <StatCard
          label="Disk (peak)"
          value={`${Math.round(peakDisk.pct)}%`}
          sub={peakDisk.mount}
          tone={peakDisk.pct >= 90 ? "error" : peakDisk.pct >= 75 ? "warn" : "ok"}
        />
      </div>

      <div className="chart-grid">
        <MetricCard
          title="CPU utilization"
          current={`${Math.round(latest.cpu_pct)}%`}
          values={samples.map((s) => s.cpu_pct)}
          yMin={0}
          yMax={100}
          color="var(--accent)"
        />
        <MetricCard
          title="Memory used"
          current={fmtBytes(latest.mem_used)}
          values={samples.map((s) => s.mem_used)}
          yMin={0}
          yMax={memMax > 0 ? memMax : 1}
          color="var(--chart-4)"
        />
      </div>

      <div className="panel">
        <h2>Disk usage</h2>
        {disks.length === 0 ? (
          <p className="muted">No disk data yet.</p>
        ) : (
          disks.map((disk) => {
            const pct = disk.total > 0 ? (disk.used / disk.total) * 100 : 0;
            const color =
              pct >= 90 ? "var(--error)" : pct >= 75 ? "var(--warn)" : "var(--accent)";
            return (
              <div key={disk.mount} className="disk-row">
                <span className="disk-mount">{disk.mount}</span>
                <span className="disk-bar">
                  <span
                    className="disk-bar-fill"
                    style={{ width: `${Math.min(pct, 100)}%`, background: color }}
                  />
                </span>
                <span className="muted disk-figures">
                  {fmtBytes(disk.used)} / {fmtBytes(disk.total)} ({pct.toFixed(0)}%)
                </span>
              </div>
            );
          })
        )}
      </div>
      <p className="muted">Showing the last hour. Refreshes every 30s.</p>
    </div>
  );
}

function MetricCard({
  title,
  current,
  values,
  yMin,
  yMax,
  color,
}: {
  title: string;
  current: string;
  values: number[];
  yMin: number;
  yMax: number;
  color: string;
}) {
  return (
    <div className="panel metric-card">
      <div className="metric-head">
        <h2>{title}</h2>
        <span className="metric-current" style={{ color }}>
          {current}
        </span>
      </div>
      <AreaSpark values={values} yMin={yMin} yMax={yMax} color={color} />
    </div>
  );
}

function AreaSpark({
  values,
  yMin,
  yMax,
  color,
}: {
  values: number[];
  yMin: number;
  yMax: number;
  color: string;
}) {
  const gradId = useId();
  const W = 320;
  const H = 84;
  if (values.length === 0) return <p className="muted">No data yet.</p>;
  const span = yMax - yMin || 1;
  const xy = (v: number, i: number) => {
    const x = values.length === 1 ? W / 2 : (i / (values.length - 1)) * W;
    const y = H - ((Math.min(Math.max(v, yMin), yMax) - yMin) / span) * H;
    return [x, y] as const;
  };
  const line = values.map((v, i) => xy(v, i).join(",")).join(" ");
  const [lastX, lastY] = xy(values[values.length - 1], values.length - 1);
  const area = `0,${H} ${line} ${W},${H}`;
  return (
    <svg
      className="area-spark"
      viewBox={`0 0 ${W} ${H}`}
      preserveAspectRatio="none"
      role="img"
    >
      <defs>
        <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.28" />
          <stop offset="100%" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      <polygon points={area} fill={`url(#${gradId})`} />
      <polyline
        points={line}
        fill="none"
        stroke={color}
        strokeWidth="2"
        vectorEffect="non-scaling-stroke"
      />
      <circle cx={lastX} cy={lastY} r="3" fill={color} vectorEffect="non-scaling-stroke" />
    </svg>
  );
}
