import { useId } from 'react';

interface Bar {
  label: string;
  value: number;
}

interface Props {
  title: string;
  /** One sentence that states the chart's main point; read by screen readers with the title. */
  summary: string;
  bars: Bar[];
  format: (value: number) => string;
}

const LABEL_WIDTH = 230;
const BAR_WIDTH = 260;
const ROW = 30;

/**
 * A horizontal bar chart. Every bar is labeled with its value in text, so no information depends on
 * color, and screen readers get the title and a summary; the table next to it has the full data.
 */
export function BarChart({ title, summary, bars, format }: Props) {
  const id = useId();
  const max = Math.max(...bars.map((b) => b.value), Number.EPSILON);
  const height = bars.length * ROW + 8;
  return (
    <figure className="chart">
      <svg
        role="img"
        aria-labelledby={`${id}-title ${id}-summary`}
        viewBox={`0 0 ${LABEL_WIDTH + BAR_WIDTH + 90} ${height}`}
        preserveAspectRatio="xMinYMin meet"
      >
        <title id={`${id}-title`}>{title}</title>
        <desc id={`${id}-summary`}>{summary}</desc>
        {bars.map((b, i) => {
          const width = Math.max((b.value / max) * BAR_WIDTH, 2);
          const y = i * ROW + 4;
          return (
            <g key={b.label}>
              <text x={LABEL_WIDTH - 8} y={y + 17} textAnchor="end" className="chart-label">
                {b.label}
              </text>
              <rect x={LABEL_WIDTH} y={y} width={width} height={ROW - 8} rx="3" className="chart-bar" />
              <text x={LABEL_WIDTH + width + 6} y={y + 17} className="chart-value">
                {format(b.value)}
              </text>
            </g>
          );
        })}
      </svg>
      <figcaption>{title}</figcaption>
    </figure>
  );
}
