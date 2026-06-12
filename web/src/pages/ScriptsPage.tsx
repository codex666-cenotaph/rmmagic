import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import * as api from "../api/client";
import { useAuth } from "../auth";
import { ErrorText, Modal, fmtTime } from "../components/ui";
import { TargetPicker } from "../components/TargetPicker";

export function ScriptsPage() {
  const { can } = useAuth();
  const canManage = can("scripts.manage");
  const canExecute = can("scripts.execute");
  const qc = useQueryClient();
  const scripts = useQuery({ queryKey: ["scripts"], queryFn: () => api.listScripts() });

  const [editing, setEditing] = useState<api.Script | "new" | null>(null);
  const [running, setRunning] = useState<api.Script | null>(null);

  const archiveMut = useMutation({
    mutationFn: (id: string) => api.archiveScript(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["scripts"] }),
  });

  if (scripts.isLoading) return <p>Loading scripts…</p>;
  if (scripts.error)
    return (
      <p className="error">Failed to load scripts: {(scripts.error as Error).message}</p>
    );

  const list = scripts.data?.scripts ?? [];

  return (
    <div>
      <h1>Scripts</h1>
      {canManage && (
        <p>
          <button type="button" className="primary" onClick={() => setEditing("new")}>
            New script
          </button>
        </p>
      )}
      {list.length === 0 && <p className="muted">No scripts in the library yet.</p>}
      <table className="data">
        <thead>
          <tr>
            <th>Name</th>
            <th>Language</th>
            <th>Parameters</th>
            <th>Version</th>
            <th>Updated</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {list.map((s) => (
            <tr key={s.id}>
              <td>
                {s.name}
                {s.description && <div className="muted">{s.description}</div>}
              </td>
              <td>{s.language}</td>
              <td>
                {(s.parameters ?? []).map((p) => (
                  <span key={p.name} className="badge" style={{ marginRight: 4 }}>
                    {p.name}
                    {p.required ? "*" : ""}
                  </span>
                ))}
              </td>
              <td>v{s.version}</td>
              <td>{fmtTime(s.updated_at)}</td>
              <td>
                <span className="row-actions">
                  {canExecute && (
                    <button type="button" className="primary" onClick={() => setRunning(s)}>
                      Run
                    </button>
                  )}
                  {canManage && (
                    <>
                      <button type="button" onClick={() => setEditing(s)}>
                        Edit
                      </button>
                      <button
                        type="button"
                        className="danger"
                        onClick={() => {
                          if (confirm(`Archive script "${s.name}"?`)) archiveMut.mutate(s.id);
                        }}
                      >
                        Archive
                      </button>
                    </>
                  )}
                </span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <ErrorText error={archiveMut.error} />

      {editing && (
        <ScriptEditorDialog
          script={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            void qc.invalidateQueries({ queryKey: ["scripts"] });
          }}
        />
      )}
      {running && <RunScriptDialog script={running} onClose={() => setRunning(null)} />}
    </div>
  );
}

function ScriptEditorDialog({
  script,
  onClose,
  onSaved,
}: {
  script: api.Script | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState(script?.name ?? "");
  const [description, setDescription] = useState(script?.description ?? "");
  const [language, setLanguage] = useState<api.ScriptLanguage>(script?.language ?? "bash");
  const [body, setBody] = useState(script?.body ?? "");
  // Parameter definitions are edited as JSON; a structured editor can
  // come later.
  const [paramsText, setParamsText] = useState(
    JSON.stringify(script?.parameters ?? [], null, 2),
  );
  const [paramsError, setParamsError] = useState("");

  const saveMut = useMutation({
    mutationFn: (parameters: api.ScriptParameterDef[]) => {
      const payload = { name: name.trim(), description, language, body, parameters };
      return script ? api.updateScript(script.id, payload) : api.createScript(payload);
    },
    onSuccess: onSaved,
  });

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    let parameters: api.ScriptParameterDef[];
    try {
      parameters = JSON.parse(paramsText);
      if (!Array.isArray(parameters)) throw new Error("not an array");
    } catch {
      setParamsError("Parameters must be a JSON array of {name, description?, default?, required?}");
      return;
    }
    setParamsError("");
    saveMut.mutate(parameters);
  }

  return (
    <Modal title={script ? `Edit "${script.name}"` : "New script"} onClose={onClose}>
      <form style={{ display: "grid", gap: 12 }} onSubmit={onSubmit}>
        <label>
          Name
          <input value={name} onChange={(e) => setName(e.target.value)} required />
        </label>
        <label>
          Description
          <input value={description} onChange={(e) => setDescription(e.target.value)} />
        </label>
        <label>
          Language
          <select
            value={language}
            onChange={(e) => setLanguage(e.target.value as api.ScriptLanguage)}
          >
            <option value="bash">bash</option>
            <option value="python">python</option>
            <option value="powershell">powershell</option>
            <option value="batch">batch</option>
          </select>
        </label>
        <label>
          Script body
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            rows={12}
            style={{ fontFamily: "monospace" }}
            required
          />
        </label>
        <label>
          Parameter definitions (JSON)
          <textarea
            value={paramsText}
            onChange={(e) => setParamsText(e.target.value)}
            rows={4}
            style={{ fontFamily: "monospace" }}
          />
        </label>
        {paramsError && <p className="error">{paramsError}</p>}
        <ErrorText error={saveMut.error} />
        <div className="row-actions">
          <button
            type="submit"
            className="primary"
            disabled={!name.trim() || !body.trim() || saveMut.isPending}
          >
            {script ? "Save" : "Create"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

export function RunScriptDialog({
  script,
  onClose,
}: {
  script: api.Script;
  onClose: () => void;
}) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [target, setTarget] = useState<api.JobTarget | null>(null);
  const [paramValues, setParamValues] = useState<Record<string, string>>(() => {
    const init: Record<string, string> = {};
    for (const p of script.parameters ?? []) init[p.name] = p.default ?? "";
    return init;
  });
  const [timeoutS, setTimeoutS] = useState(300);
  // Set when the server demands a blast-radius ack (409).
  const [confirmation, setConfirmation] = useState<api.DispatchConfirmation | null>(null);

  const runMut = useMutation({
    mutationFn: (confirmToken?: string) =>
      api.dispatchScript(script.id, {
        target: target!,
        parameters: paramValues,
        timeout_s: timeoutS,
        confirm_token: confirmToken,
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["jobs"] });
      onClose();
      navigate("/jobs");
    },
    onError: (err) => {
      const c = api.confirmationFrom(err);
      if (c) setConfirmation(c);
    },
  });

  const missingRequired = (script.parameters ?? []).some(
    (p) => p.required && !paramValues[p.name]?.trim(),
  );

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (target && !missingRequired) {
      setConfirmation(null);
      runMut.mutate(undefined);
    }
  }

  return (
    <Modal title={`Run "${script.name}"`} onClose={onClose}>
      <form style={{ display: "grid", gap: 12 }} onSubmit={onSubmit}>
        <TargetPicker onChange={setTarget} />
        {(script.parameters ?? []).map((p) => (
          <label key={p.name}>
            {p.name}
            {p.required ? " *" : ""}
            {p.description && <span className="muted"> — {p.description}</span>}
            <input
              value={paramValues[p.name] ?? ""}
              onChange={(e) =>
                setParamValues((prev) => ({ ...prev, [p.name]: e.target.value }))
              }
              required={p.required}
            />
          </label>
        ))}
        <label>
          Timeout (seconds)
          <input
            type="number"
            min={1}
            max={86400}
            value={timeoutS}
            onChange={(e) => setTimeoutS(Number(e.target.value))}
          />
        </label>

        {confirmation ? (
          <div className="warning">
            This will run on <strong>{confirmation.device_count} devices</strong>.
            Confirm to proceed.
          </div>
        ) : (
          <ErrorText error={runMut.error} />
        )}
        <div className="row-actions">
          {confirmation ? (
            <button
              type="button"
              className="danger"
              disabled={runMut.isPending}
              onClick={() => runMut.mutate(confirmation.confirm_token)}
            >
              Run on {confirmation.device_count} devices
            </button>
          ) : (
            <button
              type="submit"
              className="primary"
              disabled={!target || missingRequired || runMut.isPending}
            >
              Run
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
