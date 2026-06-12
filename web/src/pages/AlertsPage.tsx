import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import * as api from "../api/client";
import { useAuth } from "../auth";
import { ErrorText, fmtRelative } from "../components/ui";

export function AlertsPage() {
  const { can } = useAuth();
  const canManage = can("alerts.manage");
  const [status, setStatus] = useState<"" | "firing" | "resolved">("");
  const qc = useQueryClient();

  const alerts = useQuery({
    queryKey: ["alerts", status],
    queryFn: () => api.listAlerts({ status: status || undefined, limit: 200 }),
    refetchInterval: 30_000,
  });

  const ackMut = useMutation({
    mutationFn: (id: string) => api.ackAlert(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["alerts"] }),
  });

  const rows = alerts.data?.alerts ?? [];

  return (
    <div>
      <h1>Alerts</h1>

      <div className="toolbar">
        <div className="tabs">
          {(["", "firing", "resolved"] as const).map((s) => (
            <button
              key={s || "all"}
              type="button"
              className={status === s ? "tab active" : "tab"}
              onClick={() => setStatus(s)}
            >
              {s === "" ? "All" : s.charAt(0).toUpperCase() + s.slice(1)}
            </button>
          ))}
        </div>
      </div>

      <ErrorText error={alerts.error} />
      {alerts.isLoading && <p>Loading…</p>}

      {!alerts.isLoading && rows.length === 0 && (
        <p className="muted">No alerts{status ? ` with status "${status}"` : ""}.</p>
      )}

      {rows.length > 0 && (
        <div className="card">
          <table className="data-table">
            <thead>
              <tr>
                <th>Device</th>
                <th>Rule</th>
                <th>Severity</th>
                <th>Message</th>
                <th>Status</th>
                <th>Fired</th>
                <th>Resolved</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {rows.map((a) => (
                <tr key={a.id}>
                  <td>
                    <Link to={`/devices/${a.device_id}`}>{a.hostname}</Link>
                  </td>
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
                  <td className="muted">
                    {a.resolved_at ? fmtRelative(a.resolved_at) : "—"}
                  </td>
                  <td>
                    {canManage && a.status === "firing" && !a.acked_at && (
                      <button
                        type="button"
                        disabled={ackMut.isPending}
                        onClick={() => ackMut.mutate(a.id)}
                      >
                        Ack
                      </button>
                    )}
                    {a.acked_at && (
                      <span className="muted" title={`Acked ${fmtRelative(a.acked_at)}`}>
                        Acked
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
