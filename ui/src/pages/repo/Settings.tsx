import { useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import {
  clearRepoSetting,
  deleteRepo,
  fetchRepoSettings,
  fetchTasks,
  putRepoSetting,
  regenerateWiki,
  reindexRepo,
  rescanBiomarkers,
  setTask,
  type HealthRule,
  type ModelCatalog,
  type RepoSettings,
  type TaskInfo,
  type TasksResponse,
} from "../../api.ts";
import { AsyncView, useAsync } from "../../useAsync.tsx";
import { MetricTip } from "../../MetricTip.tsx";

/** Renders the repo Settings page: loads per-repo settings and renders SettingsBody, with a nonce-driven reload. */
export function Settings() {
  const { repoId = "" } = useParams();
  const [nonce, setNonce] = useState(0);
  const state = useAsync(() => fetchRepoSettings(repoId), [repoId, nonce]);

  return (
    <div className="settings-page">
      <header className="page-header">
        <h1>Settings</h1>
      </header>
      <AsyncView state={state}>
        {(s) => <SettingsBody repoId={repoId} settings={s} reload={() => setNonce((n) => n + 1)} />}
      </AsyncView>
    </div>
  );
}

// EditRule is the editable form of a HealthRule: one match (a path prefix or a
// glob pattern) plus the biomarkers it disables.
type EditRule = { kind: "path" | "pattern"; match: string; biomarkers: string[] };

/** Converts stored HealthRules into editable EditRule form, classifying each as a glob pattern or path prefix. */
function toEdit(rules: HealthRule[] | null): EditRule[] {
  return (rules ?? []).map((r) => ({
    kind: r.pattern ? "pattern" : "path",
    match: r.pattern ?? r.path ?? "",
    biomarkers: Object.keys(r.overrides ?? {}),
  }));
}

/** Converts editable EditRules back into HealthRules, dropping empty rules and marking selected biomarkers disabled. */
function toRules(edits: EditRule[]): HealthRule[] {
  return edits
    .filter((e) => e.match.trim() && e.biomarkers.length > 0)
    .map((e) => ({
      [e.kind]: e.match.trim(),
      overrides: Object.fromEntries(e.biomarkers.map((b) => [b, "disabled"])),
    }));
}

/** Main settings body: tasks, generation overrides, health-rule exclusions editor, and the danger zone. */
function SettingsBody({
  repoId,
  settings,
  reload,
}: {
  repoId: string;
  settings: RepoSettings;
  reload: () => void;
}) {
  const navigate = useNavigate();
  const [rules, setRules] = useState<EditRule[]>(() => toEdit(settings.effective.health_rules));
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const overridden = !!settings.overridden["health_rules"];

  function update(i: number, patch: Partial<EditRule>) {
    setRules((rs) => rs.map((r, idx) => (idx === i ? { ...r, ...patch } : r)));
  }
  /** Adds or removes biomarker `b` from rule `i`'s disabled set. */
  function toggleBiomarker(i: number, b: string) {
    setRules((rs) =>
      rs.map((r, idx) =>
        idx === i
          ? {
              ...r,
              biomarkers: r.biomarkers.includes(b)
                ? r.biomarkers.filter((x) => x !== b)
                : [...r.biomarkers, b],
            }
          : r,
      ),
    );
  }

  /** Persists the edited exclusion rules as the repo's health_rules setting. */
  function save() {
    setSaving(true);
    setMsg(null);
    putRepoSetting(repoId, "health_rules", toRules(rules))
      .then(() => {
        setMsg("Saved. Re-index the repo for changes to take effect.");
        reload();
      })
      .catch((e: unknown) => setMsg(e instanceof Error ? e.message : String(e)))
      .finally(() => setSaving(false));
  }

  /** Clears the repo's health_rules override, reverting to global defaults. */
  function resetToGlobal() {
    setSaving(true);
    clearRepoSetting(repoId, "health_rules")
      .then(() => {
        setMsg("Reset to global defaults.");
        reload();
      })
      .catch((e: unknown) => setMsg(e instanceof Error ? e.message : String(e)))
      .finally(() => setSaving(false));
  }

  return (
    <>
      <TasksSection repoId={repoId} />

      <GenerationSection repoId={repoId} settings={settings} reload={reload} />

      <section className="settings-section">
        <h2 className="section-title">Exclude files from health checks</h2>
        <p className="muted small">
          Each rule suppresses the selected biomarkers for files matching a path
          prefix or a glob pattern.{" "}
          {overridden ? "Customised for this repo." : "Currently using global defaults."}
        </p>

        <div className="rules">
          {rules.map((r, i) => (
            <div className="rule" key={i}>
              <div className="rule-match">
                <select
                  value={r.kind}
                  onChange={(e) => update(i, { kind: e.target.value as EditRule["kind"] })}
                >
                  <option value="path">path prefix</option>
                  <option value="pattern">glob pattern</option>
                </select>
                <input
                  className="wiki-search"
                  placeholder={r.kind === "path" ? "internal/generated/" : "**/*_test.go"}
                  value={r.match}
                  onChange={(e) => update(i, { match: e.target.value })}
                />
                <button className="btn-ghost" onClick={() => setRules((rs) => rs.filter((_, idx) => idx !== i))}>
                  Remove
                </button>
              </div>
              <div className="rule-biomarkers">
                {settings.biomarkers.map((b) => (
                  <label key={b} className={"chip-toggle" + (r.biomarkers.includes(b) ? " chip-toggle-on" : "")}>
                    <input
                      type="checkbox"
                      checked={r.biomarkers.includes(b)}
                      onChange={() => toggleBiomarker(i, b)}
                    />
                    <MetricTip id={b}>{b.replace(/_/g, " ")}</MetricTip>
                  </label>
                ))}
              </div>
            </div>
          ))}
        </div>

        <div className="settings-actions">
          <button
            className="btn-ghost"
            onClick={() => setRules((rs) => [...rs, { kind: "path", match: "", biomarkers: [] }])}
          >
            + Add exclusion rule
          </button>
          <span className="spacer" />
          {overridden && (
            <button className="btn-ghost" onClick={resetToGlobal} disabled={saving}>
              Reset to global
            </button>
          )}
          <button className="btn" onClick={save} disabled={saving}>
            {saving ? "Saving…" : "Save"}
          </button>
        </div>
        {msg && <p className="muted small">{msg}</p>}
      </section>

      <section className="settings-section">
        <h2 className="section-title">Maintenance</h2>
        <MaintenanceActions repoId={repoId} />
      </section>

      <section className="settings-section danger">
        <h2 className="section-title">Danger zone</h2>
        <DeleteRepo repoId={repoId} onDeleted={() => navigate("/repositories")} />
      </section>
    </>
  );
}

/** Settings section that loads the repo's analysis tasks and renders TasksBody, with nonce-driven reload. */
function TasksSection({ repoId }: { repoId: string }) {
  const [nonce, setNonce] = useState(0);
  const state = useAsync(() => fetchTasks(repoId), [repoId, nonce]);

  return (
    <section className="settings-section">
      <h2 className="section-title">Tasks</h2>
      <p className="muted small">
        Choose which analysis tasks run for this repository. Changes apply on the
        next run.
      </p>
      <AsyncView state={state}>
        {(data) => (
          <TasksBody repoId={repoId} data={data} reload={() => setNonce((n) => n + 1)} />
        )}
      </AsyncView>
    </section>
  );
}

/** Renders per-repo analysis task toggles with optimistic updates and unmet-requirement hints. */
function TasksBody({
  repoId,
  data,
  reload,
}: {
  repoId: string;
  data: TasksResponse;
  reload: () => void;
}) {
  // Local mirror so toggles feel instant; we still reload to pick up any
  // server-side recomputation of `overridden`.
  const [tasks, setTasks] = useState<TaskInfo[]>(data.tasks);
  const [busy, setBusy] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);

  /** Optimistically flips a task's enabled flag, persisting via the API and rolling back on failure. */
  function toggle(task: TaskInfo) {
    const next = !task.enabled;
    setBusy(task.id);
    setMsg(null);
    setTasks((ts) => ts.map((t) => (t.id === task.id ? { ...t, enabled: next } : t)));
    setTask(repoId, task.id, next)
      .then(() => reload())
      .catch((e: unknown) => {
        // Roll back the optimistic change on failure.
        setTasks((ts) => ts.map((t) => (t.id === task.id ? { ...t, enabled: task.enabled } : t)));
        setMsg(e instanceof Error ? e.message : String(e));
      })
      .finally(() => setBusy(null));
  }

  // A requirement is met when its corresponding capability is configured.
  // Drive everything off `t.requires` so new tasks/requirements work without
  // any task-ID special-casing.
  const REQUIREMENT_LABELS: Record<string, string> = {
    provider: "an LLM provider",
    embedder: "an embedder",
  };
  const configured: Record<string, boolean> = {
    provider: data.providerConfigured,
    embedder: data.embedderConfigured,
  };
  /** Builds a hint listing any unconfigured requirements for a task, or null when all are met. */
  function unmetHint(req: string[]): string | null {
    const labels = req
      .filter((r) => !configured[r])
      .map((r) => REQUIREMENT_LABELS[r] ?? r);
    if (labels.length === 0) return null;
    const joined =
      labels.length === 1 ? labels[0] : `${labels.slice(0, -1).join(", ")} and ${labels[labels.length - 1]}`;
    const them = labels.length === 1 ? "one" : "them";
    return `Needs ${joined} — configure ${them} in Settings; this task is skipped until then.`;
  }

  return (
    <>
      <div className="rules">
        {tasks.map((t) => (
          <div className="rule" key={t.id}>
            <div className="rule-match">
              <label className={"chip-toggle" + (t.enabled ? " chip-toggle-good" : "")}>
                <input
                  type="checkbox"
                  checked={t.enabled}
                  disabled={busy === t.id}
                  onChange={() => toggle(t)}
                />
                {t.enabled ? "Enabled" : "Disabled"}
              </label>
              <strong>{t.name}</strong>
              {t.overridden && <span className="muted small">customised</span>}
              <span className="spacer" />
            </div>
            <p className="muted small">{t.description}</p>
            {(() => {
              const hint = unmetHint(t.requires);
              return hint ? <p className="muted small">{hint}</p> : null;
            })()}
          </div>
        ))}
      </div>
      {msg && <p className="error small">{msg}</p>}
    </>
  );
}

