import { FormEvent, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { login, mfaVerify } from "../api/client";
import { ErrorText } from "../components/ui";

export function LoginPage() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [step, setStep] = useState<"credentials" | "mfa">("credentials");
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();
  const qc = useQueryClient();

  async function finishLogin() {
    await qc.invalidateQueries({ queryKey: ["me"] });
    navigate("/dashboard", { replace: true });
  }

  async function onSubmitCredentials(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const res = await login(email, password);
      if (res.mfa_required) {
        setStep("mfa");
      } else {
        await finishLogin();
      }
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  async function onSubmitMfa(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await mfaVerify(code.trim());
      await finishLogin();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="login-wrap">
      <h1>rmmagic</h1>
      {step === "credentials" ? (
        <form onSubmit={onSubmitCredentials}>
          <label>
            Email
            <input
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              autoComplete="username"
              required
            />
          </label>
          <label>
            Password
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              required
            />
          </label>
          <ErrorText error={error} />
          <button type="submit" className="primary" disabled={busy}>
            {busy ? "Signing in…" : "Sign in"}
          </button>
        </form>
      ) : (
        <form onSubmit={onSubmitMfa}>
          <p>
            Two-factor authentication is enabled. Enter the 6-digit code from
            your authenticator app, or a recovery code.
          </p>
          <label>
            Code
            <input
              value={code}
              onChange={(e) => setCode(e.target.value)}
              autoComplete="one-time-code"
              autoFocus
              required
            />
          </label>
          <ErrorText error={error} />
          <button type="submit" className="primary" disabled={busy}>
            {busy ? "Verifying…" : "Verify"}
          </button>
          <button
            type="button"
            className="link"
            onClick={() => {
              setStep("credentials");
              setCode("");
              setError(null);
            }}
          >
            Back
          </button>
        </form>
      )}
    </main>
  );
}
