import { FormEvent, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as api from "../api/client";
import { useAuth } from "../auth";
import { CopyButton, ErrorText, fmtTime, Modal } from "../components/ui";

export function TokensPage() {
  const { can } = useAuth();
  const canManage = can("tokens.manage");
  const qc = useQueryClient();
  const tokens = useQuery({ queryKey: ["api-tokens"], queryFn: api.listTokens });
  const [showCreate, setShowCreate] = useState(false);
  const [newToken, setNewToken] = useState<string | null>(null);

  const revokeMut = useMutation({
    mutationFn: (id: string) => api.revokeToken(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["api-tokens"] }),
  });

  if (tokens.isLoading) return <p>Loading tokens…</p>;
  if (tokens.error)
    return (
      <p className="error">Failed to load tokens: {(tokens.error as Error).message}</p>
    );

  const list = tokens.data?.tokens ?? [];

  return (
    <div>
      <h1>API Tokens</h1>
      {canManage && (
        <p>
          <button type="button" className="primary" onClick={() => setShowCreate(true)}>
            Create token
          </button>
        </p>
      )}
      {list.length === 0 && <p className="muted">No API tokens.</p>}
      <table className="data">
        <thead>
          <tr>
            <th>Name</th>
            <th>Permissions</th>
            <th>Scope</th>
            <th>Last used</th>
            <th>Expires</th>
            <th>Status</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {list.map((t) => (
            <tr key={t.id}>
              <td>{t.name}</td>
              <td>
                {t.permissions.map((p) => (
                  <span key={p} className="badge" style={{ marginRight: 4 }}>
                    {p}
                  </span>
                ))}
              </td>
              <td>
                {t.scope_type
                  ? `${t.scope_type}${t.scope_id ? `:${t.scope_id}` : ""}`
                  : "—"}
              </td>
              <td>{fmtTime(t.last_used_at)}</td>
              <td>{fmtTime(t.expires_at)}</td>
              <td>
                <span className={`badge ${t.revoked_at ? "off" : "on"}`}>
                  {t.revoked_at ? "revoked" : "active"}
                </span>
              </td>
              <td>
                {canManage && !t.revoked_at && (
                  <button
                    type="button"
                    className="danger"
                    onClick={() => {
                      if (confirm(`Revoke token "${t.name}"? This cannot be undone.`))
                        revokeMut.mutate(t.id);
                    }}
                  >
                    Revoke
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <ErrorText error={revokeMut.error} />
      {showCreate && (
        <CreateTokenDialog
          onClose={() => setShowCreate(false)}
          onCreated={(token) => {
            setShowCreate(false);
            setNewToken(token);
            void qc.invalidateQueries({ queryKey: ["api-tokens"] });
          }}
        />
      )}
      {newToken && (
        <Modal title="Token created" onClose={() => setNewToken(null)}>
          <div className="warning">
            This is the only time the token will be shown. Copy it now and store
            it securely.
          </div>
          <div className="secret-box">{newToken}</div>
          <div className="row-actions">
            <CopyButton text={newToken} label="Copy token" />
            <button type="button" onClick={() => setNewToken(null)}>
              Done
            </button>
          </div>
        </Modal>
      )}
    </div>
  );
}

function CreateTokenDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (token: string) => void;
}) {
  const { me } = useAuth();
  const customers = useQuery({ queryKey: ["customers"], queryFn: api.listCustomers });

  // The available permissions are the union of the caller's own grants.
  const availablePermissions = useMemo(() => {
    const set = new Set<string>();
    for (const g of me.grants) for (const p of g.permissions) set.add(p);
    return [...set].sort();
  }, [me]);

  const [name, setName] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [scopeType, setScopeType] = useState<"" | api.ScopeType>("");
  const [customerId, setCustomerId] = useState("");
  const [siteId, setSiteId] = useState("");
  const [expiresAt, setExpiresAt] = useState("");

  const sites = useQuery({
    queryKey: ["sites", customerId],
    queryFn: () => api.listSites(customerId),
    enabled: scopeType === "site" && customerId !== "",
  });

  const createMut = useMutation({
    mutationFn: () => {
      const body: Parameters<typeof api.createToken>[0] = {
        name: name.trim(),
        permissions: [...selected],
      };
      if (scopeType) {
        body.scope_type = scopeType;
        if (scopeType === "customer" && customerId) body.scope_id = customerId;
        if (scopeType === "site" && siteId) body.scope_id = siteId;
      }
      if (expiresAt) body.expires_at = new Date(expiresAt).toISOString();
      return api.createToken(body);
    },
    onSuccess: (res) => onCreated(res.token),
  });

  function togglePerm(p: string) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(p)) next.delete(p);
      else next.add(p);
      return next;
    });
  }

  const scopeReady =
    scopeType === "" ||
    scopeType === "tenant" ||
    (scopeType === "customer" && customerId !== "") ||
    (scopeType === "site" && siteId !== "");

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (name.trim() && selected.size > 0 && scopeReady) createMut.mutate();
  }

  return (
    <Modal title="Create API token" onClose={onClose}>
      <form style={{ display: "grid", gap: 12 }} onSubmit={onSubmit}>
        <label>
          Name
          <input value={name} onChange={(e) => setName(e.target.value)} required />
        </label>
        <div>
          <div style={{ fontWeight: 500, marginBottom: 4 }}>Permissions</div>
          {availablePermissions.length === 0 && (
            <p className="muted">You have no permissions to grant.</p>
          )}
          {availablePermissions.map((p) => (
            <label
              key={p}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 6,
                fontWeight: 400,
              }}
            >
              <input
                type="checkbox"
                checked={selected.has(p)}
                onChange={() => togglePerm(p)}
              />
              {p}
            </label>
          ))}
        </div>
        <label>
          Scope (optional)
          <select
            value={scopeType}
            onChange={(e) => {
              setScopeType(e.target.value as "" | api.ScopeType);
              setCustomerId("");
              setSiteId("");
            }}
          >
            <option value="">No scope restriction</option>
            <option value="tenant">Tenant</option>
            <option value="customer">Customer</option>
            <option value="site">Site</option>
          </select>
        </label>
        {(scopeType === "customer" || scopeType === "site") && (
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
        {scopeType === "site" && customerId && (
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
          Expiry (optional)
          <input
            type="datetime-local"
            value={expiresAt}
            onChange={(e) => setExpiresAt(e.target.value)}
          />
        </label>
        <ErrorText error={createMut.error} />
        <div className="row-actions">
          <button
            type="submit"
            className="primary"
            disabled={
              !name.trim() || selected.size === 0 || !scopeReady || createMut.isPending
            }
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