/** Settings section for per-repo provider/model and embedder overrides; shows a notice when nothing is configured globally. */
function GenerationSection({
  repoId,
  settings,
  reload,
}: {
  repoId: string;
  settings: RepoSettings;
  reload: () => void;
}) {
  const { global, providerOptions, embedderOptions, effective, overridden } = settings;
  const { providerCatalog, embedderCatalog } = settings;

  if (!global.providerConfigured && !global.embedderConfigured) {
    return (
      <section className="settings-section">
        <h2 className="section-title">Generation &amp; embeddings</h2>
        <p className="muted small">
          No LLM provider or embedder is configured globally. Configure one in
          global settings to enable per-repo overrides.
        </p>
      </section>
    );
  }

  return (
    <section className="settings-section">
      <h2 className="section-title">Generation &amp; embeddings</h2>
      <p className="muted small">
        Override the provider, model, or embedder for this repository. Leave a
        field on "Inherit global" to use the global configuration.
      </p>
      {global.providerConfigured && (
        <OverrideGroup
          repoId={repoId}
          reload={reload}
          label="Provider"
          selectKey="provider"
          selectOptions={providerOptions}
          catalog={providerCatalog}
          selectValue={overridden["provider"] ? effective.provider : ""}
          selectOverridden={!!overridden["provider"]}
          modelKey="model"
          modelValue={overridden["model"] ? effective.model : ""}
          modelPlaceholder={effective.model || "model"}
          modelOverridden={!!overridden["model"]}
        />
      )}
      {global.embedderConfigured && (
        <OverrideGroup
          repoId={repoId}
          reload={reload}
          label="Embedder"
          selectKey="embedder"
          selectOptions={embedderOptions}
          catalog={embedderCatalog}
          selectValue={overridden["embedder"] ? effective.embedder : ""}
          selectOverridden={!!overridden["embedder"]}
          modelKey="embedding_model"
          modelValue={overridden["embedding_model"] ? effective.embedding_model : ""}
          modelPlaceholder={effective.embedding_model || "embedding model"}
          modelOverridden={!!overridden["embedding_model"]}
        />
      )}
    </section>
  );
}

