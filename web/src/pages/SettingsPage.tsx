import { FormEvent, useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as api from "../api/client";
import { useAuth } from "../auth";
import { CopyButton, ErrorText } from "../components/ui";

export function SettingsPage() {
  const { me, can } = useAuth();

  return (
    <div>
      <h1>Settings</h1>
      <div className="card">
        <h2 style={{ marginTop: 0, fontSize: 17 }}>Account</h2>
        <p>
          Signed in as <strong>{me.user.email}</strong> in tenant{" "}
          <strong>{me.tenant.name}</strong> <span className="muted">({me.tenant.slug})</span>
        </p>
      </div>
      <MfaSection />
      {can("tenant.manage") && <AssistantSection />}
    </div>
  );
}

// Default model ids surfaced as placeholders per provider.
const PROVIDER_MODELS: Record<api.AssistantProvider, string> = {
  anthropic: "claude-opus-4-8",
  mistral: "mistral-large-latest",
};

function AssistantSection() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ["assistant-settings"],
    queryFn: api.getAssistantSettings,
  });

  const [enabled, setEnabled] = useState(false);
  const [provider, setProvider] = useState<api.AssistantProvider>("anthropic");
  const [model, setModel] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [saved, setSaved] = useState(false);

  // Hydrate the form once settings load.
  useEffect(() => {
    if (data) {
      setEnabled(data.enabled);
      setProvider(data.provider);
      setModel(data.model);
    }
  }, [data]);

  const saveMut = useMutation({
    mutationFn: () =>
      api.updateAssistantSettings({
        enabled,
        provider,
        model: model.trim(),
        // Only send the key when the admin typed a new one.
        api_key: apiKey.trim() ? apiKey.trim() : undefined,
      }),
    onSuccess: () => {
      setApiKey("");
      setSaved(true);
      void qc.invalidateQueries({ queryKey: ["assistant-settings"] });
    },
  });

  function onSave(e: FormEvent) {
    e.preventDefault();
    setSaved(false);
    saveMut.mutate();
  }

  const keySet = data?.key_set ?? false;

  return (
    <div className="card">
      <h2 style={{ marginTop: 0, fontSize: 17 }}>AI assistant</h2>
      <p className="muted" style={{ marginTop: 0 }}>
        Configure the "Ask AI" assistant. The model can read your fleet and run
        the same actions you can — its tools run with each user's own
        permissions. The API key is stored encrypted and never shown again.
      </p>
      {isLoading ? (
        <p className="muted">Loading…</p>
      ) : (
        <form className="inline-form" onSubmit={onSave} style={{ flexDirection: "column", alignItems: "stretch", gap: 12 }}>
          <label style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <input
              type="checkbox"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
            />
            Enable the assistant for this tenant
          </label>

          <label>
            Provider
            <select
              value={provider}
              onChange={(e) =>
                setProvider(e.target.value as api.AssistantProvider)
              }
            >
              <option value="anthropic">Anthropic (Claude)</option>
              <option value="mistral">Mistral AI</option>
            </select>
          </label>

          <label>
            Model
            <input
              value={model}
              onChange={(e) => setModel(e.target.value)}
              placeholder={PROVIDER_MODELS[provider]}
            />
          </label>

          <label>
            API key
            <input
              type="password"
              autoComplete="off"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              placeholder={
                keySet ? "•••••••• (stored — leave blank to keep)" : "Paste API key"
              }
            />
          </label>

          <div className="row-actions">
            <button type="submit" className="primary" disabled={saveMut.isPending}>
              {saveMut.isPending ? "Saving…" : "Save"}
            </button>
            {saved && !saveMut.isPending && (
              <span className="badge on">saved</span>
            )}
          </div>
          <ErrorText error={saveMut.error} />
        </form>
      )}
    </div>
  );
}

function MfaSection() {
  const { me } = useAuth();
  const qc = useQueryClient();
  const [setup, setSetup] = useState<api.MfaSetupResponse | null>(null);
  const [code, setCode] = useState("");
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);

  const setupMut = useMutation({
    mutationFn: api.mfaSetup,
    onSuccess: (res) => setSetup(res),
  });
  const enableMut = useMutation({
    mutationFn: () => api.mfaEnable(code.trim()),
    onSuccess: (res) => {
      setRecoveryCodes(res.recovery_codes);
      setSetup(null);
      setCode("");
      void qc.invalidateQueries({ queryKey: ["me"] });
    },
  });

  function onEnable(e: FormEvent) {
    e.preventDefault();
    enableMut.mutate();
  }

  return (
    <div className="card">
      <h2 style={{ marginTop: 0, fontSize: 17 }}>Two-factor authentication</h2>
      {recoveryCodes ? (
        <div>
          <div className="warning">
            <strong>Save these recovery codes now.</strong> They are shown only
            once and are the only way back into your account if you lose your
            authenticator device.
          </div>
          <div className="secret-box">
            {recoveryCodes.map((c) => (
              <div key={c}>{c}</div>
            ))}
          </div>
          <div className="row-actions">
            <CopyButton text={recoveryCodes.join("\n")} label="Copy recovery codes" />
            <button type="button" onClick={() => setRecoveryCodes(null)}>
              I have saved my recovery codes
            </button>
          </div>
        </div>
      ) : me.user.mfa_enabled ? (
        <p>
          <span className="badge on">enabled</span> Two-factor authentication is
          active on your account.
        </p>
      ) : !setup ? (
        <div>
          <p>
            <span className="badge off">disabled</span> Protect your account with
            a TOTP authenticator app.
          </p>
          <button
            type="button"
            className="primary"
            onClick={() => setupMut.mutate()}
            disabled={setupMut.isPending}
          >
            {setupMut.isPending ? "Starting…" : "Set up MFA"}
          </button>
          <ErrorText error={setupMut.error} />
        </div>
      ) : (
        <div>
          <p>
            Add this account to your authenticator app. Open the app and either
            paste the otpauth URL or enter the secret manually:
          </p>
          <div style={{ fontWeight: 500 }}>otpauth URL</div>
          <div className="secret-box">{setup.otpauth_url}</div>
          <div style={{ fontWeight: 500 }}>Secret (for manual entry)</div>
          <div className="secret-box">{setup.secret}</div>
          <div className="row-actions" style={{ marginBottom: 12 }}>
            <CopyButton text={setup.otpauth_url} label="Copy URL" />
            <CopyButton text={setup.secret} label="Copy secret" />
          </div>
          <form className="inline-form" onSubmit={onEnable}>
            <label>
              6-digit code from your app
              <input
                value={code}
                onChange={(e) => setCode(e.target.value)}
                inputMode="numeric"
                pattern="[0-9]{6}"
                placeholder="123456"
                required
              />
            </label>
            <button type="submit" className="primary" disabled={enableMut.isPending}>
              {enableMut.isPending ? "Enabling…" : "Enable MFA"}
            </button>
            <button
              type="button"
              onClick={() => {
                setSetup(null);
                setCode("");
              }}
            >
              Cancel
            </button>
          </form>
          <ErrorText error={enableMut.error} />
        </div>
      )}
    </div>
  );
}
