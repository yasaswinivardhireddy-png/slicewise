import { useMemo, useRef, useState, type CSSProperties, type KeyboardEvent } from 'react';
import { Link } from 'react-router';
import type { Job, Pod } from '../api.ts';
import { clock, priorityLabel } from '../format.ts';

interface Props {
  pod: Pod;
  onOpen: (jobId: string) => void;
}

/** "j-17" -> "17": the number printed on a job's chips. */
export const jobNumber = (id: string) => id.replace(/^j-/, '');

/** Color class for a job. Color is only a cue: chips also show the job number and the label says it all. */
export const colorClass = (id: string) => `c${Number(jobNumber(id)) % 8}`;

/**
 * The pod's chips as one ARIA grid per layer (z). A single chip is in the tab order (a roving tab
 * stop): arrow keys move within a layer, Page Up and Page Down move between layers, Home and End go
 * to the ends of a row (with Ctrl, of the layer), and Enter or Space opens the chip's job.
 */
export function PodGrid({ pod, onOpen }: Props) {
  const { x: X, y: Y, z: Z } = pod.dims;
  const jobs = useMemo(() => new Map(pod.running.map((j) => [j.id, j])), [pod.running]);
  const [active, setActive] = useState(0);
  const cells = useRef<(HTMLDivElement | null)[]>([]);
  const index = (x: number, y: number, z: number) => (z * Y + y) * X + x;
  const coords = (i: number) => ({ x: i % X, y: Math.floor(i / X) % Y, z: Math.floor(i / (X * Y)) });
  const jobAt = (i: number): Job | undefined => jobs.get(pod.cells[i] ?? '');

  const label = (i: number) => {
    const { x, y, z } = coords(i);
    const job = jobAt(i);
    const where = `x ${x}, y ${y}, z ${z}`;
    if (!job) return `${where}: idle`;
    return `${where}: job ${jobNumber(job.id)}, ${job.name}, ${job.tenant}, ${priorityLabel[job.priority].toLowerCase()} priority`;
  };

  const onKeyDown = (e: KeyboardEvent, i: number) => {
    const { x, y, z } = coords(i);
    let to: number | undefined;
    switch (e.key) {
      case 'ArrowRight':
        to = x < X - 1 ? i + 1 : i;
        break;
      case 'ArrowLeft':
        to = x > 0 ? i - 1 : i;
        break;
      case 'ArrowDown':
        to = y < Y - 1 ? i + X : i;
        break;
      case 'ArrowUp':
        to = y > 0 ? i - X : i;
        break;
      case 'Home':
        to = e.ctrlKey ? index(0, 0, z) : index(0, y, z);
        break;
      case 'End':
        to = e.ctrlKey ? index(X - 1, Y - 1, z) : index(X - 1, y, z);
        break;
      case 'PageDown':
        to = z < Z - 1 ? index(x, y, z + 1) : i;
        break;
      case 'PageUp':
        to = z > 0 ? index(x, y, z - 1) : i;
        break;
      case 'Enter':
      case ' ': {
        const id = pod.cells[i];
        if (id) onOpen(id);
        break;
      }
      default:
        return;
    }
    e.preventDefault();
    if (to !== undefined && to !== i) {
      setActive(to);
      cells.current[to]?.focus();
    }
  };

  const focused = jobAt(active);
  const where = coords(active);
  return (
    <div className="pod">
      <p id="pod-help" className="hint">
        Use the arrow keys to move between chips, Page Up and Page Down to change layers, and Enter to open the job on a
        chip.
      </p>
      <div className="layers">
        {Array.from({ length: Z }, (_, z) => (
          <section key={z} className="layer" aria-labelledby={`layer-${z}`}>
            <h3 id={`layer-${z}`}>Layer z = {z}</h3>
            <div
              role="grid"
              className="grid"
              aria-labelledby={`layer-${z}`}
              aria-describedby="pod-help"
              style={{ '--cols': X } as CSSProperties}
            >
              {Array.from({ length: Y }, (_, y) => (
                <div role="row" key={y} className="grid-row">
                  {Array.from({ length: X }, (_, x) => {
                    const i = index(x, y, z);
                    const job = jobAt(i);
                    return (
                      <div
                        key={x}
                        role="gridcell"
                        ref={(el) => {
                          cells.current[i] = el;
                        }}
                        tabIndex={i === active ? 0 : -1}
                        aria-label={label(i)}
                        className={job ? `cell busy ${colorClass(job.id)}` : 'cell idle'}
                        onKeyDown={(e) => onKeyDown(e, i)}
                        onFocus={() => setActive(i)}
                        onClick={() => job && onOpen(job.id)}
                      >
                        {job ? jobNumber(job.id) : ''}
                      </div>
                    );
                  })}
                </div>
              ))}
            </div>
          </section>
        ))}
      </div>
      <div className="chip-detail">
        <h3>Selected chip</h3>
        <p>
          Chip at x {where.x}, y {where.y}, z {where.z}:{' '}
          {focused ? (
            <>
              job {jobNumber(focused.id)}, <Link to={`/jobs/${focused.id}`}>{focused.name}</Link> ({focused.tenant},{' '}
              {priorityLabel[focused.priority].toLowerCase()} priority, {focused.chips} chips
              {focused.endsAt !== null ? `, ends ${clock(focused.endsAt)}` : ''})
            </>
          ) : (
            'idle'
          )}
        </p>
      </div>
    </div>
  );
}
