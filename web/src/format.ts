import type { Dims, JobState, Placement, Priority } from './api.ts';

const pad = (n: number) => String(n).padStart(2, '0');

/** Formats simulated seconds as a cluster clock, for example "Day 2, 14:05". */
export function clock(seconds: number): string {
  const day = Math.floor(seconds / 86_400) + 1;
  return `Day ${day}, ${pad(Math.floor((seconds % 86_400) / 3600))}:${pad(Math.floor((seconds % 3600) / 60))}`;
}

/** Formats a length of time in seconds, for example "1 h 05 min" or "45 min". */
export function duration(seconds: number): string {
  if (seconds < 60) return 'under 1 min';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (h >= 48) return `${(seconds / 86_400).toFixed(1)} days`;
  if (h === 0) return `${m} min`;
  return m === 0 ? `${h} h` : `${h} h ${pad(m)} min`;
}

export const hours = (h: number) => (h < 0.05 ? 'under 0.1 h' : `${h.toFixed(1)} h`);

export const percent = (fraction: number) => `${(fraction * 100).toFixed(1)}%`;

export const count = (n: number) => n.toLocaleString('en-US');

export const priorityLabel: Record<Priority, string> = {
  production: 'Production',
  batch: 'Batch',
  'best-effort': 'Best effort',
};

export const stateLabel: Record<JobState, string> = {
  queued: 'Queued',
  running: 'Running',
  succeeded: 'Succeeded',
  cancelled: 'Cancelled',
};

export const shapeText = (d: Dims) => `${d.x}x${d.y}x${d.z}`;

export const placementText = (p: Placement) =>
  `${shapeText(p.size)} at x ${p.origin.x}, y ${p.origin.y}, z ${p.origin.z}`;

export const plural = (n: number, one: string, many = `${one}s`) => `${count(n)} ${n === 1 ? one : many}`;

/** Slice shapes offered in the submit form, smallest first. */
export const SHAPES = ['2x2x1', '2x2x2', '2x2x4', '2x4x4', '4x4x4', '4x4x8', '4x8x8', '8x8x8'];

/** Whether some rotation of a shape such as "2x4x4" fits inside the pod. */
export function shapeFits(shape: string, pod: Dims): boolean {
  const s = shape.split('x').map(Number);
  if (s.length !== 3 || s.some((n) => !Number.isInteger(n) || n < 1)) return false;
  const [a = 0, b = 0, c = 0] = s;
  const rotations = [
    [a, b, c],
    [a, c, b],
    [b, a, c],
    [b, c, a],
    [c, a, b],
    [c, b, a],
  ];
  return rotations.some(([x = 0, y = 0, z = 0]) => x <= pod.x && y <= pod.y && z <= pod.z);
}

export const chipsIn = (shape: string) => shape.split('x').reduce((n, side) => n * Number(side), 1);