/** A provider/embedder override editor pairing a provider select with a model select or free-text input, with save and reset-to-global. */
function OverrideGroup({
  repoId,
  reload,
  label,
  selectKey,
  selectOptions,
  catalog,
  selectValue,
  selectOverridden,
  modelKey,
  modelValue,
  modelPlaceholder,
  modelOverridden,
}: {
  repoId: string;
  reload: () => void;
  label: string;
  selectKey: string;
  selectOptions: string[];
  catalog: ModelCatalog;
  selectValue: string;
  selectOverridden: boolean;
  modelKey: string;
  modelValue: string;
  modelPlaceholder: string;
  modelOverridden: boolean;
}) {
  const [sel, setSel] = useState(selectValue);
  const [model, setModel] = useState(modelValue);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const customised = selectOverridden || modelOverridden;
  const inherit = sel === "";
  const entry = catalog[sel];

  // Changing the provider repopulates the model: "Inherit global" clears the
  // model override; a specific provider resets to its catalog default (keeping
  // the current model if that provider already serves it). Providers without a
  // catalog entry fall back to free-text.
  function changeProvider(next: string) {
    setSel(next);
    if (next === "") {
      setModel("");
      return;
    }
    const nextEntry = catalog[next];
    if (!nextEntry) return; // free-text: leave whatever the user typed
    setModel(model && nextEntry.models.includes(model) ? model : nextEntry.default);
  }

  // Models selectable for the chosen provider, always including the current
  // value so a previously-saved custom model stays visible.
  const models = entry
    ? entry.models.includes(model) || !model
      ? entry.models
      : [model, ...entry.models]
    : [];

  /** Saves a non-empty setting value, or clears the override when the value is blank. */
  function persist(key: string, value: string): Promise<void> {
    return value.trim()
      ? putRepoSetting(repoId, key, value.trim())
      : clearRepoSetting(repoId, key);
  }

  /** Persists the selected provider/embedder and its model together. */
  function save() {
    setSaving(true);
    setMsg(null);
    Promise.all([persist(selectKey, sel), persist(modelKey, model)])
      .then(() => {
        setMsg("Saved. Re-index the repo for changes to take effect.");
        reload();
      })
      .catch((e: unknown) => setMsg(e instanceof Error ? e.message : String(e)))
      .finally(() => setSaving(false));
  }

  /** Clears both the provider/embedder and model overrides, reverting to global defaults. */
  function resetToGlobal() {
    setSaving(true);
    setMsg(null);
    Promise.all([clearRepoSetting(repoId, selectKey), clearRepoSetting(repoId, modelKey)])
      .then(() => {
        setSel("");
        setModel("");
        setMsg("Reset to global defaults.");
        reload();
      })
      .catch((e: unknown) => setMsg(e instanceof Error ? e.message : String(e)))
      .finally(() => setSaving(false));
  }

  return (
    <div className="rule">
      <div className="rule-match">
        <strong>{label}</strong>
        {customised && <span className="muted small">customised</span>}
        <span className="spacer" />
      </div>
      <div className="rule-match">
        <select value={sel} onChange={(e) => changeProvider(e.target.value)}>
          <option value="">Inherit global</option>
          {selectOptions.map((o) => (
            <option key={o} value={o}>
              {o}
            </option>
          ))}
        </select>
        {inherit ? (
          <input
            className="wiki-search"
            placeholder="Inherits global model"
            value=""
            disabled
            onChange={() => {}}
          />
        ) : entry ? (
          <select value={model} onChange={(e) => setModel(e.target.value)}>
            {models.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        ) : (
          <input
            className="wiki-search"
            placeholder={modelPlaceholder}
            value={model}
            onChange={(e) => setModel(e.target.value)}
          />
        )}
      </div>
      <div className="settings-actions">
        <span className="spacer" />
        {customised && (
          <button className="btn-ghost" onClick={resetToGlobal} disabled={saving}>
            Reset to global
          </button>
        )}
        <button className="btn" onClick={save} disabled={saving}>
          {saving ? "Saving…" : "Save"}
        </button>
      </div>
      {msg && <p className="muted small">{msg}</p>}
    </div>
  );
}

/** Non-destructive maintenance triggers: reindex, rescan biomarkers, and (credit-spending) wiki regeneration. */
function MaintenanceActions({ repoId }: { repoId: string }) {
  // Tracks which action is in flight (only one at a time) and the last result.
  const [busy, setBusy] = useState<null | "reindex" | "health" | "wiki">(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [armWiki, setArmWiki] = useState(false);

  /** Runs a trigger, showing a transient status message; never throws to the UI. */
  function run(kind: "reindex" | "health" | "wiki", fn: () => Promise<unknown>, started: string) {
    setBusy(kind);
    setMsg(null);
    fn()
      .then(() => setMsg(started))
      .catch((e: unknown) => setMsg(e instanceof Error ? e.message : String(e)))
      .finally(() => {
        setBusy(null);
        setArmWiki(false);
      });
  }

  return (
    <div className="settings-actions-col">
      <div className="maint-row">
        <div>
          <strong>Reindex</strong>
          <p className="muted small">
            Re-parse changed files and recompute the graph, health, and wiki in place.
            Non-destructive — nothing is wiped, and the wiki only regenerates pages whose
            source changed (no extra LLM cost).
          </p>
        </div>
        <button
          className="btn"
          disabled={busy !== null}
          onClick={() => run("reindex", () => reindexRepo(repoId), "Reindex started in the background.")}
        >
          {busy === "reindex" ? "Starting…" : "Reindex"}
        </button>
      </div>

      <div className="maint-row">
        <div>
          <strong>Rescan biomarkers</strong>
          <p className="muted small">
            Recompute every code-health finding from scratch — useful after changing
            thresholds or exclusion rules. No LLM cost.
          </p>
        </div>
        <button
          className="btn"
          disabled={busy !== null}
          onClick={() => run("health", () => rescanBiomarkers(repoId), "Biomarker rescan started in the background.")}
        >
          {busy === "health" ? "Starting…" : "Rescan biomarkers"}
        </button>
      </div>

      <div className="maint-row">
        <div>
          <strong>Regenerate wiki</strong>
          <p className="muted small">
            Re-write <em>every</em> wiki page with the LLM, ignoring freshness. This
            spends provider credits — normal indexing already keeps the wiki current, so
            only use this after a prompt/model change or to repair the wiki.
          </p>
        </div>
        {!armWiki ? (
          <button className="btn" disabled={busy !== null} onClick={() => setArmWiki(true)}>
            Regenerate wiki…
          </button>
        ) : (
          <span className="confirm">
            <button className="btn-ghost" disabled={busy !== null} onClick={() => setArmWiki(false)}>
              Cancel
            </button>
            <button
              className="btn"
              disabled={busy !== null}
              onClick={() =>
                run("wiki", () => regenerateWiki(repoId), "Full wiki regeneration started in the background.")
              }
            >
              {busy === "wiki" ? "Starting…" : "Confirm — spend credits"}
            </button>
          </span>
        )}
      </div>

      {msg && <p className="muted small">{msg}</p>}
    </div>
  );
}

/** Danger-zone control to delete the repo: a two-step armed confirmation that removes all indexed data. */
function DeleteRepo({ repoId, onDeleted }: { repoId: string; onDeleted: () => void }) {
  const [armed, setArmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  /** Calls the delete-repo API and invokes onDeleted on success. */
  function run() {
    setBusy(true);
    setError(null);
    deleteRepo(repoId)
      .then(onDeleted)
      .catch((e: unknown) => {
        setError(e instanceof Error ? e.message : String(e));
        setBusy(false);
      });
  }

  return (
    <div className="danger-box">
      <div>
        <strong>Delete this repository</strong>
        <p className="muted small">
          Stops watching it and removes all indexed data (graph, health, decisions,
          wiki). The files on disk are untouched. This can't be undone.
        </p>
      </div>
      {!armed ? (
        <button className="btn-danger" onClick={() => setArmed(true)}>
          Delete…
        </button>
      ) : (
        <span className="confirm">
          <button className="btn-ghost" onClick={() => setArmed(false)} disabled={busy}>
            Cancel
          </button>
          <button className="btn-danger" onClick={run} disabled={busy}>
            {busy ? "Deleting…" : "Confirm delete"}
          </button>
        </span>
      )}
      {error && <span className="error small">{error}</span>}
    </div>
  );
}
