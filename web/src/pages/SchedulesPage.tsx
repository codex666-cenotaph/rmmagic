import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as api from "../api/client";
import { useAuth } from "../auth";
import { ErrorText, Modal, fmtRelative, fmtTime } from "../components/ui";
import { TargetPicker } from "../components/TargetPicker";

const CRON_PRESETS = [
  { label: "Every hour", value: "@hourly" },
  { label: "Nightly at 03:00 UTC", value: "0 3 * * *" },
  { label: "Weekly (Sun 03:00 UTC)", value: "0 3 * * 0" },
  { label: "Custom…", value: "custom" },
];

function describeTarget(t: api.JobTarget): string {
  if (t.device_ids) return `${t.device_ids.length} device(s)`;
  if (t.site_id) return "site";
  if (t.customer_id) return "customer";
  return "—";
}

export function SchedulesPage() {
  const { can } = useAuth();
  const canExecute = can("scripts.execute");
  const qc = useQueryClient();
  const schedules = useQuery({ queryKey: ["schedules"], queryFn: api.listSchedules });
  const [showCreate, setShowCreate] = useState(false);

  const deleteMut = useMutation({
    mutationFn: (id: string) => api.deleteSchedule(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["schedules"] }),
  });

  const toggleMut = useMutation({
    mutationFn: (s: api.Schedule) =>
      api.updateSchedule(s.id, {
        script_id: s.script_id,
        name: s.name,
        cron: s.cron,
        target: s.target,
        parameters: s.parameters,
        timeout_s: s.timeout_s,
        expires_in_s: s.expires_in_s,
        enabled: !s.enabled,
      }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["schedules"] }),
  });

  if (schedules.isLoading) return <p>Loading schedules…</p>;
  if (schedules.error)
    return (
      <p className="error">
        Failed to load schedules: {(schedules.error as Error).message}
      </p>
    );

  const list = schedules.data?.schedules ?? [];

  return (
    <div>
      <h1>Schedules</h1>
      <p className="muted">Cron times are evaluated in UTC.</p>
      {canExecute && (
        <p>
          <button type="button" className="primary" onClick={() => setShowCreate(true)}>
            New schedule
          </button>
        </p>
      )}
      {list.length === 0 && <p className="muted">No schedules.</p>}
      <table className="data">
        <thead>
          <tr>
            <th>Name</th>
            <th>Script</th>
            <th>Cron</th>
            <th>Target</th>
            <th>Status</th>
            <th>Last run</th>
            <th>Next run</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {list.map((s) => (
            <tr key={s.id}>
              <td>{s.name}</td>
              <td>{s.script_name}</td>
              <td>
                <code>{s.cron}</code>
              </td>
              <td>{describeTarget(s.target)}</td>
              <td>
                <span className={`badge ${s.enabled ? "on" : "off"}`}>
                  {s.enabled ? "enabled" : "disabled"}
                </span>
              </td>
              <td>{fmtRelative(s.last_run_at)}</td>
              <td>{s.enabled ? fmtTime(s.next_run_at) : "—"}</td>
              <td>
                {canExecute && (
                  <span className="row-actions">
                    <button type="button" onClick={() => toggleMut.mutate(s)}>
                      {s.enabled ? "Disable" : "Enable"}
                    </button>
                    <button
                      type="button"
                      className="danger"
                      onClick={() => {
                        if (confirm(`Delete schedule "${s.name}"?`)) deleteMut.mutate(s.id);
                      }}
                    >
                      Delete
                    </button>
                  </span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <ErrorText error={deleteMut.error} />
      <ErrorText error={toggleMut.error} />
      {showCreate && (
        <CreateScheduleDialog
          onClose={() => setShowCreate(false)}
          onSaved={() => {
            setShowCreate(false);
            void qc.invalidateQueries({ queryKey: ["schedules"] });
          }}
        />
      )}
    </div>
  );
}

function CreateScheduleDialog({
  onClose,
  onSaved,
}: {
  onClose: () => void;
  onSaved: () => void;
}) {
  const scripts = useQuery({ queryKey: ["scripts"], queryFn: () => api.listScripts() });
  const [scriptId, setScriptId] = useState("");
  const [name, setName] = useState("");
  const [preset, setPreset] = useState("0 3 * * *");
  const [customCron, setCustomCron] = useState("");
  const [target, setTarget] = useState<api.JobTarget | null>(null);
  const [paramValues, setParamValues] = useState<Record<string, string>>({});
  const [confirmation, setConfirmation] = useState<api.DispatchConfirmation | null>(null);

  const cron = preset === "custom" ? customCron : preset;
  const selectedScript = (scripts.data?.scripts ?? []).find((s) => s.id === scriptId);

  const createMut = useMutation({
    mutationFn: (confirmToken?: string) =>
      api.createSchedule({
        script_id: scriptId,
        name: name.trim(),
        cron,
        target: target!,
        parameters: paramValues,
        confirm_token: confirmToken,
      }),
    onSuccess: onSaved,
    onError: (err) => {
      const c = api.confirmationFrom(err);
      if (c) setConfirmation(c);
    },
  });

  const ready = scriptId && name.trim() && cron.trim() && target;

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (ready) {
      setConfirmation(null);
      createMut.mutate(undefined);
    }
  }

  return (
    <Modal title="New schedule" onClose={onClose}>
      <form style={{ display: "grid", gap: 12 }} onSubmit={onSubmit}>
        <label>
          Name
          <input value={name} onChange={(e) => setName(e.target.value)} required />
        </label>
        <label>
          Script
          <select
            value={scriptId}
            onChange={(e) => {
              setScriptId(e.target.value);
              setParamValues({});
            }}
            required
          >
            <option value="">Select a script…</option>
            {(scripts.data?.scripts ?? []).map((s) => (
              <option key={s.id} value={s.id}>
                {s.name} ({s.language})
              </option>
            ))}
          </select>
        </label>
        <label>
          Runs
          <select value={preset} onChange={(e) => setPreset(e.target.value)}>
            {CRON_PRESETS.map((p) => (
              <option key={p.value} value={p.value}>
                {p.label}
              </option>
            ))}
          </select>
        </label>
        {preset === "custom" && (
          <label>
            Cron expression (UTC)
            <input
              value={customCron}
              onChange={(e) => setCustomCron(e.target.value)}
              placeholder="0 3 * * *"
              required
            />
          </label>
        )}
        <TargetPicker onChange={setTarget} />
        {(selectedScript?.parameters ?? []).map((p) => (
          <label key={p.name}>
            {p.name}
            {p.required ? " *" : ""}
            {p.description && <span className="muted"> — {p.description}</span>}
            <input
              value={paramValues[p.name] ?? p.default ?? ""}
              onChange={(e) =>
                setParamValues((prev) => ({ ...prev, [p.name]: e.target.value }))
              }
              required={p.required}
            />
          </label>
        ))}

        {confirmation ? (
          <div className="warning">
            This schedule currently targets{" "}
            <strong>{confirmation.device_count} devices</strong>. Confirm to create it.
          </div>
        ) : (
          <ErrorText error={createMut.error} />
        )}
        <div className="row-actions">
          {confirmation ? (
            <button
              type="button"
              className="danger"
              disabled={createMut.isPending}
              onClick={() => createMut.mutate(confirmation.confirm_token)}
            >
              Create for {confirmation.device_count} devices
            </button>
          ) : (
            <button type="submit" className="primary" disabled={!ready || createMut.isPending}>
              Create
            </button>
          )}
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}
