import { useEffect, useState, type ReactNode } from "react";
import { fetchBiomarkers, type Biomarker } from "./api.ts";
import { Icon } from "./Icon.tsx";

// useBiomarker returns the catalog entry for a biomarker id, or null while the
// (module-cached) catalog is loading or if the id is unknown. Every instance
// shares the single cached fetchBiomarkers() promise, so no per-tooltip fetch.
function useBiomarker(id: string): Biomarker | null {
  const [bm, setBm] = useState<Biomarker | null>(null);
  useEffect(() => {
    let cancelled = false;
    fetchBiomarkers()
      .then((list) => {
        if (!cancelled) setBm(list.find((b) => b.id === id) ?? null);
      })
      .catch(() => {
        if (!cancelled) setBm(null);
      });
    return () => {
      cancelled = true;
    };
  }, [id]);
  return bm;
}

// MetricTip wraps children in a hover target. When the biomarker is known it
// shows a CSS-driven popover (name, summary, thresholds); while loading or for
// an unknown id it just renders children with no popover.
export function MetricTip({ id, children }: { id: string; children: ReactNode }) {
  const bm = useBiomarker(id);
  if (!bm) return <>{children}</>;
  return (
    <span className="metric-tip" tabIndex={0}>
      {children}
      <span className="metric-tip-pop" role="tooltip">
        <span className="metric-tip-name">{bm.name}</span>
        <span className="metric-tip-summary">{bm.summary}</span>
        {bm.thresholds && (
          <span className="metric-tip-thresholds">
            <span className="metric-tip-th-label">Thresholds</span> {bm.thresholds}
          </span>
        )}
      </span>
    </span>
  );
}

// MetricLabel renders a biomarker's human name (falling back to a prettified
// id) plus a tiny info affordance, all wrapped in a MetricTip.
export function MetricLabel({ id }: { id: string }) {
  const bm = useBiomarker(id);
  const label = bm?.name ?? id.replace(/_/g, " ");
  return (
    <MetricTip id={id}>
      <span className="metric-label">
        {label}
        <Icon name="info" size={13} />
      </span>
    </MetricTip>
  );
}
