import type { JobState } from '../api.ts';
import { stateLabel } from '../format.ts';

const icons: Record<JobState, string> = { queued: '◷', running: '▶', succeeded: '✓', cancelled: '✕' };

/** A job state shown with text and an icon, never by color alone. */
export function StateBadge({ state }: { state: JobState }) {
  return (
    <span className={`badge badge-${state}`}>
      <span aria-hidden="true">{icons[state]}</span> {stateLabel[state]}
    </span>
  );
}
