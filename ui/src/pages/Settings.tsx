import { useState } from "react";
import {
  fetchGlobalSettings,
  putGlobalSetting,
  type GlobalSettings,
  type ModelCatalog,
} from "../api.ts";
import { AsyncView, useAsync } from "../useAsync.tsx";

// ModelField renders either a catalog-driven <select> (when the chosen
// provider/embedder has a catalog entry) or a free-text <input> fallback (when
// it doesn't — e.g. litellm has no enumerable model list).
function ModelField({
  catalog,
  provider,
  value,
  onChange,
  placeholder,
}: {
  catalog: ModelCatalog;
  provider: string;
  value: string;
  onChange: (v: string) => void;
  placeholder: string;
}) {
  const entry = catalog[provider];
  if (!entry) {
    return (
      <input
        className="wiki-search"
        placeholder={placeholder}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }
  // Ensure the current value is selectable even if it isn't in the catalog
  // (e.g. a previously-saved custom model).
  const models = entry.models.includes(value) || !value
    ? entry.models
    : [value, ...entry.models];
  return (
    <select value={value} onChange={(e) => onChange(e.target.value)}>
      {models.map((m) => (
        <option key={m} value={m}>
          {m}
        </option>
      ))}
    </select>
  );
}

// pickModel chooses the model to show after a provider changes: keep the
// current value if that provider can serve it, otherwise fall back to the
// provider's catalog default (or current value when there's no catalog).
function pickModel(catalog: ModelCatalog, provider: string, current: string): string {
  const entry = catalog[provider];
  if (!entry) return current;
  if (current && entry.models.includes(current)) return current;
  return entry.default;
}

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
  const { providerCatalog, embedderCatalog } = settings;

  return (
    <>
      <ProviderSection
        reload={reload}
        options={providerOptions}
        catalog={providerCatalog}
        provider={effective.provider}
        model={effective.model}
        ready={global.providerConfigured}
      />
      <EmbedderSection
        reload={reload}
        options={embedderOptions}
        catalog={embedderCatalog}
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
  catalog,
  provider,
  model,
  ready,
}: {
  reload: () => void;
  options: string[];
  catalog: ModelCatalog;
  provider: string;
  model: string;
  ready: boolean;
}) {
  const [sel, setSel] = useState(provider);
  const [mdl, setMdl] = useState(model);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  function changeProvider(next: string) {
    setSel(next);
    setMdl(pickModel(catalog, next, mdl));
  }

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
          <select value={sel} onChange={(e) => changeProvider(e.target.value)}>
            {options.map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </select>
          <ModelField
            catalog={catalog}
            provider={sel}
            value={mdl}
            onChange={setMdl}
            placeholder="model"
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
  catalog,
  embedder,
  embeddingModel,
  ready,
}: {
  reload: () => void;
  options: string[];
  catalog: ModelCatalog;
  embedder: string;
  embeddingModel: string;
  ready: boolean;
}) {
  const [sel, setSel] = useState(embedder);
  const [mdl, setMdl] = useState(embeddingModel);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  function changeEmbedder(next: string) {
    setSel(next);
    setMdl(pickModel(catalog, next, mdl));
  }

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
          <select value={sel} onChange={(e) => changeEmbedder(e.target.value)}>
            {options.map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </select>
          <ModelField
            catalog={catalog}
            provider={sel}
            value={mdl}
            onChange={setMdl}
            placeholder="embedding model"
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
