import { useState } from "react";
import {
  fetchGlobalSettings,
  putGlobalSetting,
  type GlobalSettings,
  type ModelCatalog,
  type OptionStatus,
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

// statusByName indexes a status list for O(1) lookup by option name. Names
// absent from the backend status default to not-ready with no `requires`.
function statusByName(list: OptionStatus[]): Record<string, OptionStatus> {
  const m: Record<string, OptionStatus> = {};
  for (const s of list) m[s.name] = s;
  return m;
}

function lookupStatus(byName: Record<string, OptionStatus>, name: string): OptionStatus {
  return byName[name] ?? { name, ready: false, requires: "" };
}

// optionLabel builds the <option> text from an option's readiness:
//   ready              -> "name  ✓"
//   not ready, needs X -> "name — needs X"
//   not ready, no req  -> "name"
function optionLabel(name: string, byName: Record<string, OptionStatus>): string {
  const st = lookupStatus(byName, name);
  if (st.ready) return `${name}  ✓`;
  if (st.requires) return `${name} — needs ${st.requires}`;
  return name;
}

// ReadinessLine renders the status message for the currently selected
// provider/embedder. Ready -> a green positive line; not ready -> a specific
// line naming the missing env var, plus a hint listing options that are ready
// now (or, if none are, a nudge to set one up).
function ReadinessLine({
  selected,
  options,
  status,
}: {
  selected: string;
  options: string[];
  status: OptionStatus[];
}) {
  const byName = statusByName(status);
  const st = lookupStatus(byName, selected);
  if (st.ready) {
    return (
      <p className="muted small" style={{ color: "var(--good)" }}>
        ✓ {selected} is ready.
      </p>
    );
  }
  const readyNames = options.filter((o) => lookupStatus(byName, o).ready);
  const base = st.requires
    ? `${selected} needs ${st.requires} in the daemon's environment.`
    : `${selected} is not usable in the daemon's environment.`;
  const hint = readyNames.length
    ? ` Ready now: ${readyNames.join(", ")}.`
    : " Set one of the options' required env vars in the daemon's environment to enable it.";
  return (
    <p className="muted small">
      {base}
      {hint}
    </p>
  );
}

/** Settings renders the global settings page, loading effective provider,
 * embedder, and API-key configuration and delegating to SettingsBody. */
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

/** SettingsBody lays out the loaded settings as the provider, embedder, and
 * API-keys sections. */
function SettingsBody({
  settings,
  reload,
}: {
  settings: GlobalSettings;
  reload: () => void;
}) {
  const { effective, providerOptions, embedderOptions, providerKeys } = settings;
  const { providerStatus, embedderStatus } = settings;
  const { providerCatalog, embedderCatalog } = settings;

  return (
    <>
      <ProviderSection
        reload={reload}
        options={providerOptions}
        status={providerStatus}
        catalog={providerCatalog}
        provider={effective.provider}
        model={effective.model}
      />
      <EmbedderSection
        reload={reload}
        options={embedderOptions}
        status={embedderStatus}
        catalog={embedderCatalog}
        embedder={effective.embedder}
        embeddingModel={effective.embedding_model}
      />
      <ApiKeysSection providerKeys={providerKeys} />
    </>
  );
}

/** ProviderSection renders the LLM-provider selector and model field, with a
 * readiness line and a Save button that persists provider/model globally. */
function ProviderSection({
  reload,
  options,
  status,
  catalog,
  provider,
  model,
}: {
  reload: () => void;
  options: string[];
  status: OptionStatus[];
  catalog: ModelCatalog;
  provider: string;
  model: string;
}) {
  const [sel, setSel] = useState(provider);
  const [mdl, setMdl] = useState(model);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const byName = statusByName(status);

  function changeProvider(next: string) {
    setSel(next);
    setMdl(pickModel(catalog, next, mdl));
  }

  /** save persists the selected provider and model, then reloads settings. */
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
                {optionLabel(o, byName)}
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
        <ReadinessLine selected={sel} options={options} status={status} />
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

/** EmbedderSection renders the embedder selector and embedding-model field,
 * with a readiness line and a Save button that persists them globally. */
function EmbedderSection({
  reload,
  options,
  status,
  catalog,
  embedder,
  embeddingModel,
}: {
  reload: () => void;
  options: string[];
  status: OptionStatus[];
  catalog: ModelCatalog;
  embedder: string;
  embeddingModel: string;
}) {
  const [sel, setSel] = useState(embedder);
  const [mdl, setMdl] = useState(embeddingModel);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const byName = statusByName(status);

  function changeEmbedder(next: string) {
    setSel(next);
    setMdl(pickModel(catalog, next, mdl));
  }

  /** save persists the selected embedder and embedding model, then reloads
   * settings. */
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
                {optionLabel(o, byName)}
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
        <ReadinessLine selected={sel} options={options} status={status} />
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

/** ApiKeysSection renders a read-only chip per provider showing whether its API
 * key was detected in the daemon's environment. */
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
