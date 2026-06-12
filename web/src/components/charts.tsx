import { ReactNode } from "react";

/** A single segment of a donut/legend. `color` is any CSS color string. */
export interface Segment {
  label: string;
  value: number;
  color: string;
}

export function StatCard({
  label,
  value,
  sub,
  tone = "default",
  icon,
}: {
  label: string;
  value: ReactNode;
  sub?: ReactNode;
  tone?: "default" | "ok" | "warn" | "error" | "accent";
  icon?: ReactNode;
}) {
  return (
    <div className={`stat-card tone-${tone}`}>
      <div className="stat-card-head">
        <span className="stat-label">{label}</span>
        {icon && <span className="stat-icon">{icon}</span>}
      </div>
      <div className="stat-value">{value}</div>
      {sub !== undefined && <div className="stat-sub">{sub}</div>}
    </div>
  );
}

/** SVG donut chart with a centered total and a legend beside it. */
export function Donut({
  segments,
  size = 168,
  thickness = 22,
  centerLabel,
}: {
  segments: Segment[];
  size?: number;
  thickness?: number;
  centerLabel?: string;
}) {
  const total = segments.reduce((s, x) => s + x.value, 0);
  const r = (size - thickness) / 2;
  const c = size / 2;
  const circ = 2 * Math.PI * r;
  let offset = 0;

  return (
    <div className="donut-wrap">
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} role="img">
        <circle
          cx={c}
          cy={c}
          r={r}
          fill="none"
          stroke="var(--track)"
          strokeWidth={thickness}
        />
        {total > 0 &&
          segments.map((seg) => {
            const frac = seg.value / total;
            const len = frac * circ;
            const dash = `${len} ${circ - len}`;
            const el = (
              <circle
                key={seg.label}
                cx={c}
                cy={c}
                r={r}
                fill="none"
                stroke={seg.color}
                strokeWidth={thickness}
                strokeDasharray={dash}
                strokeDashoffset={-offset}
                transform={`rotate(-90 ${c} ${c})`}
                strokeLinecap="butt"
              >
                <title>{`${seg.label}: ${seg.value}`}</title>
              </circle>
            );
            offset += len;
            return el;
          })}
        <text
          x={c}
          y={c - 4}
          textAnchor="middle"
          className="donut-total"
        >
          {total}
        </text>
        {centerLabel && (
          <text
            x={c}
            y={c + 16}
            textAnchor="middle"
            className="donut-center-label"
          >
            {centerLabel}
          </text>
        )}
      </svg>
      <ul className="legend">
        {segments.map((seg) => (
          <li key={seg.label}>
            <span className="legend-dot" style={{ background: seg.color }} />
            <span className="legend-label">{seg.label}</span>
            <span className="legend-value">{seg.value}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** Horizontal bar list — good for "by OS", "by version", "by status". */
export function BarRows({
  items,
  max,
  fmt = (n) => String(n),
  emptyText = "No data.",
}: {
  items: { label: string; value: number; color?: string }[];
  max?: number;
  fmt?: (n: number) => string;
  emptyText?: string;
}) {
  if (items.length === 0) return <p className="muted">{emptyText}</p>;
  const top = Math.max(max ?? 0, ...items.map((i) => i.value), 1);
  return (
    <div className="bar-rows">
      {items.map((it) => (
        <div className="bar-row" key={it.label}>
          <span className="bar-label" title={it.label}>
            {it.label}
          </span>
          <span className="bar-track">
            <span
              className="bar-fill"
              style={{
                width: `${(it.value / top) * 100}%`,
                background: it.color ?? "var(--accent)",
              }}
            />
          </span>
          <span className="bar-value">{fmt(it.value)}</span>
        </div>
      ))}
    </div>
  );
}

// Shared chart palette (semantic CSS variables defined in styles.css).
export const CHART = {
  online: "var(--ok)",
  offline: "var(--muted-2)",
  decommissioned: "var(--error)",
  warning: "var(--warn)",
  critical: "var(--error)",
  accent: "var(--accent)",
  series: [
    "var(--accent)",
    "var(--chart-2)",
    "var(--chart-3)",
    "var(--chart-4)",
    "var(--chart-5)",
    "var(--chart-6)",
  ],
};
