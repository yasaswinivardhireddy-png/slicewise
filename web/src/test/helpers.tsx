import { render } from '@testing-library/react';
import axe from 'axe-core';
import { MemoryRouter } from 'react-router';
import { vi } from 'vitest';
import { App } from '../App.tsx';
import type { Job, Pod, TenantUsage } from '../api.ts';
import { LiveProvider } from '../live.tsx';

type Reply = { status?: number; body?: unknown } | undefined;
export type Route = (url: URL, method: string, body: unknown) => Reply;

export interface Call {
  path: string;
  method: string;
  body: unknown;
}

/** Replaces fetch with a router of canned replies and records every call. */
export function mockApi(route: Route): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input, 'http://localhost');
      const method = init?.method ?? 'GET';
      const body: unknown = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ path: url.pathname + url.search, method, body });
      const reply = route(url, method, body) ?? { status: 404, body: { title: 'Not found', status: 404 } };
      return new Response(JSON.stringify(reply.body ?? {}), {
        status: reply.status ?? 200,
        headers: { 'content-type': 'application/json' },
      });
    }),
  );
  return calls;
}

/** Renders the whole app, with its landmarks and live region, at a path. */
export function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <LiveProvider>
        <App />
      </LiveProvider>
    </MemoryRouter>,
  );
}

/** Returns axe-core violations. jsdom cannot compute colors, so contrast is checked in the browser tests. */
export async function axeViolations(root: Element = document.body): Promise<string[]> {
  const results = await axe.run(root, { rules: { 'color-contrast': { enabled: false } } });
  return results.violations.map((v) => `${v.id}: ${v.help} (${v.nodes.map((n) => n.target.join(' ')).join('; ')})`);
}

export const job = (overrides: Partial<Job> = {}): Job => ({
  id: 'j-1',
  name: 'train-1',
  tenant: 'research',
  priority: 'production',
  shape: '2x2x1',
  chips: 4,
  durationMinutes: 120,
  state: 'running',
  submittedAt: 600,
  startedAt: 600,
  endsAt: 7800,
  endedAt: null,
  waitSeconds: 0,
  placement: { origin: { x: 0, y: 0, z: 0 }, size: { x: 2, y: 2, z: 1 } },
  preemptions: 0,
  history: [
    { at: 600, state: 'queued', reason: 'submitted' },
    { at: 600, state: 'running', reason: 'placed on 2x2x1 at (0,0,0)' },
  ],
  ...overrides,
});

const evalJob = job({
  id: 'j-2',
  name: 'eval-2',
  tenant: 'serving',
  priority: 'batch',
  shape: '1x2x1',
  chips: 2,
  placement: { origin: { x: 2, y: 0, z: 0 }, size: { x: 1, y: 2, z: 1 } },
});

/** A 4x2x2 pod: j-1 on x 0-1, y 0-1 of layer 0; j-2 on x 2, y 0-1 of layer 0; layer 1 idle. */
export const pod: Pod = {
  dims: { x: 4, y: 2, z: 2 },
  chips: 16,
  busyChips: 6,
  now: 3600,
  speed: 60,
  policy: 'best fit + backfill + preemption',
  queued: 1,
  cells: ['j-1', 'j-1', 'j-2', '', 'j-1', 'j-1', 'j-2', '', '', '', '', '', '', '', '', ''],
  running: [job(), evalJob],
};

export const tenants: TenantUsage[] = [
  { name: 'research', quota: 6, used: 4, running: 1, queued: 0 },
  { name: 'serving', quota: 6, used: 2, running: 1, queued: 1 },
];

export const jobs: Job[] = [
  evalJob,
  job(),
  job({ id: 'j-3', name: 'sweep-3', tenant: 'serving', priority: 'best-effort', shape: '2x2x2', chips: 8, state: 'queued', startedAt: null, endsAt: null, placement: null, waitSeconds: 900, submittedAt: 2700 }),
];

/** Replies for the read-only endpoints every page uses. */
export const baseRoutes: Route = (url) => {
  if (url.pathname === '/api/v1/pod') return { body: pod };
  if (url.pathname === '/api/v1/tenants') return { body: { tenants } };
  if (url.pathname === '/api/v1/jobs') return { body: { now: 3600, jobs } };
  const match = /^\/api\/v1\/jobs\/(j-\d+)$/.exec(url.pathname);
  const found = jobs.find((j) => j.id === match?.[1]);
  if (found) return { body: found };
  return undefined;
};
