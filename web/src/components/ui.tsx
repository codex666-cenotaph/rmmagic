import { ReactNode, useState } from "react";

export function Modal({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
}) {
  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal" role="dialog" aria-label={title}>
        <h2>{title}</h2>
        {children}
      </div>
    </div>
  );
}

export function CopyButton({ text, label = "Copy" }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      onClick={() => {
        void navigator.clipboard.writeText(text).then(() => {
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        });
      }}
    >
      {copied ? "Copied!" : label}
    </button>
  );
}

export function ErrorText({ error }: { error: unknown }) {
  if (!error) return null;
  return (
    <p className="error">{error instanceof Error ? error.message : String(error)}</p>
  );
}

export function fmtTime(iso: string | null | undefined): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

export function fmtRelative(iso: string | null | undefined): string {
  if (!iso) return "never";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const diffSec = Math.floor((Date.now() - d.getTime()) / 1000);
  if (diffSec < 5) return "just now";
  if (diffSec < 60) return `${diffSec}s ago`;
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`;
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`;
  return `${Math.floor(diffSec / 86400)}d ago`;
}

export function fmtBytes(n: number): string {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${i === 0 ? Math.round(v) : v >= 10 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}`;
}

// Compare dotted version strings ("1.4.2"); returns >0 if a is newer.
export function compareVersions(a: string, b: string): number {
  const pa = a.split(/[.\-+]/);
  const pb = b.split(/[.\-+]/);
  const n = Math.max(pa.length, pb.length);
  for (let i = 0; i < n; i++) {
    const x = parseInt(pa[i] ?? "0", 10) || 0;
    const y = parseInt(pb[i] ?? "0", 10) || 0;
    if (x !== y) return x - y;
  }
  return 0;
}

export function DeviceStatusBadge({
  status,
  online,
}: {
  status: "active" | "decommissioned";
  online: boolean;
}) {
  if (status === "decommissioned")
    return <span className="badge off">decommissioned</span>;
  return (
    <span className={`badge ${online ? "on" : ""}`}>
      {online ? "online" : "offline"}
    </span>
  );
}
