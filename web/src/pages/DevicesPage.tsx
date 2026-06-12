import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import * as api from "../api/client";
import { useAuth } from "../auth";
import { DeviceStatusBadge, ErrorText, fmtRelative } from "../components/ui";

export function DevicesPage() {
  const { can } = useAuth();
  const canManage = can("devices.manage");
  const qc = useQueryClient();
  const devices = useQuery({
    queryKey: ["devices"],
    queryFn: api.listDevices,
    refetchInterval: 15_000,
  });

  const decommissionMut = useMutation({
    mutationFn: (id: string) => api.decommissionDevice(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["devices"] }),
  });

  if (devices.isLoading) return <p>Loading devices…</p>;
  if (devices.error)
    return (
      <p className="error">
        Failed to load devices: {(devices.error as Error).message}
      </p>
    );

  const list = devices.data?.devices ?? [];

  return (
    <div>
      <h1>Devices</h1>
      {list.length === 0 && <p className="muted">No devices enrolled yet.</p>}
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
          {list.map((d) => (
            <tr key={d.id}>
              <td>
                <Link to={`/devices/${d.id}`}>{d.hostname}</Link>
              </td>
              <td>
                {d.customer_name} <span className="muted">/ {d.site_name}</span>
              </td>
              <td>
                {d.os} <span className="muted">({d.arch})</span>
              </td>
              <td>{d.agent_version}</td>
              <td>
                <DeviceStatusBadge status={d.status} online={d.online} />
              </td>
              <td>{fmtRelative(d.last_seen_at)}</td>
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
          ))}
        </tbody>
      </table>
      <ErrorText error={decommissionMut.error} />
    </div>
  );
}
