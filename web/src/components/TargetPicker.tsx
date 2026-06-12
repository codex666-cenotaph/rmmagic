import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import * as api from "../api/client";

type Mode = "devices" | "site" | "customer";

// TargetPicker builds a JobTarget: explicit devices, one site, or one
// customer. Calls onChange with null while the selection is incomplete.
export function TargetPicker({
  onChange,
}: {
  onChange: (target: api.JobTarget | null) => void;
}) {
  const [mode, setMode] = useState<Mode>("devices");
  const [customerId, setCustomerId] = useState("");
  const [siteId, setSiteId] = useState("");
  const [deviceIds, setDeviceIds] = useState<Set<string>>(new Set());

  const customers = useQuery({ queryKey: ["customers"], queryFn: api.listCustomers });
  const devices = useQuery({ queryKey: ["devices"], queryFn: api.listDevices });
  const sites = useQuery({
    queryKey: ["sites", customerId],
    queryFn: () => api.listSites(customerId),
    enabled: mode === "site" && customerId !== "",
  });

  const activeDevices = useMemo(
    () => (devices.data?.devices ?? []).filter((d) => d.status === "active"),
    [devices.data],
  );

  useEffect(() => {
    let target: api.JobTarget | null = null;
    if (mode === "devices" && deviceIds.size > 0) target = { device_ids: [...deviceIds] };
    if (mode === "site" && siteId) target = { site_id: siteId };
    if (mode === "customer" && customerId) target = { customer_id: customerId };
    onChange(target);
    // onChange is assumed stable enough; parents pass setState.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, customerId, siteId, deviceIds]);

  function toggleDevice(id: string) {
    setDeviceIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  return (
    <div style={{ display: "grid", gap: 8 }}>
      <label>
        Target
        <select
          value={mode}
          onChange={(e) => {
            setMode(e.target.value as Mode);
            setCustomerId("");
            setSiteId("");
            setDeviceIds(new Set());
          }}
        >
          <option value="devices">Specific devices</option>
          <option value="site">All devices in a site</option>
          <option value="customer">All devices of a customer</option>
        </select>
      </label>

      {mode === "devices" && (
        <div style={{ maxHeight: 180, overflowY: "auto", border: "1px solid var(--border, #ddd)", borderRadius: 4, padding: 8 }}>
          {activeDevices.length === 0 && <p className="muted">No active devices.</p>}
          {activeDevices.map((d) => (
            <label key={d.id} style={{ display: "flex", alignItems: "center", gap: 6, fontWeight: 400 }}>
              <input
                type="checkbox"
                checked={deviceIds.has(d.id)}
                onChange={() => toggleDevice(d.id)}
              />
              {d.hostname}{" "}
              <span className="muted">
                ({d.customer_name} / {d.site_name}, {d.online ? "online" : "offline"})
              </span>
            </label>
          ))}
        </div>
      )}

      {(mode === "site" || mode === "customer") && (
        <label>
          Customer
          <select
            value={customerId}
            onChange={(e) => {
              setCustomerId(e.target.value);
              setSiteId("");
            }}
            required
          >
            <option value="">Select a customer…</option>
            {(customers.data?.customers ?? []).map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </label>
      )}

      {mode === "site" && customerId && (
        <label>
          Site
          <select value={siteId} onChange={(e) => setSiteId(e.target.value)} required>
            <option value="">{sites.isLoading ? "Loading sites…" : "Select a site…"}</option>
            {(sites.data?.sites ?? []).map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </label>
      )}
    </div>
  );
}
