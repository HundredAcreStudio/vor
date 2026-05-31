import { useParams } from "react-router-dom";
import { fetchDecisions } from "../../api.ts";
import { AsyncView, useAsync } from "../../useAsync.tsx";

/** RepoDecisions renders the repo's detected architectural decisions, each with
 * its rationale, source, evidence location, and confidence. */
export function RepoDecisions() {
  const { repoId = "" } = useParams();
  const decisions = useAsync(() => fetchDecisions(repoId), [repoId]);

  return (
    <>
      <header className="page-header">
        <h1>Decisions</h1>
      </header>
      <AsyncView state={decisions}>
        {(rows) =>
          rows.length === 0 ? (
            <p className="muted">No architectural decisions detected.</p>
          ) : (
            <ul className="decision-list">
              {rows.map((d, i) => (
                <li className="decision" key={i}>
                  <div className="decision-head">
                    <span className="decision-title">{d.decision || d.title}</span>
                    <span className="badge">{d.source.replace(/_/g, " ")}</span>
                  </div>
                  {d.rationale && <p className="decision-why">{d.rationale}</p>}
                  <div className="decision-foot muted">
                    {d.evidenceFile && (
                      <span className="mono">
                        {d.evidenceFile}
                        {d.evidenceLine ? `:${d.evidenceLine}` : ""}
                      </span>
                    )}
                    <span>confidence {(d.confidence * 100).toFixed(0)}%</span>
                  </div>
                </li>
              ))}
            </ul>
          )
        }
      </AsyncView>
    </>
  );
}
