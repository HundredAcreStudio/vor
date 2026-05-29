import { useParams } from "react-router-dom";
import { fetchHealthSummary, fetchOverview, fetchRepo, type RepoSummary } from "../../api.ts";
import { AsyncView, useAsync } from "../../useAsync.tsx";

export function RepoOverview() {
  const { repoId = "" } = useParams();
  const repo = useAsync(() => fetchRepo(repoId), [repoId]);
  const health = useAsync(() => fetchHealthSummary(repoId), [repoId]);
  const summary = useAsync(
    () => fetchOverview().then((o) => o.repos.find((r) => r.id === repoId)),
    [repoId],
  );

  return (
    <>
      <header className="page-header">
        <h1>Overview</h1>
      </header>

      <AsyncView state={health}>
        {(h) => {
          const tier = h.averageScore >= 7.5 ? "good" : h.averageScore >= 5 ? "warn" : "bad";
          return (
            <div className="kpi-row">
              <Kpi value={h.averageScore.toFixed(1)} label="health (1–10)" tier={tier} />
              <Kpi value={h.findingCount.toLocaleString()} label="findings" />
              <AsyncView state={summary}>
                {(s?: RepoSummary) => (
                  <>
                    <Kpi value={(s?.fileCount ?? 0).toLocaleString()} label="files" />
                    <Kpi value={(s?.symbolCount ?? 0).toLocaleString()} label="symbols" />
                  </>
                )}
              </AsyncView>
            </div>
          );
        }}
      </AsyncView>

      <AsyncView state={repo}>
        {(r) => (
          <dl className="facts">
            <Fact label="Local path" value={r.localPath} mono />
            <Fact label="Default branch" value={r.defaultBranch} mono />
            <Fact label="Head commit" value={r.headCommit ? r.headCommit.slice(0, 12) : "—"} mono />
            <Fact label="Last indexed" value={new Date(r.updatedAt).toLocaleString()} />
          </dl>
        )}
      </AsyncView>
    </>
  );
}

function Kpi({ value, label, tier }: { value: string; label: string; tier?: string }) {
  return (
    <div className="kpi">
      <span className={`kpi-value${tier ? ` health-${tier}` : ""}`}>{value}</span>
      <span className="kpi-label">{label}</span>
    </div>
  );
}

function Fact({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="fact">
      <dt>{label}</dt>
      <dd className={mono ? "mono" : undefined}>{value}</dd>
    </div>
  );
}
