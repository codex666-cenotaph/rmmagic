import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as api from "../api/client";
import { useAuth } from "../auth";
import { ErrorText, Modal } from "../components/ui";

export function UsersPage() {
  const { can } = useAuth();
  const canManage = can("users.manage");
  const qc = useQueryClient();
  const users = useQuery({ queryKey: ["users"], queryFn: api.listUsers });
  const [assigningUser, setAssigningUser] = useState<api.User | null>(null);

  const invalidate = () => void qc.invalidateQueries({ queryKey: ["users"] });

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const createMut = useMutation({
    mutationFn: () => api.createUser(email.trim(), password),
    onSuccess: () => {
      setEmail("");
      setPassword("");
      invalidate();
    },
  });
  const statusMut = useMutation({
    mutationFn: ({ id, status }: { id: string; status: "active" | "disabled" }) =>
      api.setUserStatus(id, status),
    onSuccess: invalidate,
  });
  const removeAssignMut = useMutation({
    mutationFn: (id: string) => api.deleteAssignment(id),
    onSuccess: invalidate,
  });

  function onCreate(e: FormEvent) {
    e.preventDefault();
    createMut.mutate();
  }

  if (users.isLoading) return <p>Loading users…</p>;
  if (users.error)
    return (
      <p className="error">Failed to load users: {(users.error as Error).message}</p>
    );

  const list = users.data?.users ?? [];

  return (
    <div>
      <h1>Users</h1>
      {canManage && (
        <form className="inline-form card" onSubmit={onCreate}>
          <label>
            Email
            <input
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
            />
          </label>
          <label>
            Initial password
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
            />
          </label>
          <button type="submit" className="primary" disabled={createMut.isPending}>
            Create user
          </button>
          <ErrorText error={createMut.error} />
        </form>
      )}
      <table className="data">
        <thead>
          <tr>
            <th>Email</th>
            <th>Status</th>
            <th>MFA</th>
            <th>Roles</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {list.map((u) => (
            <tr key={u.id}>
              <td>{u.email}</td>
              <td>
                <span className={`badge ${u.status === "active" ? "on" : "off"}`}>
                  {u.status}
                </span>
              </td>
              <td>
                <span className={`badge ${u.mfa_enabled ? "on" : "off"}`}>
                  {u.mfa_enabled ? "on" : "off"}
                </span>
              </td>
              <td>
                {u.assignments.length === 0 && <span className="muted">none</span>}
                {u.assignments.map((a) => (
                  <div key={a.id} className="row-actions" style={{ marginBottom: 4 }}>
                    <span>
                      {a.role_name}{" "}
                      <span className="muted">
                        ({a.scope_type}
                        {a.scope_id ? `:${a.scope_id}` : ""})
                      </span>
                    </span>
                    {canManage && (
                      <button
                        type="button"
                        className="link danger"
                        onClick={() => {
                          if (confirm(`Remove role "${a.role_name}" from ${u.email}?`))
                            removeAssignMut.mutate(a.id);
                        }}
                      >
                        remove
                      </button>
                    )}
                  </div>
                ))}
              </td>
              <td>
                {canManage && (
                  <span className="row-actions">
                    <button type="button" onClick={() => setAssigningUser(u)}>
                      Assign role
                    </button>
                    {u.status === "active" ? (
                      <button
                        type="button"
                        className="danger"
                        onClick={() => statusMut.mutate({ id: u.id, status: "disabled" })}
                      >
                        Disable
                      </button>
                    ) : (
                      <button
                        type="button"
                        onClick={() => statusMut.mutate({ id: u.id, status: "active" })}
                      >
                        Enable
                      </button>
                    )}
                  </span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <ErrorText error={statusMut.error} />
      <ErrorText error={removeAssignMut.error} />
      {assigningUser && (
        <AssignRoleDialog
          user={assigningUser}
          onClose={() => setAssigningUser(null)}
          onDone={() => {
            setAssigningUser(null);
            invalidate();
          }}
        />
      )}
    </div>
  );
}

function AssignRoleDialog({
  user,
  onClose,
  onDone,
}: {
  user: api.User;
  onClose: () => void;
  onDone: () => void;
}) {
  const roles = useQuery({ queryKey: ["roles"], queryFn: api.listRoles });
  const customers = useQuery({ queryKey: ["customers"], queryFn: api.listCustomers });
  const [roleId, setRoleId] = useState("");
  const [scopeType, setScopeType] = useState<api.ScopeType>("tenant");
  const [customerId, setCustomerId] = useState("");
  const [siteId, setSiteId] = useState("");

  const sites = useQuery({
    queryKey: ["sites", customerId],
    queryFn: () => api.listSites(customerId),
    enabled: scopeType === "site" && customerId !== "",
  });

  const assignMut = useMutation({
    mutationFn: () => {
      const body: { role_id: string; scope_type: api.ScopeType; scope_id?: string } = {
        role_id: roleId,
        scope_type: scopeType,
      };
      if (scopeType === "customer") body.scope_id = customerId;
      if (scopeType === "site") body.scope_id = siteId;
      return api.createAssignment(user.id, body);
    },
    onSuccess: onDone,
  });

  const scopeReady =
    scopeType === "tenant" ||
    (scopeType === "customer" && customerId !== "") ||
    (scopeType === "site" && siteId !== "");

  return (
    <Modal title={`Assign role to ${user.email}`} onClose={onClose}>
      <form
        style={{ display: "grid", gap: 12 }}
        onSubmit={(e) => {
          e.preventDefault();
          if (roleId && scopeReady) assignMut.mutate();
        }}
      >
        <label>
          Role
          <select value={roleId} onChange={(e) => setRoleId(e.target.value)} required>
            <option value="">Select a role…</option>
            {(roles.data?.roles ?? []).map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
                {r.is_builtin ? " (built-in)" : ""}
              </option>
            ))}
          </select>
        </label>
        <label>
          Scope type
          <select
            value={scopeType}
            onChange={(e) => {
              setScopeType(e.target.value as api.ScopeType);
              setSiteId("");
            }}
          >
            <option value="tenant">Tenant (entire organization)</option>
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
        <ErrorText error={roles.error} />
        <ErrorText error={customers.error} />
        <ErrorText error={sites.error} />
        <ErrorText error={assignMut.error} />
        <div className="row-actions">
          <button
            type="submit"
            className="primary"
            disabled={!roleId || !scopeReady || assignMut.isPending}
          >
            Assign
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}
