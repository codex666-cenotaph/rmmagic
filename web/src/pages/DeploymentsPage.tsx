import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as api from "../api/client";
import { useAuth } from "../auth";
import { ErrorText } from "../components/ui";

// DeploymentsPage is the central, rule-based app deployment surface:
// reusable App Packages (what to install + how to detect it) and
// Deployment Rules that bind a package to a scope with tag/hostname
// filters. The worker reconciles enabled rules hourly, pushing installs
// to in-scope devices that don't already have the app.
export function DeploymentsPage() {
  const { can } = useAuth();
  const canManage = can("apps.manage");
  const qc = useQueryClient();
  const [showNewPkg, setShowNewPkg] = useState(false);
  const [editingPkg, setEditingPkg] = useState<api.AppPackage | null>(null);
  const [showNewRule, setShowNewRule] = useState(false);
  const [editingRule, setEditingRule] = useState<api.DeploymentRule | null>(
    null,
  );

  const packages = useQuery({
    queryKey: ["app-packages"],
    queryFn: () => api.listAppPackages(),
  });
  const rules = useQuery({
    queryKey: ["deployment-rules"],
    queryFn: api.listDeploymentRules,
  });

  const archivePkgMut = useMutation({
    mutationFn: (id: string) => api.archiveAppPackage(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["app-packages"] }),
  });
  const deleteRuleMut = useMutation({
    mutationFn: (id: string) => api.deleteDeploymentRule(id),
    onSuccess: () =>
      void qc.invalidateQueries({ queryKey: ["deployment-rules"] }),
  });

  const pkgList = packages.data?.packages ?? [];

  return (
    <div>
      <h1>App Deployment</h1>
      <p className="muted">
        Manage app packages centrally and connect them to devices with
        deployment rules. Rules reconcile hourly: each in-scope device that
        doesn’t already have the app gets an install job automatically.
      </p>

      <section>
        <div className="toolbar">
          <h2>Packages</h2>
          {canManage && (
            <button type="button" onClick={() => setShowNewPkg(true)}>
              New package
            </button>
          )}
        </div>
        <ErrorText error={packages.error} />
        {packages.isLoading && <p>Loading…</p>}
        {(showNewPkg || editingPkg) && (
          <PackageForm
            pkg={editingPkg ?? undefined}
            onClose={() => {
              setShowNewPkg(false);
              setEditingPkg(null);
            }}
            onSaved={() => {
              setShowNewPkg(false);
              setEditingPkg(null);
              void qc.invalidateQueries({ queryKey: ["app-packages"] });
            }}
          />
        )}
        {pkgList.length === 0 && !packages.isLoading && (
          <p className="muted">No packages yet.</p>
        )}
        {pkgList.map((pkg) => (
          <PackageCard
            key={pkg.id}
            pkg={pkg}
            canManage={canManage}
            onEdit={() => setEditingPkg(pkg)}
            onArchive={() => {
              if (confirm(`Archive package "${pkg.name}"?`))
                archivePkgMut.mutate(pkg.id);
            }}
          />
        ))}
      </section>

      <section style={{ marginTop: "2rem" }}>
        <div className="toolbar">
          <h2>Deployment Rules</h2>
          {canManage && (
            <button
              type="button"
              disabled={pkgList.length === 0}
              title={pkgList.length === 0 ? "Create a package first" : ""}
              onClick={() => setShowNewRule(true)}
            >
              New rule
            </button>
          )}
        </div>
        <ErrorText error={rules.error} />
        {rules.isLoading && <p>Loading…</p>}
        {(showNewRule || editingRule) && (
          <RuleForm
            rule={editingRule ?? undefined}
            packages={pkgList}
            onClose={() => {
              setShowNewRule(false);
              setEditingRule(null);
            }}
            onSaved={() => {
              setShowNewRule(false);
              setEditingRule(null);
              void qc.invalidateQueries({ queryKey: ["deployment-rules"] });
            }}
          />
        )}
        {(rules.data?.rules ?? []).length === 0 && !rules.isLoading && (
          <p className="muted">No deployment rules yet.</p>
        )}
        {(rules.data?.rules ?? []).map((rule) => (
          <RuleCard
            key={rule.id}
            rule={rule}
            canManage={canManage}
            onEdit={() => setEditingRule(rule)}
            onDelete={() => {
              if (confirm(`Delete rule "${rule.name}"?`))
                deleteRuleMut.mutate(rule.id);
            }}
          />
        ))}
      </section>
    </div>
  );
}

