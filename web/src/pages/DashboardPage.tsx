import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import * as api from "../api/client";
import { useAuth } from "../auth";
import { compareVersions, fmtRelative } from "../components/ui";
import { BarRows, CHART, Donut, StatCard } from "../components/charts";

export function DashboardPage() {
  const { me, can } = useAuth();
  const canDevices = can("devices.read");
  const canAlerts = can("alerts.read");
  const canScripts = can("scripts.read");

  const devicesQ = useQuery({
    queryKey: ["devices"],
    queryFn: api.listDevices,
    refetchInterval: 15_000,
    enabled: canDevices,
  });
  const alertsQ = useQuery({
    queryKey: ["alerts", "firing", "dashboard"],
    queryFn: () => api.listAlerts({ status: "firing", limit: 100 }),
    refetchInterval: 15_000,
    enabled: canAlerts,
  });
  const jobsQ = useQuery({
    queryKey: ["jobs"],
    queryFn: () => api.listJobs(),
    refetchInterval: 20_000,
    enabled: canScripts,
  });
  const schedulesQ = useQuery({
    queryKey: ["schedules"],
    queryFn: api.listSchedules,
    refetchInterval: 60_000,
    enabled: canScripts,
  });
  const customersQ = useQuery({
    queryKey: ["customers"],
    queryFn: api.listCustomers,
    refetchInterval: 60_000,
  });

  const devices = useMemo(
    () => devicesQ.data?.devices ?? [],
    [devicesQ.data],
  );
  const active = devices.filter((d) => d.status !== "decommissioned");
  const online = active.filter((d) => d.online).length;
  const offline = active.length - online;
  const decommissioned = devices.length - active.length;

  // Devices by OS (active only).
  const byOS = useMemo(() => groupCount(active.map((d) => d.os || "unknown")), [active]);

  // Devices per customer (active only).
  const byCustomer = useMemo(
    () => groupCount(active.map((d) => d.customer_name || "unknown")),
    [active],
  );

  const customers = customersQ.data?.customers ?? [];

  // Agent version distribution → "updates" widget.
  const versions = useMemo(() => {
    const counts = groupCount(active.map((d) => d.agent_version || "unknown"));
    const newest =
      counts.length > 0
        ? counts
            .map((c) => c.label)
            .filter((v) => v !== "unknown")
            .sort(compareVersions)
            .at(-1) ?? null
        : null;
    return { counts, newest };
  }, [active]);
  const outdated = versions.newest
    ? active.filter(
        (d) =>
          d.agent_version &&
          d.agent_version !== "unknown" &&
          compareVersions(d.agent_version, versions.newest!) < 0,
      ).length
    : 0;

  const alerts = alertsQ.data?.alerts ?? [];
  const critical = alerts.filter((a) => a.severity === "critical").length;
  const unacked = alerts.filter((a) => !a.acked_at).length;

  const jobs = jobsQ.data?.jobs ?? [];
  const jobsByStatus = useMemo(
    () => groupCount(jobs.map((j) => j.status)),
    [jobs],
  );
  const jobRunning = jobs.filter(
    (j) => j.status === "running" || j.status === "sent" || j.status === "pending",
  ).length;

  const schedules = schedulesQ.data?.schedules ?? [];
  const schedulesEnabled = schedules.filter((s) => s.enabled).length;

  const statusJobClass: Record<string, string> = {
    succeeded: CHART.online,
    running: CHART.accent,
    sent: CHART.accent,
    pending: "var(--muted-2)",
    failed: CHART.critical,
    timed_out: CHART.warning,
    expired: CHART.warning,
  };

  return (
    <div className="dashboard">
      <div className="page-head">
        <h1>Dashboard</h1>
        <span className="muted">{me.tenant.name}</span>
      </div>

      {/* Top-line metrics */}
      <div className="stat-grid">
        <StatCard
          label="Devices online"
          value={canDevices ? `${online}` : "—"}
          sub={
            canDevices ? (
              <span>
                {offline > 0 ? `${offline} offline` : "all online"} ·{" "}
                {active.length} active
              </span>
            ) : (
              "no access"
            )
          }
          tone={offline > 0 ? "warn" : "ok"}
        />
        <StatCard
          label="Firing alerts"
          value={canAlerts ? `${alerts.length}` : "—"}
          sub={
            canAlerts
              ? `${critical} critical · ${unacked} unacked`
              : "no access"
          }
          tone={alerts.length === 0 ? "ok" : critical > 0 ? "error" : "warn"}
        />
        <StatCard
          label="Jobs in flight"
          value={canScripts ? `${jobRunning}` : "—"}
          sub={canScripts ? `${jobs.length} recent` : "no access"}
          tone={jobRunning > 0 ? "accent" : "default"}
        />
        <StatCard
          label="Outdated agents"
          value={canDevices ? `${outdated}` : "—"}
          sub={
            canDevices
              ? versions.newest
                ? `latest ${versions.newest}`
                : "no devices"
              : "no access"
          }
          tone={outdated > 0 ? "warn" : "ok"}
        />
        <StatCard
          label="Customers"
          value={`${customers.length}`}
          sub={
            canDevices
              ? `${byCustomer.length} with devices`
              : `${customers.length} total`
          }
          tone="default"
        />
      </div>

      <div className="dash-grid">
        {canDevices && (
          <section className="panel">
            <h2>Device status</h2>
            <Donut
              centerLabel="devices"
              segments={[
                { label: "Online", value: online, color: CHART.online },
                { label: "Offline", value: offline, color: CHART.offline },
                {
                  label: "Decommissioned",
                  value: decommissioned,
                  color: CHART.decommissioned,
                },
              ]}
            />
          </section>
        )}

        {canDevices && (
          <section className="panel">
            <h2>Devices by OS</h2>
            <BarRows
              items={byOS.map((g, i) => ({
                label: g.label,
                value: g.value,
                color: CHART.series[i % CHART.series.length],
              }))}
              emptyText="No devices enrolled yet."
            />
          </section>
        )}

        {canDevices && (
          <section className="panel">
            <div className="panel-head">
              <h2>Devices per customer</h2>
              <Link to="/customers" className="panel-link">
                View all
              </Link>
            </div>
            <BarRows
              items={byCustomer.map((g, i) => ({
                label: g.label,
                value: g.value,
                color: CHART.series[i % CHART.series.length],
              }))}
              emptyText="No devices enrolled yet."
            />
          </section>
        )}

        {canDevices && (
          <section className="panel">
            <div className="panel-head">
              <h2>Agent versions</h2>
              {outdated > 0 && (
                <span className="badge badge-warn">{outdated} need update</span>
              )}
            </div>
            <BarRows
              items={versions.counts.map((g) => ({
                label:
                  g.label === versions.newest
                    ? `${g.label} (latest)`
                    : g.label,
                value: g.value,
                color:
                  g.label === versions.newest ? CHART.online : CHART.warning,
              }))}
              emptyText="No devices enrolled yet."
            />
          </section>
        )}

        {canScripts && (
          <section className="panel">
            <div className="panel-head">
              <h2>Job activity</h2>
              <Link to="/jobs" className="panel-link">
                View all
              </Link>
            </div>
            <BarRows
              items={jobsByStatus.map((g) => ({
                label: g.label.replace("_", " "),
                value: g.value,
                color: statusJobClass[g.label] ?? "var(--muted-2)",
              }))}
              emptyText="No jobs dispatched yet."
            />
            <p className="muted panel-foot">
              {schedulesEnabled} of {schedules.length} schedules enabled
            </p>
          </section>
        )}

        {canAlerts && (
          <section className="panel panel-wide">
            <div className="panel-head">
              <h2>Recent firing alerts</h2>
              <Link to="/alerts" className="panel-link">
                View all
              </Link>
            </div>
            {alerts.length === 0 ? (
              <p className="muted">No firing alerts — all clear. 🎉</p>
            ) : (
              <table className="data">
                <thead>
                  <tr>
                    <th>Severity</th>
                    <th>Device</th>
                    <th>Message</th>
                    <th>Fired</th>
                    <th>Ack</th>
                  </tr>
                </thead>
                <tbody>
                  {alerts.slice(0, 8).map((a) => (
                    <tr key={a.id}>
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
                      <td>
                        <Link to={`/devices/${a.device_id}`}>{a.hostname}</Link>
                      </td>
                      <td>{a.message}</td>
                      <td className="muted">{fmtRelative(a.fired_at)}</td>
                      <td>
                        {a.acked_at ? (
                          <span className="muted">acked</span>
                        ) : (
                          <span className="badge badge-warn">pending</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>
        )}
      </div>

      {!canDevices && !canAlerts && !canScripts && (
        <p className="muted">
          You don't have access to any dashboard data. Contact an administrator
          for additional permissions.
        </p>
      )}
    </div>
  );
}

function groupCount(values: string[]): { label: string; value: number }[] {
  const map = new Map<string, number>();
  for (const v of values) map.set(v, (map.get(v) ?? 0) + 1);
  return [...map.entries()]
    .map(([label, value]) => ({ label, value }))
    .sort((a, b) => b.value - a.value);
}
