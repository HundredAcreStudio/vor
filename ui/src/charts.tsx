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

export type DonutSlice = { label: string; value: number };

// Donut draws a ring chart with a legend. Slices are expected pre-sorted.
export function Donut({ slices, size = 130 }: { slices: DonutSlice[]; size?: number }) {
  const total = slices.reduce((s, x) => s + x.value, 0) || 1;
  const r = size / 2 - 6;
  const c = 2 * Math.PI * r;
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
                stroke={PALETTE[i % PALETTE.length]}
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
            <span className="swatch" style={{ background: PALETTE[i % PALETTE.length] }} />
            <span className="ellipsis">{s.label}</span>
            <span className="muted">{Math.round((s.value / total) * 100)}%</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
