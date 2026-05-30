import { useMemo } from "react";
import type { ContribEdge, ContribNode, OwnershipCell } from "./api.ts";

// Dependency-free SVG visualizations for the Risk dashboard:
//   - Treemap: a slice-and-dice ownership map colored by risk.
//   - ContributorNetwork: a circular co-authorship graph.

// riskColor buckets a 0..1 risk score into five shades spanning the theme's
// good → warn → bad tiers. Bucketing keeps the palette legible without a real
// color-space interpolation library.
function riskColor(risk: number): string {
  const r = Math.max(0, Math.min(1, risk));
  if (r < 0.2) return "#2f7d3a"; // deep green (low risk)
  if (r < 0.4) return "#3fb950"; // --good
  if (r < 0.6) return "#d29922"; // --warn
  if (r < 0.8) return "#e8683f"; // amber→red
  return "#f85149"; // --bad (high risk)
}

type Rect = { x: number; y: number; w: number; h: number; cell: OwnershipCell };

// sliceAndDice recursively splits a rectangle proportional to each cell's
// value, alternating split axis with depth. Cells must be pre-sorted desc.
function sliceAndDice(
  cells: OwnershipCell[],
  x: number,
  y: number,
  w: number,
  h: number,
  horizontal: boolean,
  out: Rect[],
): void {
  if (cells.length === 0 || w <= 0 || h <= 0) return;
  if (cells.length === 1) {
    out.push({ x, y, w, h, cell: cells[0] });
    return;
  }
  const total = cells.reduce((s, c) => s + Math.max(0, c.value), 0) || 1;
  // Split the list into two groups of ~equal value so the layout stays
  // balanced rather than degenerating into thin strips.
  let acc = 0;
  let split = 1;
  for (let i = 0; i < cells.length; i++) {
    acc += Math.max(0, cells[i].value);
    if (acc >= total / 2) {
      split = Math.max(1, Math.min(cells.length - 1, i + 1));
      break;
    }
  }
  const first = cells.slice(0, split);
  const second = cells.slice(split);
  const firstVal = first.reduce((s, c) => s + Math.max(0, c.value), 0);
  const frac = firstVal / total;

  if (horizontal) {
    const w1 = w * frac;
    sliceAndDice(first, x, y, w1, h, !horizontal, out);
    sliceAndDice(second, x + w1, y, w - w1, h, !horizontal, out);
  } else {
    const h1 = h * frac;
    sliceAndDice(first, x, y, w, h1, !horizontal, out);
    sliceAndDice(second, x, y + h1, w, h - h1, !horizontal, out);
  }
}

const TREEMAP_W = 1000;
const TREEMAP_H = 420;

// Treemap renders ownership cells as area-proportional rectangles colored by
// risk. Uses a viewBox so the SVG scales to 100% width while keeping its
// internal layout coordinates stable.
export function Treemap({
  cells,
  onSelect,
}: {
  cells: OwnershipCell[];
  onSelect?: (name: string) => void;
}) {
  const rects = useMemo(() => {
    const sorted = [...cells]
      .filter((c) => c.value > 0)
      .sort((a, b) => b.value - a.value);
    const out: Rect[] = [];
    sliceAndDice(sorted, 0, 0, TREEMAP_W, TREEMAP_H, true, out);
    return out;
  }, [cells]);

  if (rects.length === 0) {
    return <p className="muted small">No ownership data.</p>;
  }

  return (
    <div className="treemap-wrap">
      <svg
        width="100%"
        viewBox={`0 0 ${TREEMAP_W} ${TREEMAP_H}`}
        preserveAspectRatio="none"
        className="treemap"
      >
        {rects.map((r) => {
          const label = r.cell.name.split("/").slice(-1)[0] || r.cell.name;
          const showLabel = r.w > 60 && r.h > 26;
          const showSub = r.w > 80 && r.h > 44;
          return (
            <g
              key={r.cell.name}
              onClick={() => onSelect?.(r.cell.name)}
              style={{ cursor: onSelect ? "pointer" : "default" }}
            >
              <title>
                {`${r.cell.name} — ${r.cell.files} files, ${r.cell.hotspots} hotspots, owner ${r.cell.owner || "—"}`}
              </title>
              <rect
                x={r.x + 1}
                y={r.y + 1}
                width={Math.max(0, r.w - 2)}
                height={Math.max(0, r.h - 2)}
                fill={riskColor(r.cell.risk)}
                stroke="var(--bg)"
                strokeWidth={1.5}
                rx={2}
              />
              {showLabel && (
                <text
                  x={r.x + 8}
                  y={r.y + 18}
                  className="treemap-label"
                  fill="#0e1116"
                >
                  {label}
                </text>
              )}
              {showSub && (
                <text
                  x={r.x + 8}
                  y={r.y + 33}
                  className="treemap-sub"
                  fill="#0e1116"
                >
                  {r.cell.files} files
                </text>
              )}
            </g>
          );
        })}
      </svg>
      <div className="treemap-legend">
        <span className="muted small">Risk</span>
        <span className="treemap-legend-bar">
          {[0.1, 0.3, 0.5, 0.7, 0.9].map((r) => (
            <span key={r} style={{ background: riskColor(r) }} />
          ))}
        </span>
        <span className="muted small">low → high</span>
      </div>
    </div>
  );
}

