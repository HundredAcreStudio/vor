import { useState } from "react";
import {
  fetchGlobalSettings,
  putGlobalSetting,
  type GlobalSettings,
} from "../api.ts";
import { AsyncView, useAsync } from "../useAsync.tsx";

export function Settings() {
  const [nonce, setNonce] = useState(0);
  const state = useAsync(() => fetchGlobalSettings(), [nonce]);

  return (
    <main className="content">
      <header className="page-header">
        <h1>Settings</h1>
      </header>
      <AsyncView state={state}>
        {(s) => <SettingsBody settings={s} reload={() => setNonce((n) => n + 1)} />}
      </AsyncView>
    </main>
  );
}

function SettingsBody({
  settings,
  reload,
}: {
  settings: GlobalSettings;
  reload: () => void;
}) {
  const { effective, providerOptions, embedderOptions, providerKeys, global } = settings;

  return (
    <>
      <ProviderSection
        reload={reload}
        options={providerOptions}
        provider={effective.provider}
        model={effective.model}
        ready={global.providerConfigured}
      />
      <EmbedderSection
        reload={reload}
        options={embedderOptions}
        embedder={effective.embedder}
        embeddingModel={effective.embedding_model}
        ready={global.embedderConfigured}
      />
      <ApiKeysSection providerKeys={providerKeys} />
    </>
  );
}

function ProviderSection({
  reload,
  options,
  provider,
  model,
  ready,
}: {
  reload: () => void;
  options: string[];
  provider: string;
  model: string;
  ready: boolean;
}) {
  const [sel, setSel] = useState(provider);
  const [mdl, setMdl] = useState(model);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  function save() {
    setSaving(true);
    setMsg(null);
    Promise.all([putGlobalSetting("provider", sel), putGlobalSetting("model", mdl)])
      .then(() => {
        setMsg("Saved. Re-index repos for changes to take effect.");
        reload();
      })
      .catch((e: unknown) => setMsg(e instanceof Error ? e.message : String(e)))
      .finally(() => setSaving(false));
  }

  return (
    <section className="settings-section">
      <h2 className="section-title">LLM provider</h2>
      <p className="muted small">
        The provider and model used for wiki generation, decisions, and other
        LLM-backed analysis tasks across all repositories.
      </p>
      <div className="rule">
        <div className="rule-match">
          <select value={sel} onChange={(e) => setSel(e.target.value)}>
            {options.map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </select>
          <input
            className="wiki-search"
            placeholder="model"
            value={mdl}
            onChange={(e) => setMdl(e.target.value)}
          />
        </div>
        <p className="muted small">
          {ready
            ? "Provider ready."
            : "No usable provider — set a provider above and provide its API key in the environment."}
        </p>
        <div className="settings-actions">
          <span className="spacer" />
          <button className="btn" onClick={save} disabled={saving}>
            {saving ? "Saving…" : "Save"}
          </button>
        </div>
        {msg && <p className="muted small">{msg}</p>}
      </div>
    </section>
  );
}

function EmbedderSection({
  reload,
  options,
  embedder,
  embeddingModel,
  ready,
}: {
  reload: () => void;
  options: string[];
  embedder: string;
  embeddingModel: string;
  ready: boolean;
}) {
  const [sel, setSel] = useState(embedder);
  const [mdl, setMdl] = useState(embeddingModel);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  function save() {
    setSaving(true);
    setMsg(null);
    Promise.all([
      putGlobalSetting("embedder", sel),
      putGlobalSetting("embedding_model", mdl),
    ])
      .then(() => {
        setMsg("Saved. Re-index repos for changes to take effect.");
        reload();
      })
      .catch((e: unknown) => setMsg(e instanceof Error ? e.message : String(e)))
      .finally(() => setSaving(false));
  }

  return (
    <section className="settings-section">
      <h2 className="section-title">Embedder</h2>
      <p className="muted small">
        The embedding backend used for semantic search and similarity across all
        repositories.
      </p>
      <div className="rule">
        <div className="rule-match">
          <select value={sel} onChange={(e) => setSel(e.target.value)}>
            {options.map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </select>
          <input
            className="wiki-search"
            placeholder="embedding model"
            value={mdl}
            onChange={(e) => setMdl(e.target.value)}
          />
        </div>
        <p className="muted small">
          {ready ? "Embedder ready." : "No usable embedder configured."}
        </p>
        <div className="settings-actions">
          <span className="spacer" />
          <button className="btn" onClick={save} disabled={saving}>
            {saving ? "Saving…" : "Save"}
          </button>
        </div>
        {msg && <p className="muted small">{msg}</p>}
      </div>
    </section>
  );
}

function ApiKeysSection({ providerKeys }: { providerKeys: Record<string, boolean> }) {
  const entries = Object.entries(providerKeys);

  return (
    <section className="settings-section">
      <h2 className="section-title">API keys</h2>
      <p className="muted small">
        API keys are read from the daemon's environment (e.g. ANTHROPIC_API_KEY,
        OPENAI_API_KEY) and can't be edited here.
      </p>
      <div className="rule">
        <div className="rule-match">
          {entries.map(([name, detected]) => (
            <span
              key={name}
              className={"chip-toggle" + (detected ? " chip-toggle-good" : "")}
            >
              {name}
              {" — "}
              {detected ? "detected" : "not set"}
            </span>
          ))}
        </div>
      </div>
    </section>
  );
}
