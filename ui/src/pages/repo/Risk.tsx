import { useParams } from "react-router-dom";
import { fetchRisk, type RiskData } from "../../api.ts";
import { Donut } from "../../charts.tsx";
import { Icon } from "../../Icon.tsx";
import { AsyncView, useAsync } from "../../useAsync.tsx";

// RepoRisk surfaces where risk is concentrated across the repo: ownership
// silos, churn hotspots, dead code, stale decisions and security findings.
// TODO(follow-up): add the full Risk tab bar (Heatmap / Hotspots / Modules /
// Dead Code / Impact / Security), the Ownership treemap and the Contributor
// network force-graph — out of scope for this page.
export function RepoRisk() {
  const { repoId = "" } = useParams();
  const risk = useAsync(() => fetchRisk(repoId), [repoId]);

  return (
    <>
      <header className="page-header">
        <h1>
          <Icon name="shield" size={22} /> Risk
        </h1>
        <p className="muted page-sub">
          Where risk is concentrated — ownership silos, churn hotspots, dead code, and stale
          decisions.
        </p>
      </header>

      <AsyncView state={risk}>{(d) => <RiskBody data={d} />}</AsyncView>
    </>
  );
}

function RiskBody({ data }: { data: RiskData }) {
  const c = data.counts;
  const bf = data.busFactor;
  const contributors = [...data.topContributors].sort((a, b) => b.commits - a.commits);
  const maxCommits = Math.max(1, ...contributors.map((x) => x.commits));

  return (
    <>
      {/* stat cards */}
      <div className="stat-cards">
        <StatCard
          label="Hotspots"
          value={c.hotspots}
          sub="high-churn files"
          icon="local_fire_department"
        />
        <StatCard label="Silos" value={c.silos} sub="bus factor ≤ 1" icon="group" />
        <StatCard label="Dead code" value={c.deadCode} sub="findings" icon="delete" />
        <StatCard
          label="Stale decisions"
          value={c.staleDecisions}
          sub="need review"
          icon="lightbulb"
        />
        <StatCard label="Security" value={c.securityHigh} sub="high findings" icon="shield" />
      </div>

      <div className="risk-cols">
        {/* bus factor analysis */}
        <section className="panel">
          <header className="panel-head">
            <h3>Bus Factor Analysis</h3>
            <span className="muted small">{bf.total.toLocaleString()} files</span>
          </header>
          {bf.total === 0 ? (
            <p className="muted small">No ownership data.</p>
          ) : (
            <>
              <Donut
                slices={[
                  { label: `Safe (≥3) — ${bf.safe}`, value: bf.safe },
                  { label: `Warning (2) — ${bf.warning}`, value: bf.warning },
                  { label: `Risk (≤1) — ${bf.risk}`, value: bf.risk },
                ]}
                colors={["var(--good)", "var(--warn)", "var(--bad)"]}
              />
              {bf.riskFiles.length > 0 && (
                <>
                  <h4 className="risk-subhead">Highest risk files</h4>
                  <ul className="mini-list">
                    {bf.riskFiles.slice(0, 8).map((f) => (
                      <li key={f.path}>
                        <span className="mono path" title={f.path}>
                          {f.path}
                        </span>
                        <span className="muted">
                          {f.contributors} contributor{f.contributors === 1 ? "" : "s"}
                        </span>
                      </li>
                    ))}
                  </ul>
                </>
              )}
            </>
          )}
        </section>

        {/* top contributors */}
        <section className="panel">
          <header className="panel-head">
            <h3>Top Contributors</h3>
          </header>
          {contributors.length === 0 ? (
            <p className="muted small">No contributor data.</p>
          ) : (
            <ul className="contrib-list">
              {contributors.map((p) => (
                <li key={p.name} className="contrib-row">
                  <span className="contrib-name ellipsis" title={p.name}>
                    {p.name}
                  </span>
                  <span className="contrib-track">
                    <span
                      className="contrib-fill"
                      style={{ width: `${(p.commits / maxCommits) * 100}%` }}
                    />
                  </span>
                  <span className="contrib-count">{p.commits.toLocaleString()}</span>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
    </>
  );
}

function StatCard({
  label,
  value,
  sub,
  icon,
}: {
  label: string;
  value: number;
  sub: string;
  icon: string;
}) {
  return (
    <div className="stat-card">
      <div className="stat-card-top">
        <span className="stat-card-label">{label}</span>
        <span className="stat-card-icon">
          <Icon name={icon} size={18} />
        </span>
      </div>
      <span className="stat-card-value">{value.toLocaleString()}</span>
      <span className="stat-card-sub muted">{sub}</span>
    </div>
  );
}
