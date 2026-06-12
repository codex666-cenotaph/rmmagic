import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as api from "../api/client";
import { useAuth } from "../auth";
import { CopyButton, ErrorText, fmtTime, Modal } from "../components/ui";

function tokenStatus(t: api.EnrollmentToken): "active" | "revoked" | "expired" {
  if (t.revoked_at) return "revoked";
  if (t.expires_at && new Date(t.expires_at).getTime() < Date.now())
    return "expired";
  return "active";
}

/** Default expiry: now + 24h, as a datetime-local input value. */
function defaultExpiry(): string {
  const d = new Date(Date.now() + 24 * 60 * 60 * 1000);
  d.setMinutes(d.getMinutes() - d.getTimezoneOffset());
  return d.toISOString().slice(0, 16);
}

export function EnrollPage() {
  const { can } = useAuth();
  const canEnroll = can("devices.enroll");
  const qc = useQueryClient();
  const tokens = useQuery({
    queryKey: ["enrollment-tokens"],
    queryFn: api.listEnrollmentTokens,
    enabled: canEnroll,
  });
  const [showCreate, setShowCreate] = useState(false);
  const [newToken, setNewToken] = useState<string | null>(null);

  const revokeMut = useMutation({
    mutationFn: (id: string) => api.revokeEnrollmentToken(id),
    onSuccess: () =>
      void qc.invalidateQueries({ queryKey: ["enrollment-tokens"] }),
  });

  if (!canEnroll)
    return (
      <p className="error">You do not have permission to enroll devices.</p>
    );
  if (tokens.isLoading) return <p>Loading enrollment tokens…</p>;
  if (tokens.error)
    return (
      <p className="error">
        Failed to load enrollment tokens: {(tokens.error as Error).message}
      </p>
    );

  const list = tokens.data?.tokens ?? [];
  const installCommand = (token: string) =>
    `rmmagent enroll --server ${window.location.origin} --token ${token} && rmmagent run`;

  return (
    <div>
      <h1>Enrollment Tokens</h1>
      <p>
        <button type="button" className="primary" onClick={() => setShowCreate(true)}>
          New token
        </button>
      </p>
      {list.length === 0 && <p className="muted">No enrollment tokens.</p>}
      <table className="data">
        <thead>
          <tr>
            <th>Site</th>
            <th>Expires</th>
            <th>Uses</th>
            <th>Status</th>
            <th>Created</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {list.map((t) => {
            const status = tokenStatus(t);
            return (
              <tr key={t.id}>
                <td>{t.site_name}</td>
                <td>{fmtTime(t.expires_at)}</td>
                <td>
                  {t.use_count} / {t.max_uses}
                </td>
                <td>
                  <span className={`badge ${status === "active" ? "on" : "off"}`}>
                    {status}
                  </span>
                </td>
                <td>{fmtTime(t.created_at)}</td>
                <td>
                  {status === "active" && (
                    <button
                      type="button"
                      className="danger"
                      onClick={() => {
                        if (
                          confirm(
                            `Revoke this enrollment token for "${t.site_name}"? Agents will no longer be able to enroll with it.`,
                          )
                        )
                          revokeMut.mutate(t.id);
                      }}
                    >
                      Revoke
                    </button>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <ErrorText error={revokeMut.error} />
      {showCreate && (
        <CreateEnrollmentTokenDialog
          onClose={() => setShowCreate(false)}
          onCreated={(token) => {
            setShowCreate(false);
            setNewToken(token);
            void qc.invalidateQueries({ queryKey: ["enrollment-tokens"] });
          }}
        />
      )}
      {newToken && (
        <Modal title="Enrollment token created" onClose={() => setNewToken(null)}>
          <div className="warning">
            This is the only time the token will be shown. Copy it now and store
            it securely.
          </div>
          <div className="secret-box">{newToken}</div>
          <div className="row-actions">
            <CopyButton text={newToken} label="Copy token" />
          </div>
          <p style={{ marginBottom: 4 }}>
            Run this on the device to enroll it:
          </p>
          <div className="secret-box">{installCommand(newToken)}</div>
          <div className="row-actions">
            <CopyButton text={installCommand(newToken)} label="Copy command" />
            <button type="button" onClick={() => setNewToken(null)}>
              Done
            </button>
          </div>
        </Modal>
      )}
    </div>
  );
}

function CreateEnrollmentTokenDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (token: string) => void;
}) {
  const customers = useQuery({ queryKey: ["customers"], queryFn: api.listCustomers });
  const [customerId, setCustomerId] = useState("");
  const [siteId, setSiteId] = useState("");
  const [expiresAt, setExpiresAt] = useState(defaultExpiry());
  const [maxUses, setMaxUses] = useState(1);

  const sites = useQuery({
    queryKey: ["sites", customerId],
    queryFn: () => api.listSites(customerId),
    enabled: customerId !== "",
  });

  const createMut = useMutation({
    mutationFn: () => {
      const body: Parameters<typeof api.createEnrollmentToken>[0] = {
        site_id: siteId,
      };
      if (expiresAt) body.expires_at = new Date(expiresAt).toISOString();
      if (maxUses > 0) body.max_uses = maxUses;
      return api.createEnrollmentToken(body);
    },
    onSuccess: (res) => onCreated(res.token),
  });

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (siteId) createMut.mutate();
  }

  return (
    <Modal title="New enrollment token" onClose={onClose}>
      <form style={{ display: "grid", gap: 12 }} onSubmit={onSubmit}>
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
        {customerId && (
          <label>
            Site
            <select value={siteId} onChange={(e) => setSiteId(e.target.value)} required>
              <option value="">
                {sites.isLoading ? "Loading sites…" : "Select a site…"}
              </option>
              {(sites.data?.sites ?? []).map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </label>
        )}
        <label>
          Expiry
          <input
            type="datetime-local"
            value={expiresAt}
            onChange={(e) => setExpiresAt(e.target.value)}
          />
        </label>
        <label>
          Max uses
          <input
            type="number"
            min={1}
            value={maxUses}
            onChange={(e) => setMaxUses(Number(e.target.value))}
          />
        </label>
        <ErrorText error={customers.error} />
        <ErrorText error={sites.error} />
        <ErrorText error={createMut.error} />
        <div className="row-actions">
          <button
            type="submit"
            className="primary"
            disabled={!siteId || createMut.isPending}
          >
            Create
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}
