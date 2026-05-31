import { fetchBiomarkers, type Biomarker } from "../api.ts";
import { AsyncView, useAsync } from "../useAsync.tsx";

// severityClass maps a severity level to a badge color. critical/high are bad,
// medium warns, everything else (low/info) is muted.
function severityClass(sev: string): string {
  const s = sev.toLowerCase();
  if (s === "critical" || s === "high") return "metric-sev metric-sev-bad";
  if (s === "medium") return "metric-sev metric-sev-warn";
  return "metric-sev metric-sev-muted";
}

/** Metrics renders the Health Metrics page: a card per biomarker the analyzer
 * detects, documenting what each one means, how it's measured, and its
 * triggering thresholds. */
export function Metrics() {
  const state = useAsync(() => fetchBiomarkers(), []);

  return (
    <main className="content">
      <header className="page-header">
        <h1>Health Metrics</h1>
      </header>

      <p className="muted" style={{ maxWidth: 760, marginTop: 0 }}>
        Every file starts at a health score of 10. Each finding the analyzer
        reports subtracts a weighted impact from that score, and the result is
        clamped to the 1–10 range. The biomarkers below are the code smells the
        analyzer detects — this page documents what each one means, how it's
        measured, and the thresholds that trigger it.
      </p>

      <AsyncView state={state}>
        {(biomarkers) =>
          biomarkers.length === 0 ? (
            <p className="muted">No biomarkers available.</p>
          ) : (
            <div className="metric-cards">
              {biomarkers.map((b) => (
                <MetricCard key={b.id} bm={b} />
              ))}
            </div>
          )
        }
      </AsyncView>
    </main>
  );
}

/** MetricCard renders a single biomarker's name, scope, severity badges,
 * summary, and (when present) its calculation method and thresholds. */
function MetricCard({ bm }: { bm: Biomarker }) {
  return (
    <section className="settings-section metric-card">
      <div className="metric-card-head">
        <h2 className="metric-card-name">{bm.name}</h2>
        <span className="metric-scope">{bm.scope}</span>
        {bm.severities.map((s) => (
          <span key={s} className={severityClass(s)}>
            {s}
          </span>
        ))}
      </div>
      <p className="metric-summary">{bm.summary}</p>
      {bm.how && (
        <p className="muted small metric-detail">
          <span className="metric-detail-label">How it's calculated</span> {bm.how}
        </p>
      )}
      {bm.thresholds && (
        <p className="muted small metric-detail">
          <span className="metric-detail-label">Thresholds</span>{" "}
          <span className="mono">{bm.thresholds}</span>
        </p>
      )}
    </section>
  );
}
