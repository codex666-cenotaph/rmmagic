import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import * as api from "../api/client";
import { ErrorText, Modal, fmtRelative } from "../components/ui";

const STATUS_CLASS: Record<api.JobStatus, string> = {
  pending: "",
  sent: "",
  running: "",
  succeeded: "on",
  failed: "off",
  timed_out: "off",
  expired: "off",
};

export function JobStatusBadge({ status }: { status: api.JobStatus }) {
  return <span className={`badge ${STATUS_CLASS[status] ?? ""}`}>{status.replace("_", " ")}</span>;
}

// jobLabel describes what a job runs: a script by name, or a package
// install/remove with its package list.
export function jobLabel(j: api.Job): string {
  if (j.kind === "package_install" || j.kind === "package_remove") {
    const verb = j.kind === "package_install" ? "Install" : "Remove";
    const pkgs = j.spec?.packages?.join(", ") ?? "";
    return `${verb} ${pkgs}`;
  }
  return j.script_name ?? "script";
}

export function JobsPage() {
  const jobs = useQuery({
    queryKey: ["jobs"],
    queryFn: () => api.listJobs(),
    refetchInterval: 5_000,
  });
  const [outputJob, setOutputJob] = useState<api.Job | null>(null);

  if (jobs.isLoading) return <p>Loading jobs…</p>;
  if (jobs.error)
    return <p className="error">Failed to load jobs: {(jobs.error as Error).message}</p>;

  const list = jobs.data?.jobs ?? [];

  return (
    <div>
      <h1>Jobs</h1>
      {list.length === 0 && <p className="muted">No jobs have been dispatched yet.</p>}
      <table className="data">
        <thead>
          <tr>
            <th>Action</th>
            <th>Device</th>
            <th>Status</th>
            <th>Origin</th>
            <th>Created</th>
            <th>Finished</th>
            <th>Output</th>
          </tr>
        </thead>
        <tbody>
          {list.map((j) => (
            <tr key={j.id}>
              <td>{jobLabel(j)}</td>
              <td>{j.hostname}</td>
              <td>
                <JobStatusBadge status={j.status} />
              </td>
              <td>{j.schedule_id ? "schedule" : "manual"}</td>
              <td>{fmtRelative(j.created_at)}</td>
              <td>{fmtRelative(j.finished_at)}</td>
              <td>
                {["succeeded", "failed", "timed_out"].includes(j.status) && (
                  <button type="button" onClick={() => setOutputJob(j)}>
                    View
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {outputJob && <JobOutputDialog job={outputJob} onClose={() => setOutputJob(null)} />}
    </div>
  );
}

function JobOutputDialog({ job, onClose }: { job: api.Job; onClose: () => void }) {
  const output = useQuery({
    queryKey: ["job-output", job.id],
    queryFn: () => api.getJobOutput(job.id),
  });

  return (
    <Modal title={`${jobLabel(job)} on ${job.hostname}`} onClose={onClose}>
      <p>
        <JobStatusBadge status={job.status} />{" "}
        {output.data?.exit_code != null && (
          <span className="muted">exit code {output.data.exit_code}</span>
        )}
      </p>
      {output.isLoading && <p>Loading output…</p>}
      <ErrorText error={output.error} />
      {output.data && (
        <pre
          style={{
            maxHeight: 400,
            overflow: "auto",
            background: "#111",
            color: "#eee",
            padding: 12,
            borderRadius: 4,
          }}
        >
          {output.data.output || "(no output)"}
        </pre>
      )}
      <div className="row-actions">
        <button type="button" onClick={onClose}>
          Close
        </button>
      </div>
    </Modal>
  );
}
