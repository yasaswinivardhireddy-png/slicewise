import { useEffect, useRef, type ReactNode } from 'react';
import { Link, NavLink, Outlet, useLocation } from 'react-router';
import { useLive } from '../live.tsx';

/** The page's h1. It sets the document title; after client-side navigation, focus moves to it. */
export function PageHeading({ title, children }: { title: string; children?: ReactNode }) {
  useEffect(() => {
    document.title = `${title} · Slicewise`;
  }, [title]);
  return (
    <h1 id="page-heading" tabIndex={-1}>
      {children ?? title}
    </h1>
  );
}

export function Layout() {
  const { pathname } = useLocation();
  const { announceUpdates, setAnnounceUpdates, connected } = useLive();
  const firstPage = useRef(true);

  // On the first page load focus stays at the top of the document, where the skip link is. After
  // client-side navigation, focus moves to the new page's heading, as a full page load would.
  useEffect(() => {
    if (firstPage.current) {
      firstPage.current = false;
      return;
    }
    document.getElementById('page-heading')?.focus();
  }, [pathname]);

  return (
    <>
      <a className="skip-link" href="#main">
        Skip to main content
      </a>
      <header className="topbar">
        <Link to="/" className="brand">
          <img src="/favicon.svg" alt="" width="24" height="24" />
          Slicewise
        </Link>
        <nav aria-label="Main">
          <ul>
            <li>
              <NavLink to="/" end>
                Pod
              </NavLink>
            </li>
            <li>
              <NavLink to="/jobs" end>
                Jobs
              </NavLink>
            </li>
            <li>
              <NavLink to="/jobs/new">Submit a job</NavLink>
            </li>
            <li>
              <NavLink to="/simulator">Policy simulator</NavLink>
            </li>
          </ul>
        </nav>
        <div className="live-controls">
          <span className="connection">
            <span aria-hidden="true" className={connected ? 'dot on' : 'dot'} />
            {connected ? 'Live' : 'Connecting…'}
          </span>
          <button
            type="button"
            className="toggle"
            aria-pressed={announceUpdates}
            aria-describedby="announce-help"
            onClick={() => setAnnounceUpdates(!announceUpdates)}
          >
            Announce updates
            <span aria-hidden="true" className="toggle-state">
              {announceUpdates ? 'On' : 'Off'}
            </span>
          </button>
          <span id="announce-help" className="visually-hidden">
            When on, screen readers hear a summary of job changes at most every 10 seconds.
          </span>
        </div>
      </header>
      <main id="main" tabIndex={-1}>
        <Outlet />
      </main>
      <footer className="footer">
        <p>
          Slicewise schedules ML jobs onto slices of a simulated accelerator pod. Simulated time runs faster than real time.{' '}
          <a href="https://github.com/yasaswinivardhireddy-png/slicewise">Source code on GitHub</a>
        </p>
      </footer>
    </>
  );
}

export function Loading({ what }: { what: string }) {
  return (
    <p className="muted" role="status">
      Loading {what}…
    </p>
  );
}

export function ErrorMessage({ error, retry }: { error: Error; retry?: () => void }) {
  return (
    <div className="notice error" role="alert">
      <p>{error.message}</p>
      {retry && (
        <button type="button" onClick={retry}>
          Try again
        </button>
      )}
    </div>
  );
}
