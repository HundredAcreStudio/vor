import { Fragment } from "react";

// Tiny dependency-free SVG charts for the dashboard.

// HealthGauge draws a circular ring filled to score/max (default 1–10), with
// the score in the centre. Color tracks the same good/warn/bad tiers used
// across the app.
export function HealthGauge({ score, max = 10, size = 120 }: { score: number; max?: number; size?: number }) {
  const r = size / 2 - 10;
  const c = 2 * Math.PI * r;
  const frac = Math.max(0, Math.min(1, score / max));
  const color = score >= 7.5 * (max / 10) ? "var(--good)" : score >= 5 * (max / 10) ? "var(--warn)" : "var(--bad)";
  return (
    <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} className="gauge">
      <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--border)" strokeWidth={8} />
      <circle
        cx={size / 2}
        cy={size / 2}
        r={r}
        fill="none"
        stroke={color}
        strokeWidth={8}
        strokeLinecap="round"
        strokeDasharray={`${c * frac} ${c}`}
        transform={`rotate(-90 ${size / 2} ${size / 2})`}
      />
      <text x="50%" y="48%" textAnchor="middle" dominantBaseline="middle" className="gauge-value" fill={color}>
        {Number.isFinite(score) ? score.toFixed(1) : "—"}
      </text>
      <text x="50%" y="64%" textAnchor="middle" dominantBaseline="middle" className="gauge-sub">
        / {max}
      </text>
    </svg>
  );
}

const PALETTE = [
  "#58a6ff",
  "#3fb950",
  "#d29922",
  "#f85149",
  "#bc8cff",
  "#39c5cf",
  "#ff7b72",
  "#a5d6ff",
  "#7ee787",
  "#ffa657",
];

// Heatmap renders a module×module density matrix. Cell intensity scales with
// count (brighter = more import edges between the two modules).
export function Heatmap({ modules, cells }: { modules: string[]; cells: number[][] }) {
  const max = Math.max(1, ...cells.flat());
  const short = (m: string) => {
    const parts = m.split("/");
    return parts[parts.length - 1] || m;
  };
  return (
    <div className="heatmap" style={{ ["--n" as string]: modules.length }}>
      <div className="heatmap-corner" />
      {modules.map((m) => (
        <div className="heatmap-coltick" key={`c-${m}`} title={m}>
          <span>{short(m)}</span>
        </div>
      ))}
      {modules.map((row, i) => (
        <Fragment key={`r-${row}`}>
          <div className="heatmap-rowtick" title={row}>
            {short(row)}
          </div>
          {modules.map((_, j) => {
            const v = cells[i]?.[j] ?? 0;
            const alpha = v === 0 ? 0 : 0.15 + 0.85 * (v / max);
            return (
              <div
                key={j}
                className="heatmap-cell"
                title={`${row} → ${modules[j]}: ${v}`}
                style={{ background: v === 0 ? "var(--bg)" : `rgba(210, 153, 34, ${alpha})` }}
              />
            );
          })}
        </Fragment>
      ))}
    </div>
  );
}

// TrendChart draws one or more line series over a shared 0..max y-axis.
// Width fills its container (viewBox + preserveAspectRatio="none"); height is
// fixed. Handles 0 points (renders nothing) and 1 point (renders a dot).
export function TrendChart({
  series,
  max = 10,
  height = 220,
  xLabels,
}: {
  series: { label: string; color: string; values: number[] }[];
  max?: number;
  height?: number;
  xLabels?: string[];
}) {
  const n = Math.max(0, ...series.map((s) => s.values.length));
  if (n === 0) return null;

  // viewBox coordinate space; SVG scales to 100% width via preserveAspectRatio.
  const W = 600;
  const H = height;
  const padL = 28;
  const padR = 12;
  const padT = 12;
  const padB = xLabels && xLabels.length ? 24 : 12;
  const plotW = W - padL - padR;
  const plotH = H - padT - padB;

  const x = (i: number) => (n <= 1 ? padL + plotW / 2 : padL + (i / (n - 1)) * plotW);
  const y = (v: number) => {
    const frac = Math.max(0, Math.min(1, v / max));
    return padT + (1 - frac) * plotH;
  };

  const gridLevels = [0, max / 2, max];

  return (
    <div className="trend">
      <svg
        className="trend-svg"
        viewBox={`0 0 ${W} ${H}`}
        preserveAspectRatio="none"
        style={{ width: "100%", height }}
        role="img"
      >
        {gridLevels.map((lvl) => (
          <g key={lvl}>
            <line
              x1={padL}
              x2={W - padR}
              y1={y(lvl)}
              y2={y(lvl)}
              stroke="var(--border)"
              strokeWidth={1}
            />
            <text x={4} y={y(lvl) + 3} className="trend-axis" fill="var(--muted)">
              {lvl}
            </text>
          </g>
        ))}
        {series.map((s) => {
          const pts = s.values.map((v, i) => `${x(i)},${y(v)}`).join(" ");
          return (
            <g key={s.label}>
              {s.values.length > 1 && (
                <polyline
                  points={pts}
                  fill="none"
                  stroke={s.color}
                  strokeWidth={2}
                  vectorEffect="non-scaling-stroke"
                  strokeLinejoin="round"
                  strokeLinecap="round"
                />
              )}
              {s.values.map((v, i) => (
                <circle key={i} cx={x(i)} cy={y(v)} r={3} fill={s.color} />
              ))}
            </g>
          );
        })}
        {xLabels?.map((lbl, i) =>
          // Only label the endpoints to avoid crowding.
          i === 0 || i === xLabels.length - 1 ? (
            <text
              key={i}
              x={x(i)}
              y={H - 6}
              textAnchor={i === 0 ? "start" : "end"}
              className="trend-axis"
              fill="var(--muted)"
            >
              {lbl}
            </text>
          ) : null,
        )}
      </svg>
      <ul className="trend-legend">
        {series.map((s) => (
          <li key={s.label}>
            <span className="swatch" style={{ background: s.color }} />
            <span>{s.label}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

export type DonutSlice = { label: string; value: number };

// Donut draws a ring chart with a legend. Slices are expected pre-sorted.
// `colors` overrides the default palette per slice (by index).
export function Donut({
  slices,
  size = 130,
  colors,
}: {
  slices: DonutSlice[];
  size?: number;
  colors?: string[];
}) {
  const total = slices.reduce((s, x) => s + x.value, 0) || 1;
  const r = size / 2 - 6;
  const c = 2 * Math.PI * r;
  const colorAt = (i: number) => colors?.[i] ?? PALETTE[i % PALETTE.length];
  let offset = 0;
  return (
    <div className="donut">
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
        <g transform={`rotate(-90 ${size / 2} ${size / 2})`}>
          {slices.map((s, i) => {
            const frac = s.value / total;
            const seg = (
              <circle
                key={s.label}
                cx={size / 2}
                cy={size / 2}
                r={r}
                fill="none"
                stroke={colorAt(i)}
                strokeWidth={12}
                strokeDasharray={`${c * frac} ${c}`}
                strokeDashoffset={-c * offset}
              />
            );
            offset += frac;
            return seg;
          })}
        </g>
      </svg>
      <ul className="donut-legend">
        {slices.map((s, i) => (
          <li key={s.label}>
            <span className="swatch" style={{ background: colorAt(i) }} />
            <span className="ellipsis">{s.label}</span>
            <span className="muted">{Math.round((s.value / total) * 100)}%</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
