import { useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import * as api from "../api/client";
import { fmtTime } from "../components/ui";

const PAGE_SIZE = 50;

export function AuditPage() {
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const query = useInfiniteQuery({
    queryKey: ["audit"],
    queryFn: ({ pageParam }) =>
      api.listAudit({ limit: PAGE_SIZE, before: pageParam ?? undefined }),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => {
      const entries = lastPage.entries;
      if (entries.length < PAGE_SIZE) return null;
      return entries[entries.length - 1].created_at;
    },
  });

  if (query.isLoading) return <p>Loading audit log…</p>;
  if (query.error)
    return (
      <p className="error">
        Failed to load audit log: {(query.error as Error).message}
      </p>
    );

  const entries = query.data?.pages.flatMap((p) => p.entries) ?? [];

  return (
    <div>
      <h1>Audit Log</h1>
      {entries.length === 0 && <p className="muted">No audit entries.</p>}
      <table className="data">
        <thead>
          <tr>
            <th>Time</th>
            <th>Actor</th>
            <th>Action</th>
            <th>Target</th>
            <th>IP</th>
            <th>Details</th>
          </tr>
        </thead>
        <tbody>
          {entries.map((e) => (
            <AuditRow
              key={e.id}
              entry={e}
              expanded={expandedId === e.id}
              onToggle={() => setExpandedId(expandedId === e.id ? null : e.id)}
            />
          ))}
        </tbody>
      </table>
      {query.hasNextPage && (
        <p>
          <button
            type="button"
            onClick={() => void query.fetchNextPage()}
            disabled={query.isFetchingNextPage}
          >
            {query.isFetchingNextPage ? "Loading…" : "Load more"}
          </button>
        </p>
      )}
      {query.isFetchNextPageError && (
        <p className="error">Failed to load more entries.</p>
      )}
    </div>
  );
}

function AuditRow({
  entry,
  expanded,
  onToggle,
}: {
  entry: api.AuditEntry;
  expanded: boolean;
  onToggle: () => void;
}) {
  return (
    <>
      <tr>
        <td style={{ whiteSpace: "nowrap" }}>{fmtTime(entry.created_at)}</td>
        <td>
          {entry.actor_type}
          <span className="muted">:{entry.actor_id}</span>
        </td>
        <td>{entry.action}</td>
        <td>
          {entry.target_type}
          {entry.target_id && <span className="muted">:{entry.target_id}</span>}
        </td>
        <td>{entry.ip || "—"}</td>
        <td>
          <button type="button" className="link" onClick={onToggle}>
            {expanded ? "hide" : "show"}
          </button>
        </td>
      </tr>
      {expanded && (
        <tr>
          <td colSpan={6}>
            <pre className="details">{JSON.stringify(entry.details, null, 2)}</pre>
          </td>
        </tr>
      )}
    </>
  );
}
