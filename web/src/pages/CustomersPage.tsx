import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as api from "../api/client";
import { useAuth } from "../auth";
import { ErrorText, fmtTime } from "../components/ui";

export function CustomersPage() {
  const { can } = useAuth();
  const canManage = can("org.manage");
  const qc = useQueryClient();
  const customers = useQuery({ queryKey: ["customers"], queryFn: api.listCustomers });
  const [expanded, setExpanded] = useState<string | null>(null);
  const [newName, setNewName] = useState("");

  const createMut = useMutation({
    mutationFn: (name: string) => api.createCustomer(name),
    onSuccess: () => {
      setNewName("");
      void qc.invalidateQueries({ queryKey: ["customers"] });
    },
  });
  const renameMut = useMutation({
    mutationFn: ({ id, name }: { id: string; name: string }) =>
      api.renameCustomer(id, name),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["customers"] }),
  });
  const deleteMut = useMutation({
    mutationFn: (id: string) => api.deleteCustomer(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["customers"] }),
  });

  function onCreate(e: FormEvent) {
    e.preventDefault();
    if (newName.trim()) createMut.mutate(newName.trim());
  }

  if (customers.isLoading) return <p>Loading customers…</p>;
  if (customers.error)
    return (
      <p className="error">
        Failed to load customers: {(customers.error as Error).message}
      </p>
    );

  const list = customers.data?.customers ?? [];

  return (
    <div>
      <h1>Customers</h1>
      {canManage && (
        <form className="inline-form card" onSubmit={onCreate}>
          <label>
            New customer name
            <input
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              placeholder="Acme Corp"
              required
            />
          </label>
          <button type="submit" className="primary" disabled={createMut.isPending}>
            Create customer
          </button>
          <ErrorText error={createMut.error} />
        </form>
      )}
      {list.length === 0 && <p className="muted">No customers yet.</p>}
      <table className="data">
        <thead>
          <tr>
            <th style={{ width: 30 }} />
            <th>Name</th>
            <th>Created</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {list.map((c) => (
            <CustomerRow
              key={c.id}
              customer={c}
              expanded={expanded === c.id}
              onToggle={() => setExpanded(expanded === c.id ? null : c.id)}
              canManage={canManage}
              onRename={(name) => renameMut.mutate({ id: c.id, name })}
              onDelete={() => {
                if (confirm(`Delete customer "${c.name}"?`)) deleteMut.mutate(c.id);
              }}
            />
          ))}
        </tbody>
      </table>
      <ErrorText error={renameMut.error} />
      <ErrorText error={deleteMut.error} />
    </div>
  );
}

function CustomerRow({
  customer,
  expanded,
  onToggle,
  canManage,
  onRename,
  onDelete,
}: {
  customer: api.Customer;
  expanded: boolean;
  onToggle: () => void;
  canManage: boolean;
  onRename: (name: string) => void;
  onDelete: () => void;
}) {
  return (
    <>
      <tr>
        <td>
          <button type="button" className="link" onClick={onToggle}>
            {expanded ? "▾" : "▸"}
          </button>
        </td>
        <td>{customer.name}</td>
        <td>{fmtTime(customer.created_at)}</td>
        <td>
          {canManage && (
            <span className="row-actions">
              <button
                type="button"
                onClick={() => {
                  const name = prompt("New name", customer.name);
                  if (name && name.trim() && name !== customer.name)
                    onRename(name.trim());
                }}
              >
                Rename
              </button>
              <button type="button" className="danger" onClick={onDelete}>
                Delete
              </button>
            </span>
          )}
        </td>
      </tr>
      {expanded && (
        <tr>
          <td />
          <td colSpan={3}>
            <SitesPanel customerId={customer.id} canManage={canManage} />
          </td>
        </tr>
      )}
    </>
  );
}

function SitesPanel({
  customerId,
  canManage,
}: {
  customerId: string;
  canManage: boolean;
}) {
  const qc = useQueryClient();
  const sites = useQuery({
    queryKey: ["sites", customerId],
    queryFn: () => api.listSites(customerId),
  });
  const [name, setName] = useState("");
  const [timezone, setTimezone] = useState("");

  const invalidate = () =>
    void qc.invalidateQueries({ queryKey: ["sites", customerId] });

  const createMut = useMutation({
    mutationFn: () => api.createSite(customerId, name.trim(), timezone.trim() || undefined),
    onSuccess: () => {
      setName("");
      setTimezone("");
      invalidate();
    },
  });
  const renameMut = useMutation({
    mutationFn: ({ id, newName }: { id: string; newName: string }) =>
      api.updateSite(id, { name: newName }),
    onSuccess: invalidate,
  });
  const deleteMut = useMutation({
    mutationFn: (id: string) => api.deleteSite(id),
    onSuccess: invalidate,
  });

  if (sites.isLoading) return <p>Loading sites…</p>;
  if (sites.error)
    return (
      <p className="error">Failed to load sites: {(sites.error as Error).message}</p>
    );

  const list = sites.data?.sites ?? [];

  return (
    <div>
      <strong>Sites</strong>
      {list.length === 0 ? (
        <p className="muted">No sites.</p>
      ) : (
        <table className="data" style={{ margin: "8px 0" }}>
          <thead>
            <tr>
              <th>Name</th>
              <th>Timezone</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {list.map((s) => (
              <tr key={s.id}>
                <td>{s.name}</td>
                <td>{s.timezone || "—"}</td>
                <td>
                  {canManage && (
                    <span className="row-actions">
                      <button
                        type="button"
                        onClick={() => {
                          const newName = prompt("New site name", s.name);
                          if (newName && newName.trim() && newName !== s.name)
                            renameMut.mutate({ id: s.id, newName: newName.trim() });
                        }}
                      >
                        Rename
                      </button>
                      <button
                        type="button"
                        className="danger"
                        onClick={() => {
                          if (confirm(`Delete site "${s.name}"?`))
                            deleteMut.mutate(s.id);
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
      )}
      {canManage && (
        <form
          className="inline-form"
          onSubmit={(e) => {
            e.preventDefault();
            if (name.trim()) createMut.mutate();
          }}
        >
          <label>
            Site name
            <input value={name} onChange={(e) => setName(e.target.value)} required />
          </label>
          <label>
            Timezone (optional)
            <input
              value={timezone}
              onChange={(e) => setTimezone(e.target.value)}
              placeholder="America/New_York"
            />
          </label>
          <button type="submit" disabled={createMut.isPending}>
            Add site
          </button>
        </form>
      )}
      <ErrorText error={createMut.error} />
      <ErrorText error={renameMut.error} />
      <ErrorText error={deleteMut.error} />
    </div>
  );
}
