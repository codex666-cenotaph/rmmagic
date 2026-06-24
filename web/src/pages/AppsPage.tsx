import { FormEvent, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import * as api from "../api/client";
import { ErrorText } from "../components/ui";
import { TargetPicker } from "../components/TargetPicker";

// AppsPage deploys OS packages (apt/dnf) across a target, reusing the same
// job pipeline and blast-radius safeguard as script dispatch.
export function AppsPage() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [operation, setOperation] = useState<api.PackageOperation>("install");
  const [packagesText, setPackagesText] = useState("");
  const [target, setTarget] = useState<api.JobTarget | null>(null);
  const [timeoutS, setTimeoutS] = useState(600);
  const [confirmation, setConfirmation] = useState<api.DispatchConfirmation | null>(null);

  const packages = packagesText
    .split(/[\s,]+/)
    .map((p) => p.trim())
    .filter(Boolean);

  const deployMut = useMutation({
    mutationFn: (confirmToken?: string) =>
      api.deployApp({
        operation,
        packages,
        target: target!,
        timeout_s: timeoutS,
        confirm_token: confirmToken,
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["jobs"] });
      navigate("/jobs");
    },
    onError: (err) => {
      const c = api.confirmationFrom(err);
      if (c) setConfirmation(c);
    },
  });

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (target && packages.length > 0) {
      setConfirmation(null);
      deployMut.mutate(undefined);
    }
  }

  return (
    <div>
      <h1>App Deployment</h1>
      <p className="muted">
        Install or remove OS packages on Linux endpoints (apt or dnf, chosen
        per host). Runs as a job you can track on the Jobs page.
      </p>
      <form style={{ display: "grid", gap: 12, maxWidth: 560 }} onSubmit={onSubmit}>
        <label>
          Operation
          <select
            value={operation}
            onChange={(e) => setOperation(e.target.value as api.PackageOperation)}
          >
            <option value="install">Install</option>
            <option value="remove">Remove</option>
          </select>
        </label>
        <label>
          Packages
          <input
            value={packagesText}
            onChange={(e) => setPackagesText(e.target.value)}
            placeholder="nginx curl htop"
            required
          />
          <span className="muted">Space- or comma-separated package names.</span>
        </label>
        <TargetPicker onChange={setTarget} />
        <label>
          Timeout (seconds)
          <input
            type="number"
            min={1}
            max={86400}
            value={timeoutS}
            onChange={(e) => setTimeoutS(Number(e.target.value))}
          />
        </label>

        {confirmation ? (
          <div className="warning">
            This will run on <strong>{confirmation.device_count} devices</strong>.
            Confirm to proceed.
          </div>
        ) : (
          <ErrorText error={deployMut.error} />
        )}
        <div className="row-actions">
          {confirmation ? (
            <button
              type="button"
              className="danger"
              disabled={deployMut.isPending}
              onClick={() => deployMut.mutate(confirmation.confirm_token)}
            >
              {operation === "install" ? "Install" : "Remove"} on{" "}
              {confirmation.device_count} devices
            </button>
          ) : (
            <button
              type="submit"
              className="primary"
              disabled={!target || packages.length === 0 || deployMut.isPending}
            >
              {operation === "install" ? "Install packages" : "Remove packages"}
            </button>
          )}
        </div>
      </form>
    </div>
  );
}
