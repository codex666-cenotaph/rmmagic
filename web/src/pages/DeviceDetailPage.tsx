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

export function DeviceDetailPage() {
  const { id = "" } = useParams<{ id: string }>();
  const { can } = useAuth();
  const canManage = can("devices.manage");
  const qc = useQueryClient();

  const device = useQuery({
    queryKey: ["devices", id],
    queryFn: () => api.getDevice(id),
    refetchInterval: 15_000,
  });
  const stats = useQuery({
    queryKey: ["device-stats", id],
    queryFn: () => api.getDeviceStats(id),
    refetchInterval: 30_000,
  });

  const decommissionMut = useMutation({
    mutationFn: () => api.decommissionDevice(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["devices"] });
      void qc.invalidateQueries({ queryKey: ["devices", id] });
    },
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
                const pct = disk.total > 0 ? (disk.used / disk.total) * 100 : 0;
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
