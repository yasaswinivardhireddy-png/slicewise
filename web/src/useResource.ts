import { useCallback, useEffect, useState } from 'react';

export interface Resource<T> {
  data: T | undefined;
  error: Error | undefined;
  /** True while the first load is in flight. Later reloads keep showing the current data. */
  loading: boolean;
  reload: () => void;
}

/** Loads data for a component and ignores responses that arrive after it changed or unmounted. */
export function useResource<T>(load: () => Promise<T>, deps: readonly unknown[]): Resource<T> {
  const [state, setState] = useState<{ data?: T; error?: Error; loading: boolean }>({ loading: true });
  const [attempt, setAttempt] = useState(0);
  const reload = useCallback(() => setAttempt((n) => n + 1), []);

  useEffect(() => {
    let current = true;
    load().then(
      (data) => current && setState({ data, loading: false }),
      (error: unknown) =>
        current && setState((s) => ({ ...s, error: error instanceof Error ? error : new Error(String(error)), loading: false })),
    );
    return () => {
      current = false;
    };
    // `load` is recreated on every render; the caller's deps say when to reload.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, attempt]);

  return { data: state.data, error: state.error, loading: state.loading && state.data === undefined, reload };
}