function PackageCard({
  pkg,
  canManage,
  onEdit,
  onArchive,
}: {
  pkg: api.AppPackage;
  canManage: boolean;
  onEdit: () => void;
  onArchive: () => void;
}) {
  const installPkgs = pkg.install?.packages ?? [];
  const detectNames = pkg.detection?.names ?? [];
  return (
    <div className="card">
      <div className="card-header">
        <strong>{pkg.name}</strong>
        <span className="muted">{pkg.os}</span>
        {canManage && (
          <>
            <button type="button" onClick={onEdit}>
              Edit
            </button>{" "}
            <button type="button" className="danger" onClick={onArchive}>
              Archive
            </button>
          </>
        )}
      </div>
      <div className="card-body">
        {pkg.description && <p>{pkg.description}</p>}
        <p className="muted">Installs: {installPkgs.join(", ") || "—"}</p>
        <p className="muted">
          Detect by:{" "}
          {detectNames.length > 0 ? detectNames.join(", ") : "install packages"}
        </p>
      </div>
    </div>
  );
}

function RuleCard({
  rule,
  canManage,
  onEdit,
  onDelete,
}: {
  rule: api.DeploymentRule;
  canManage: boolean;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const f = rule.filters ?? {};
  const filterLines: string[] = [];
  if (f.tags && f.tags.length > 0)
    filterLines.push(`tags ${f.tags_match ?? "any"}: ${f.tags.join(", ")}`);
  if (f.hostname_regex) filterLines.push(`hostname ~ /${f.hostname_regex}/`);
  return (
    <div className="card">
      <div className="card-header">
        <strong>{rule.name}</strong>
        <span className="muted">
          {rule.package_name} → {rule.scope_type}
          {rule.scope_id ? ` (${rule.scope_id})` : ""}
        </span>
        <span className={rule.enabled ? "badge badge-ok" : "badge"}>
          {rule.enabled ? "enabled" : "disabled"}
        </span>
        {canManage && (
          <>
            <button type="button" onClick={onEdit}>
              Edit
            </button>{" "}
            <button type="button" className="danger" onClick={onDelete}>
              Delete
            </button>
          </>
        )}
      </div>
      <div className="card-body">
        {filterLines.length > 0 ? (
          <ul className="rule-list">
            {filterLines.map((l) => (
              <li key={l}>{l}</li>
            ))}
          </ul>
        ) : (
          <p className="muted">All {rule.package_os} devices in scope.</p>
        )}
        {rule.last_run_at && (
          <p className="muted">
            Last reconciled {new Date(rule.last_run_at).toLocaleString()}
          </p>
        )}
      </div>
    </div>
  );
}

function PackageForm({
  pkg,
  onClose,
  onSaved,
}: {
  pkg?: api.AppPackage;
  onClose: () => void;
  onSaved: () => void;
}) {
  const qc = useQueryClient();
  const isEditing = !!pkg;
  const [name, setName] = useState(pkg?.name ?? "");
  const [description, setDescription] = useState(pkg?.description ?? "");
  const [os, setOs] = useState<api.PackageOS>(pkg?.os ?? "linux");
  const [packagesText, setPackagesText] = useState(
    (pkg?.install?.packages ?? []).join(" "),
  );
  const [detectText, setDetectText] = useState(
    (pkg?.detection?.names ?? []).join(" "),
  );
  const [timeoutS, setTimeoutS] = useState(String(pkg?.timeout_s ?? 600));
  const [err, setErr] = useState("");

  const mut = useMutation({
    mutationFn: async (body: api.AppPackageBody) => {
      if (isEditing) {
        await api.updateAppPackage(pkg!.id, body);
      } else {
        await api.createAppPackage(body);
      }
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["app-packages"] });
      onSaved();
    },
    onError: (e) => setErr((e as Error).message),
  });

  function tokenize(s: string): string[] {
    return s
      .split(/[\s,]+/)
      .map((p) => p.trim())
      .filter(Boolean);
  }

  function submit(ev: React.FormEvent) {
    ev.preventDefault();
    setErr("");
    const packages = tokenize(packagesText);
    if (packages.length === 0) {
      setErr("At least one package is required");
      return;
    }
    mut.mutate({
      name,
      description,
      os,
      packages,
      detection_names: tokenize(detectText),
      timeout_s: Number(timeoutS) || 600,
    });
  }

  return (
    <form className="card form" onSubmit={submit}>
      <h3>{isEditing ? "Edit package" : "New package"}</h3>
      <label>
        Name
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          required
        />
      </label>
      <label>
        Description
        <input
          value={description}
          onChange={(e) => setDescription(e.target.value)}
        />
      </label>
      <label>
        Platform
        <select
          value={os}
          onChange={(e) => setOs(e.target.value as api.PackageOS)}
        >
          <option value="linux">Linux (apt/dnf)</option>
          <option value="windows">Windows</option>
          <option value="darwin">macOS</option>
        </select>
      </label>
      <label>
        Install packages
        <input
          value={packagesText}
          onChange={(e) => setPackagesText(e.target.value)}
          placeholder="nginx curl"
          required
        />
        <span className="muted">Space- or comma-separated package names.</span>
      </label>
      <label>
        Detection names (optional)
        <input
          value={detectText}
          onChange={(e) => setDetectText(e.target.value)}
          placeholder="leave blank to detect by install packages"
        />
        <span className="muted">
          Package names whose presence in inventory means “already installed”.
        </span>
      </label>
      <label>
        Timeout (seconds)
        <input
          type="number"
          min={1}
          max={86400}
          value={timeoutS}
          onChange={(e) => setTimeoutS(e.target.value)}
        />
      </label>
      {err && <p className="error">{err}</p>}
      <div className="form-actions">
        <button type="submit" disabled={mut.isPending}>
          {isEditing ? "Save" : "Create"}
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </form>
  );
}

