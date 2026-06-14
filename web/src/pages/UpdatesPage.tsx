import { FormEvent, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as api from "../api/client";
import { useAuth } from "../auth";
import { ErrorText, Modal, fmtBytes, fmtRelative } from "../components/ui";
import { TargetPicker } from "../components/TargetPicker";

const PHASE_CLASS: Record<api.DeviceUpdate["phase"], string> = {
  offered: "",
  downloading: "",
  verified: "",
  applied: "on",
  rolled_back: "off",
  failed: "off",
};

// UpdatesPage manages the signed agent-release catalog: register releases,
// roll one out to a target, and watch each device's update phase.
export function UpdatesPage() {
  const { can } = useAuth();
  const canManage = can("agent.update");
  const qc = useQueryClient();

  const releases = useQuery({ queryKey: ["agent-releases"], queryFn: () => api.listReleases() });
  const updates = useQuery({
    queryKey: ["device-updates"],
    queryFn: api.listDeviceUpdates,
    refetchInterval: 5_000,
  });
  const devices = useQuery({ queryKey: ["devices"], queryFn: api.listDevices });

  const [creating, setCreating] = useState(false);
  const [rolling, setRolling] = useState<api.AgentRelease | null>(null);

  const hostByID = useMemo(() => {
    const m = new Map<string, api.Device>();
    for (const d of devices.data?.devices ?? []) m.set(d.id, d);
    return m;
  }, [devices.data]);

  if (releases.isLoading) return <p>Loading releases…</p>;
  if (releases.error)
    return <p className="error">Failed to load releases: {(releases.error as Error).message}</p>;

  const list = releases.data?.releases ?? [];
  const updateList = updates.data?.updates ?? [];

  return (
    <div>
      <h1>Agent Updates</h1>
      <p className="muted">
        Releases are signed binaries agents verify (sha256 + Ed25519) before
        swapping. Roll one out to a site to upgrade its agents.
      </p>
      {canManage && (
        <p>
          <button type="button" className="primary" onClick={() => setCreating(true)}>
            Register release
          </button>
        </p>
      )}

      <h2>Release catalog</h2>
      {list.length === 0 && <p className="muted">No releases registered yet.</p>}
      {list.length > 0 && (
        <table className="data">
          <thead>
            <tr>
              <th>Version</th>
              <th>Channel</th>
              <th>Platform</th>
              <th>Binary</th>
              <th>Registered</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {list.map((r) => (
              <tr key={r.id}>
                <td>
                  {r.version}
                  {r.notes && <div className="muted">{r.notes}</div>}
                </td>
                <td>
                  <span className="badge">{r.channel}</span>
                </td>
                <td>
                  {r.os}/{r.arch}
                </td>
                <td>
                  {r.has_binary ? (
                    r.size_bytes ? fmtBytes(r.size_bytes) : "ready"
                  ) : (
                    <span className="badge off">no binary</span>
                  )}
                </td>
                <td>{fmtRelative(r.created_at)}</td>
                <td>
                  {canManage && (
                    <button
                      type="button"
                      className="primary"
                      disabled={!r.has_binary}
                      title={r.has_binary ? "" : "Upload a binary before rolling out"}
                      onClick={() => setRolling(r)}
                    >
                      Roll out
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <h2 style={{ marginTop: 24 }}>Rollout status</h2>
      {updateList.length === 0 && <p className="muted">No rollouts in progress.</p>}
      {updateList.length > 0 && (
        <table className="data">
          <thead>
            <tr>
              <th>Device</th>
              <th>Version</th>
              <th>Phase</th>
              <th>Updated</th>
            </tr>
          </thead>
          <tbody>
            {updateList.map((u) => (
              <tr key={u.device_id}>
                <td>{hostByID.get(u.device_id)?.hostname ?? u.device_id}</td>
                <td>{u.version}</td>
                <td>
                  <span className={`badge ${PHASE_CLASS[u.phase] ?? ""}`}>
                    {u.phase.replace("_", " ")}
                  </span>
                  {u.error && <div className="error">{u.error}</div>}
                </td>
                <td>{fmtRelative(u.updated_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {creating && (
        <CreateReleaseDialog
          onClose={() => setCreating(false)}
          onSaved={() => {
            setCreating(false);
            void qc.invalidateQueries({ queryKey: ["agent-releases"] });
          }}
        />
      )}
      {rolling && <RolloutDialog release={rolling} onClose={() => setRolling(null)} />}
    </div>
  );
}

async function sha256Hex(file: File): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", await file.arrayBuffer());
  return [...new Uint8Array(digest)]
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

function CreateReleaseDialog({
  onClose,
  onSaved,
}: {
  onClose: () => void;
  onSaved: () => void;
}) {
  const [channel, setChannel] = useState<api.ReleaseChannel>("stable");
  const [version, setVersion] = useState("");
  const [os, setOS] = useState("linux");
  const [arch, setArch] = useState("amd64");
  const [file, setFile] = useState<File | null>(null);
  const [sha256, setSHA256] = useState("");
  const [signature, setSignature] = useState("");
  const [notes, setNotes] = useState("");
  const [hashing, setHashing] = useState(false);

  async function onPickFile(f: File | null) {
    setFile(f);
    if (!f) return;
    setHashing(true);
    try {
      // Auto-fill sha256 from the chosen file so it matches the bytes the
      // server will store; the server rejects a mismatch.
      setSHA256(await sha256Hex(f));
    } finally {
      setHashing(false);
    }
  }

  const saveMut = useMutation({
    mutationFn: async () => {
      const { id } = await api.createRelease({
        channel,
        version: version.trim(),
        os: os.trim(),
        arch: arch.trim(),
        sha256: sha256.trim().toLowerCase(),
        signature: signature.trim(),
        notes,
      });
      if (file) await api.uploadReleaseBinary(id, file);
    },
    onSuccess: onSaved,
  });

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    saveMut.mutate();
  }

  const ready =
    version.trim() && sha256.trim() && signature.trim() && file && !hashing;

  return (
    <Modal title="Register agent release" onClose={onClose}>
      <form style={{ display: "grid", gap: 12 }} onSubmit={onSubmit}>
        <p className="muted">
          Pick the signed binary from the release; paste its signature from
          the pipeline's <code>agent_releases.json</code> manifest. The server
          stores the binary and serves it to agents behind device auth.
        </p>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12 }}>
          <label>
            Channel
            <select value={channel} onChange={(e) => setChannel(e.target.value as api.ReleaseChannel)}>
              <option value="stable">stable</option>
              <option value="beta">beta</option>
            </select>
          </label>
          <label>
            Version
            <input value={version} onChange={(e) => setVersion(e.target.value)} placeholder="1.4.0" required />
          </label>
          <label>
            OS
            <select value={os} onChange={(e) => setOS(e.target.value)}>
              <option value="linux">linux</option>
              <option value="windows">windows</option>
              <option value="darwin">darwin</option>
            </select>
          </label>
          <label>
            Arch
            <select value={arch} onChange={(e) => setArch(e.target.value)}>
              <option value="amd64">amd64</option>
              <option value="arm64">arm64</option>
            </select>
          </label>
        </div>
        <label>
          Binary
          <input
            type="file"
            onChange={(e) => void onPickFile(e.target.files?.[0] ?? null)}
            required
          />
        </label>
        <label>
          SHA-256 (hex){hashing ? " — computing…" : ""}
          <input value={sha256} onChange={(e) => setSHA256(e.target.value)} style={{ fontFamily: "monospace" }} required readOnly={!!file} />
        </label>
        <label>
          Signature (base64 Ed25519)
          <input value={signature} onChange={(e) => setSignature(e.target.value)} style={{ fontFamily: "monospace" }} required />
        </label>
        <label>
          Notes
          <input value={notes} onChange={(e) => setNotes(e.target.value)} />
        </label>
        <ErrorText error={saveMut.error} />
        <div className="row-actions">
          <button type="submit" className="primary" disabled={!ready || saveMut.isPending}>
            {saveMut.isPending ? "Uploading…" : "Register & upload"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

function RolloutDialog({
  release,
  onClose,
}: {
  release: api.AgentRelease;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [target, setTarget] = useState<api.JobTarget | null>(null);
  const [confirmation, setConfirmation] = useState<api.DispatchConfirmation | null>(null);
  const [result, setResult] = useState<{ matched: number; online_offered: number } | null>(null);

  const rollMut = useMutation({
    mutationFn: (confirmToken?: string) =>
      api.rolloutRelease(release.id, { target: target!, confirm_token: confirmToken }),
    onSuccess: (res) => {
      setResult(res);
      void qc.invalidateQueries({ queryKey: ["device-updates"] });
    },
    onError: (err) => {
      const c = api.confirmationFrom(err);
      if (c) setConfirmation(c);
    },
  });

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (target) {
      setConfirmation(null);
      rollMut.mutate(undefined);
    }
  }

  return (
    <Modal title={`Roll out ${release.version} (${release.os}/${release.arch})`} onClose={onClose}>
      {result ? (
        <div style={{ display: "grid", gap: 12 }}>
          <p>
            Offered to <strong>{result.matched}</strong> matching device(s);{" "}
            {result.online_offered} were online and received it immediately.
          </p>
          <p className="muted">
            Offline devices and the rest of the fleet pick up the offer when
            they reconnect. Track progress on this page.
          </p>
          <div className="row-actions">
            <button type="button" className="primary" onClick={onClose}>
              Done
            </button>
          </div>
        </div>
      ) : (
        <form style={{ display: "grid", gap: 12 }} onSubmit={onSubmit}>
          <p className="muted">
            Only devices on {release.os}/{release.arch} are offered this build.
          </p>
          <TargetPicker onChange={setTarget} />
          {confirmation ? (
            <div className="warning">
              This targets <strong>{confirmation.device_count} devices</strong>.
              Confirm to proceed.
            </div>
          ) : (
            <ErrorText error={rollMut.error} />
          )}
          <div className="row-actions">
            {confirmation ? (
              <button
                type="button"
                className="danger"
                disabled={rollMut.isPending}
                onClick={() => rollMut.mutate(confirmation.confirm_token)}
              >
                Roll out to {confirmation.device_count} devices
              </button>
            ) : (
              <button type="submit" className="primary" disabled={!target || rollMut.isPending}>
                Roll out
              </button>
            )}
            <button type="button" onClick={onClose}>
              Cancel
            </button>
          </div>
        </form>
      )}
    </Modal>
  );
}
