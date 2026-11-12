import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import type { JobEvent } from './api.ts';
import { plural } from './format.ts';

export interface Live {
  /** Reads a message to screen reader users through a polite live region. */
  announce: (message: string) => void;
  /** Whether a summary of background changes is announced, at most once per SUMMARY_MS. */
  announceUpdates: boolean;
  setAnnounceUpdates: (on: boolean) => void;
  /** Increases when the server reports changes; pages reload their data when it changes. */
  version: number;
  /** Recent job events, newest first. */
  recent: JobEvent[];
  connected: boolean;
}

export const SUMMARY_MS = 10_000;
const REFRESH_MS = 400;
const PREFERENCE_KEY = 'slicewise.announceUpdates';

const LiveContext = createContext<Live | null>(null);

export function useLive(): Live {
  const live = useContext(LiveContext);
  if (!live) throw new Error('useLive must be used inside <LiveProvider>');
  return live;
}

const verbs: Record<JobEvent['type'], [string, string]> = {
  submitted: ['job submitted', 'jobs submitted'],
  started: ['job started', 'jobs started'],
  preempted: ['job preempted', 'jobs preempted'],
  succeeded: ['job finished', 'jobs finished'],
  cancelled: ['job cancelled', 'jobs cancelled'],
};

/** Summarizes counted events, for example "2 jobs started and 1 job finished." */
export function summarize(counts: Partial<Record<JobEvent['type'], number>>): string {
  const parts = (Object.keys(verbs) as JobEvent['type'][])
    .filter((t) => (counts[t] ?? 0) > 0)
    .map((t) => {
      const n = counts[t] ?? 0;
      const [one, many] = verbs[t];
      return `${n} ${n === 1 ? one : many}`;
    });
  if (parts.length === 0) return '';
  const last = parts.pop();
  return `Update: ${parts.length ? `${parts.join(', ')} and ${last}` : last}.`;
}

/**
 * Provides announcements and live server events.
 *
 * Screen reader users choose whether background activity is announced. When it is, changes are
 * summarized at most every ten seconds instead of read one by one, so a busy pod does not drown
 * out everything else. Messages caused by the user's own actions are always announced.
 */
export function LiveProvider({ children, eventsUrl = '/api/v1/events' }: { children: ReactNode; eventsUrl?: string }) {
  const [message, setMessage] = useState('');
  const [version, setVersion] = useState(0);
  const [recent, setRecent] = useState<JobEvent[]>([]);
  const [connected, setConnected] = useState(false);
  const [announceUpdates, setAnnounceUpdatesState] = useState(() => localStorage.getItem(PREFERENCE_KEY) === 'on');
  const counts = useRef<Partial<Record<JobEvent['type'], number>>>({});
  const refreshTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const clearTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const announce = useCallback((text: string) => {
    // Clearing first makes screen readers read a message again even if it repeats.
    setMessage('');
    clearTimeout(clearTimer.current);
    clearTimer.current = setTimeout(() => setMessage(text), 50);
  }, []);

  const setAnnounceUpdates = useCallback((on: boolean) => {
    localStorage.setItem(PREFERENCE_KEY, on ? 'on' : 'off');
    counts.current = {};
    setAnnounceUpdatesState(on);
  }, []);

  useEffect(() => {
    if (typeof EventSource === 'undefined') return;
    const source = new EventSource(eventsUrl);
    const scheduleRefresh = () => {
      refreshTimer.current ??= setTimeout(() => {
        refreshTimer.current = undefined;
        setVersion((v) => v + 1);
      }, REFRESH_MS);
    };
    source.onopen = () => setConnected(true);
    source.onerror = () => setConnected(false);
    source.addEventListener('job', (e) => {
      const event = JSON.parse((e as MessageEvent<string>).data) as JobEvent;
      counts.current[event.type] = (counts.current[event.type] ?? 0) + 1;
      setRecent((r) => [event, ...r].slice(0, 30));
      scheduleRefresh();
    });
    source.addEventListener('reset', scheduleRefresh);
    return () => {
      source.close();
      clearTimeout(refreshTimer.current);
      refreshTimer.current = undefined;
    };
  }, [eventsUrl]);

  useEffect(() => {
    if (!announceUpdates) return;
    const timer = setInterval(() => {
      const text = summarize(counts.current);
      counts.current = {};
      if (text) announce(text);
    }, SUMMARY_MS);
    return () => clearInterval(timer);
  }, [announceUpdates, announce]);

  useEffect(() => () => clearTimeout(clearTimer.current), []);

  const value = useMemo(
    () => ({ announce, announceUpdates, setAnnounceUpdates, version, recent, connected }),
    [announce, announceUpdates, setAnnounceUpdates, version, recent, connected],
  );
  return (
    <LiveContext.Provider value={value}>
      {children}
      <div className="visually-hidden" aria-live="polite" aria-atomic="true" data-testid="announcer">
        {message}
      </div>
    </LiveContext.Provider>
  );
}

/** Text for a job count, for example "Showing 3 jobs." */
export const showing = (n: number) => `Showing ${plural(n, 'job')}.`;