function RuleForm({
  rule,
  packages,
  onClose,
  onSaved,
}: {
  rule?: api.DeploymentRule;
  packages: api.AppPackage[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const qc = useQueryClient();
  const isEditing = !!rule;
  const [packageId, setPackageId] = useState(
    rule?.package_id ?? packages[0]?.id ?? "",
  );
  const [name, setName] = useState(rule?.name ?? "");
  const [scopeType, setScopeType] = useState<api.DeployScopeType>(
    rule?.scope_type ?? "tenant",
  );
  const [scopeCustomerId, setScopeCustomerId] = useState("");
  const [scopeSiteId, setScopeSiteId] = useState("");
  const [scopeDeviceId, setScopeDeviceId] = useState("");
  const [tagsText, setTagsText] = useState(
    (rule?.filters?.tags ?? []).join(" "),
  );
  const [tagsMatch, setTagsMatch] = useState<"any" | "all">(
    rule?.filters?.tags_match ?? "any",
  );
  const [hostnameRegex, setHostnameRegex] = useState(
    rule?.filters?.hostname_regex ?? "",
  );
  const [enabled, setEnabled] = useState(rule?.enabled ?? true);
  const [err, setErr] = useState("");

  const customers = useQuery({
    queryKey: ["customers"],
    queryFn: api.listCustomers,
    enabled: scopeType === "customer" || scopeType === "site",
  });
  const sites = useQuery({
    queryKey: ["sites", scopeCustomerId],
    queryFn: () => api.listSites(scopeCustomerId),
    enabled: scopeType === "site" && scopeCustomerId !== "",
  });
  const devices = useQuery({
    queryKey: ["devices"],
    queryFn: api.listDevices,
    enabled: scopeType === "device",
  });

  const mut = useMutation({
    mutationFn: async (body: api.DeploymentRuleBody) => {
      if (isEditing) {
        await api.updateDeploymentRule(rule!.id, body);
      } else {
        await api.createDeploymentRule(body);
      }
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["deployment-rules"] });
      onSaved();
    },
    onError: (e) => setErr((e as Error).message),
  });

  function scopeID(): string {
    switch (scopeType) {
      case "customer":
        return scopeCustomerId;
      case "site":
        return scopeSiteId;
      case "device":
        return scopeDeviceId;
      default:
        return "";
    }
  }

  function submit(ev: React.FormEvent) {
    ev.preventDefault();
    setErr("");
    if (!packageId) {
      setErr("Select a package");
      return;
    }
    const id = scopeID();
    if (scopeType !== "tenant" && !id) {
      setErr(`Select a ${scopeType} for this rule`);
      return;
    }
    const tags = tagsText
      .split(/[\s,]+/)
      .map((t) => t.trim().toLowerCase())
      .filter(Boolean);
    mut.mutate({
      package_id: packageId,
      name,
      scope_type: scopeType,
      scope_id: scopeType === "tenant" ? undefined : id,
      filters: {
        tags: tags.length > 0 ? tags : undefined,
        tags_match: tags.length > 0 ? tagsMatch : undefined,
        hostname_regex: hostnameRegex.trim() || undefined,
      },
      enabled,
    });
  }

  return (
    <form className="card form" onSubmit={submit}>
      <h3>{isEditing ? "Edit rule" : "New rule"}</h3>
      <label>
        Package
        <select
          value={packageId}
          onChange={(e) => setPackageId(e.target.value)}
          required
        >
          {packages.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name} ({p.os})
            </option>
          ))}
        </select>
      </label>
      <label>
        Name
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          required
        />
      </label>
      <label>
        Scope
        <select
          value={scopeType}
          onChange={(e) => {
            setScopeType(e.target.value as api.DeployScopeType);
            setScopeCustomerId("");
            setScopeSiteId("");
            setScopeDeviceId("");
          }}
        >
          <option value="tenant">Tenant (all devices)</option>
          <option value="customer">Customer</option>
          <option value="site">Site</option>
          <option value="device">Device</option>
        </select>
      </label>

      {(scopeType === "customer" || scopeType === "site") && (
        <label>
          Customer
          <select
            value={scopeCustomerId}
            onChange={(e) => {
              setScopeCustomerId(e.target.value);
              setScopeSiteId("");
            }}
            required
          >
            <option value="">Select…</option>
            {(customers.data?.customers ?? []).map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </label>
      )}

      {scopeType === "site" && (
        <label>
          Site
          <select
            value={scopeSiteId}
            onChange={(e) => setScopeSiteId(e.target.value)}
            disabled={!scopeCustomerId}
            required
          >
            <option value="">
              {scopeCustomerId ? "Select…" : "Pick a customer first"}
            </option>
            {(sites.data?.sites ?? []).map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </label>
      )}

      {scopeType === "device" && (
        <label>
          Device
          <select
            value={scopeDeviceId}
            onChange={(e) => setScopeDeviceId(e.target.value)}
            required
          >
            <option value="">Select…</option>
            {(devices.data?.devices ?? []).map((d) => (
              <option key={d.id} value={d.id}>
                {d.hostname}
              </option>
            ))}
          </select>
        </label>
      )}

      <fieldset>
        <legend>Filters (optional)</legend>
        <label>
          Tags
          <input
            value={tagsText}
            onChange={(e) => setTagsText(e.target.value)}
            placeholder="server prod"
          />
        </label>
        <label>
          Tag match
          <select
            value={tagsMatch}
            onChange={(e) => setTagsMatch(e.target.value as "any" | "all")}
          >
            <option value="any">Any of the tags</option>
            <option value="all">All of the tags</option>
          </select>
        </label>
        <label>
          Hostname regex
          <input
            value={hostnameRegex}
            onChange={(e) => setHostnameRegex(e.target.value)}
            placeholder="^web-"
          />
        </label>
      </fieldset>

      <label>
        <input
          type="checkbox"
          checked={enabled}
          onChange={(e) => setEnabled(e.target.checked)}
        />{" "}
        Enabled
      </label>

      {err && <p className="error">{err}</p>}
      <div className="form-actions">
        <button type="submit" disabled={mut.isPending}>
          {isEditing ? "Save" : "Create"}
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </form>
  );
}