const NET_SIZE = 600;

// ContributorNetwork places contributors on a circle (radius ∝ files) and
// draws co-authorship edges (width ∝ shared files). Static circular layout —
// no physics. Mirrors ModuleGraph's approach.
export function ContributorNetwork({
  nodes,
  edges,
}: {
  nodes: ContribNode[];
  edges: ContribEdge[];
}) {
  const layout = useMemo(() => {
    const cx = NET_SIZE / 2;
    const cy = NET_SIZE / 2;
    const r = NET_SIZE / 2 - 110;
    const n = nodes.length;
    const maxFiles = Math.max(1, ...nodes.map((x) => x.files));
    const index = new Map<string, number>();
    nodes.forEach((node, i) => index.set(node.name, i));
    const pos = nodes.map((_, i) => {
      const a = -Math.PI / 2 + (2 * Math.PI * i) / Math.max(1, n);
      return { x: cx + r * Math.cos(a), y: cy + r * Math.sin(a), a };
    });
    const nr = nodes.map((node) => 6 + 14 * Math.sqrt(node.files / maxFiles));
    return { pos, nr, index };
  }, [nodes]);

  if (nodes.length < 2) {
    return <p className="muted small">Not enough contributor data.</p>;
  }

  const maxW = Math.max(1, ...edges.map((e) => e.weight));
  const short = (name: string) => (name.length > 18 ? `${name.slice(0, 17)}…` : name);

  return (
    <svg width="100%" viewBox={`0 0 ${NET_SIZE} ${NET_SIZE}`} className="contrib-graph">
      {edges.map((e, k) => {
        const i = layout.index.get(e.source);
        const j = layout.index.get(e.target);
        if (i === undefined || j === undefined) return null;
        const a = layout.pos[i];
        const b = layout.pos[j];
        return (
          <line
            key={k}
            x1={a.x}
            y1={a.y}
            x2={b.x}
            y2={b.y}
            stroke="var(--accent)"
            strokeOpacity={0.12 + 0.28 * (e.weight / maxW)}
            strokeWidth={1 + 4 * (e.weight / maxW)}
          />
        );
      })}

      {nodes.map((node, i) => {
        const p = layout.pos[i];
        const anchor = Math.cos(p.a) < -0.3 ? "end" : Math.cos(p.a) > 0.3 ? "start" : "middle";
        const lx = p.x + Math.cos(p.a) * (layout.nr[i] + 8);
        const ly = p.y + Math.sin(p.a) * (layout.nr[i] + 8);
        return (
          <g key={node.name}>
            <title>{`${node.name} — ${node.files} files, ${node.commits} commits`}</title>
            <circle
              cx={p.x}
              cy={p.y}
              r={layout.nr[i]}
              fill="var(--panel)"
              stroke="var(--accent)"
              strokeWidth={1.5}
            />
            <text
              x={lx}
              y={ly}
              textAnchor={anchor}
              dominantBaseline="middle"
              className="contrib-graph-label"
              fill="var(--muted)"
            >
              {short(node.name)}
            </text>
          </g>
        );
      })}
    </svg>
  );
}
