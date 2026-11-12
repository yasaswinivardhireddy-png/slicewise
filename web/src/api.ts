export type Priority = 'production' | 'batch' | 'best-effort';
export type JobState = 'queued' | 'running' | 'succeeded' | 'cancelled';

export interface Dims {
  x: number;
  y: number;
  z: number;
}

export interface Placement {
  origin: Dims;
  size: Dims;
}

export interface HistoryEntry {
  at: number;
  state: JobState;
  reason: string;
}

/** Times are simulated seconds since the server started. */
export interface Job {
  id: string;
  name: string;
  tenant: string;
  priority: Priority;
  shape: string;
  chips: number;
  durationMinutes: number;
  state: JobState;
  submittedAt: number;
  startedAt: number | null;
  endsAt: number | null;
  endedAt: number | null;
  waitSeconds: number;
  placement: Placement | null;
  preemptions: number;
  queuePosition?: number;
  reservation?: { at: number; placement: Placement };
  history?: HistoryEntry[];
}

export interface Pod {
  dims: Dims;
  chips: number;
  busyChips: number;
  now: number;
  speed: number;
  policy: string;
  queued: number;
  /** Job ID per chip ("" when idle), indexed (z * dims.y + y) * dims.x + x. */
  cells: string[];
  running: Job[];
}

export interface TenantUsage {
  name: string;
  quota: number;
  used: number;
  running: number;
  queued: number;
}

export interface JobDraft {
  name: string;
  tenant: string;
  priority: Priority | '';
  shape: string;
  durationMinutes: number;
}

export interface SimulationResult {
  policy: string;
  utilization: number;
  meanWaitHours: number;
  p95WaitHours: number;
  productionP95WaitHours: number;
  backfilled: number;
  preemptions: number;
  lostChipHours: number;
  fragmentationDelayed: number;
}

export interface Simulation {
  pod: string;
  seed: number;
  jobs: number;
  load: number;
  elapsedMs: number;
  results: SimulationResult[];
}

export interface JobEvent {
  seq: number;
  at: number;
  type: 'submitted' | 'started' | 'preempted' | 'succeeded' | 'cancelled';
  jobId: string;
  jobName: string;
  detail: string;
}

/** A failed API call. fieldErrors maps request fields to messages when validation failed. */
export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly fieldErrors: Record<string, string> = {},
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      ...init,
      headers: { accept: 'application/json', ...(init?.body ? { 'content-type': 'application/json' } : {}) },
    });
  } catch {
    throw new ApiError('Cannot reach the Slicewise server. Check that it is running, then try again.', 0);
  }
  const body: unknown = await res.json().catch(() => undefined);
  if (!res.ok) {
    const p = (body ?? {}) as { title?: string; detail?: string; errors?: Record<string, string> };
    const message = p.title ? `${p.title}${p.detail ? `. ${p.detail}` : ''}` : `The request failed (HTTP ${res.status}).`;
    throw new ApiError(message, res.status, p.errors ?? {});
  }
  return body as T;
}

export const api = {
  pod: () => request<Pod>('/api/v1/pod'),
  jobs: (filter: { state?: string; tenant?: string } = {}) => {
    const q = new URLSearchParams();
    if (filter.state) q.set('state', filter.state);
    if (filter.tenant) q.set('tenant', filter.tenant);
    const query = q.toString();
    return request<{ now: number; jobs: Job[] }>(`/api/v1/jobs${query ? `?${query}` : ''}`);
  },
  job: (id: string) => request<Job>(`/api/v1/jobs/${encodeURIComponent(id)}`),
  submit: (draft: JobDraft) => request<Job>('/api/v1/jobs', { method: 'POST', body: JSON.stringify(draft) }),
  cancel: (id: string) => request<Job>(`/api/v1/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST' }),
  tenants: () => request<{ tenants: TenantUsage[] }>('/api/v1/tenants'),
  simulate: (params: { seed: number; jobs: number; load: number }) =>
    request<Simulation>('/api/v1/simulations', { method: 'POST', body: JSON.stringify(params) }),
};
