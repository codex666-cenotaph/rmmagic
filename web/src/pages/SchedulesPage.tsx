import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as api from "../api/client";
import { useAuth } from "../auth";
import { ErrorText, Modal, fmtRelative, fmtTime } from "../components/ui";
import { TargetPicker } from "../components/TargetPicker";

const CRON_PRESETS = [
  { label: "Every minute", value: "* * * * *" },
  { label: "Every 5 minutes", value: "*/5 * * * *" },
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

function describeCheck(s: api.Schedule): string {
  switch (s.check_type) {
    case "exit_code":
      return s.warning_exit_codes.length
        ? `exit code (warn: ${s.warning_exit_codes.join(", ")})`
        : "exit code";
    case "output":
      return "output token";
    default:
      return "—";
  }
}

// parseExitCodes turns a comma/space separated list into integer codes,
// dropping anything that isn't a number.
function parseExitCodes(s: string): number[] {
  return s
    .split(/[\s,]+/)
    .map((t) => t.trim())
    .filter((t) => t.length > 0)
    .map((t) => Number(t))
    .filter((n) => Number.isInteger(n));
}

// SchedulesPage and HealthChecksPage are the same underlying objects
// (schedules) split by check_type: plain schedules have check_type
// "none", health checks have a mapping. The list endpoint returns all of
// them; each page filters to its own kind and creates only that kind.
export function SchedulesPage() {
  return <ScheduleManager healthcheck={false} />;
}

export function HealthChecksPage() {
  return <ScheduleManager healthcheck={true} />;
}

function ScheduleManager({ healthcheck }: { healthcheck: boolean }) {
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
        check_type: s.check_type,
        warning_exit_codes: s.warning_exit_codes,
      }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["schedules"] }),
  });

  const noun = healthcheck ? "health check" : "schedule";

  if (schedules.isLoading) return <p>Loading {noun}s…</p>;
  if (schedules.error)
    return (
      <p className="error">
        Failed to load {noun}s: {(schedules.error as Error).message}
      </p>
    );

  // The two pages share one collection, split by check_type.
  const list = (schedules.data?.schedules ?? []).filter((s) =>
    healthcheck ? s.check_type !== "none" : s.check_type === "none",
  );

  return (
    <div>
      <h1>{healthcheck ? "Health Checks" : "Schedules"}</h1>
      <p className="muted">
        {healthcheck
          ? "Scripts run on a schedule whose result sets each device's health. Cron times are evaluated in UTC."
          : "Cron times are evaluated in UTC."}
      </p>
      {canExecute && (
        <p>
          <button type="button" className="primary" onClick={() => setShowCreate(true)}>
            New {noun}
          </button>
        </p>
      )}
      {list.length === 0 && <p className="muted">No {noun}s.</p>}
      <table className="data">
        <thead>
          <tr>
            <th>Name</th>
            <th>Script</th>
            <th>Cron</th>
            <th>Target</th>
            {healthcheck && <th>Check</th>}
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
              {healthcheck && <td>{describeCheck(s)}</td>}
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
                        if (confirm(`Delete ${noun} "${s.name}"?`)) deleteMut.mutate(s.id);
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
          healthcheck={healthcheck}
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
  healthcheck,
  onClose,
  onSaved,
}: {
  healthcheck: boolean;
  onClose: () => void;
  onSaved: () => void;
}) {
  const scripts = useQuery({ queryKey: ["scripts"], queryFn: () => api.listScripts() });
  const [scriptId, setScriptId] = useState("");
  const [name, setName] = useState("");
  // Health checks default to every minute; schedules to nightly.
  const [preset, setPreset] = useState(healthcheck ? "* * * * *" : "0 3 * * *");
  const [customCron, setCustomCron] = useState("");
  const [target, setTarget] = useState<api.JobTarget | null>(null);
  const [paramValues, setParamValues] = useState<Record<string, string>>({});
  const [checkType, setCheckType] = useState<api.CheckType>(
    healthcheck ? "exit_code" : "none",
  );
  const [warnCodes, setWarnCodes] = useState("");
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
        check_type: checkType,
        warning_exit_codes:
          checkType === "exit_code" ? parseExitCodes(warnCodes) : undefined,
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
    <Modal title={healthcheck ? "New health check" : "New schedule"} onClose={onClose}>
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
        {healthcheck && (
          <>
            <label>
              Health from
              <select
                value={checkType}
                onChange={(e) => setCheckType(e.target.value as api.CheckType)}
              >
                <option value="exit_code">Exit code</option>
                <option value="output">Output (HEALTH= token)</option>
              </select>
            </label>
            {checkType === "exit_code" && (
              <label>
                Warning exit codes
                <input
                  value={warnCodes}
                  onChange={(e) => setWarnCodes(e.target.value)}
                  placeholder="e.g. 1, 2"
                />
                <span className="muted">
                  Exit 0 is healthy; codes listed here are a warning; anything
                  else is critical.
                </span>
              </label>
            )}
            {checkType === "output" && (
              <p className="muted">
                The script must print a <code>HEALTH=healthy</code>,{" "}
                <code>HEALTH=warning</code>, or <code>HEALTH=critical</code> line.
                The last match wins.
              </p>
            )}
          </>
        )}
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
            This {healthcheck ? "health check" : "schedule"} currently targets{" "}
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
