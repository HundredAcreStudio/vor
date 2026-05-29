import { useMemo, useState } from "react";

// ModuleGraph renders a module×module import graph as an interactive SVG:
// nodes (modules) on a circle sized by connectivity, directed weighted edges
// for import counts. Hovering or focusing a node highlights its edges and
// neighbors. Dependency-free — module count is small (≤16).
export function ModuleGraph({
  modules,
  cells,
  focus,
  onSelect,
  size = 560,
}: {
  modules: string[];
  cells: number[][];
  focus?: string;
  onSelect?: (module: string) => void;
  size?: number;
}) {
  const [hover, setHover] = useState<number | null>(null);
  const n = modules.length;

  const layout = useMemo(() => {
    const cx = size / 2;
    const cy = size / 2;
    const r = size / 2 - 96;
    const degree = modules.map((_, i) =>
      cells[i].reduce((s, v, j) => s + (i === j ? 0 : v), 0) +
      cells.reduce((s, row, k) => s + (k === i ? 0 : row[i]), 0),
    );
    const maxDeg = Math.max(1, ...degree);
    const pos = modules.map((_, i) => {
      const a = -Math.PI / 2 + (2 * Math.PI * i) / n;
      return { x: cx + r * Math.cos(a), y: cy + r * Math.sin(a), a };
    });
    const nr = degree.map((d) => 7 + 9 * (d / maxDeg));
    return { pos, nr, degree };
  }, [modules, cells, n, size]);

  const maxW = Math.max(1, ...cells.flat());
  const short = (m: string) => m.split("/").slice(-1)[0] || m;
  const active = hover ?? (focus ? modules.indexOf(focus) : -1);

  // Edges to draw: i -> j with weight > 0, i != j.
  const edges: { i: number; j: number; w: number }[] = [];
  for (let i = 0; i < n; i++) {
    for (let j = 0; j < n; j++) {
      if (i !== j && cells[i][j] > 0) edges.push({ i, j, w: cells[i][j] });
    }
  }
  const touches = (e: { i: number; j: number }) => active < 0 || e.i === active || e.j === active;

  return (
    <svg width="100%" viewBox={`0 0 ${size} ${size}`} className="module-graph">
      <defs>
        <marker id="mg-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
          <path d="M0,0 L10,5 L0,10 z" fill="currentColor" />
        </marker>
      </defs>

      {edges.map((e, k) => {
        const a = layout.pos[e.i];
        const b = layout.pos[e.j];
        const on = touches(e);
        return (
          <line
            key={k}
            x1={a.x}
            y1={a.y}
            x2={b.x}
            y2={b.y}
            stroke={on ? "var(--accent)" : "var(--border)"}
            strokeOpacity={active < 0 ? 0.35 : on ? 0.8 : 0.06}
            strokeWidth={1 + 3 * (e.w / maxW)}
            markerEnd="url(#mg-arrow)"
            color={on ? "var(--accent)" : "var(--border)"}
          />
        );
      })}

      {modules.map((m, i) => {
        const p = layout.pos[i];
        const dim = active >= 0 && active !== i && !cells[active][i] && !cells[i][active];
        const isActive = i === active;
        const anchor = Math.cos(p.a) < -0.3 ? "end" : Math.cos(p.a) > 0.3 ? "start" : "middle";
        const lx = p.x + Math.cos(p.a) * (layout.nr[i] + 6);
        const ly = p.y + Math.sin(p.a) * (layout.nr[i] + 6);
        return (
          <g
            key={m}
            opacity={dim ? 0.25 : 1}
            onMouseEnter={() => setHover(i)}
            onMouseLeave={() => setHover(null)}
            onClick={() => onSelect?.(m)}
            style={{ cursor: onSelect ? "pointer" : "default" }}
          >
            <circle
              cx={p.x}
              cy={p.y}
              r={layout.nr[i]}
              fill={isActive ? "var(--accent)" : "var(--panel)"}
              stroke={isActive ? "var(--accent)" : "var(--muted)"}
              strokeWidth={1.5}
            />
            <text
              x={lx}
              y={ly}
              textAnchor={anchor}
              dominantBaseline="middle"
              className="mg-label"
              fill={isActive ? "var(--accent)" : "var(--muted)"}
            >
              {short(m)}
            </text>
          </g>
        );
      })}
    </svg>
  );
}
