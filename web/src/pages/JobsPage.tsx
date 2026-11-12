import { useEffect, useMemo, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router';
import { api, type Job } from '../api.ts';
import { ErrorMessage, Loading, PageHeading } from '../components/Layout.tsx';
import { StateBadge } from '../components/StateBadge.tsx';
import { clock, duration, priorityLabel } from '../format.ts';
import { showing, useLive } from '../live.tsx';
import { useResource } from '../useResource.ts';

type SortKey = 'name' | 'tenant' | 'priority' | 'chips' | 'state' | 'submittedAt' | 'waitSeconds';
type Direction = 'ascending' | 'descending';

const columns: { key: SortKey; label: string; numeric?: boolean }[] = [
  { key: 'name', label: 'Job' },
  { key: 'tenant', label: 'Team' },
  { key: 'priority', label: 'Priority' },
  { key: 'chips', label: 'Chips', numeric: true },
  { key: 'state', label: 'State' },
  { key: 'submittedAt', label: 'Submitted' },
  { key: 'waitSeconds', label: 'Wait', numeric: true },
];

const priorityRank = { production: 2, batch: 1, 'best-effort': 0 } as const;
const stateRank = { running: 0, queued: 1, succeeded: 2, cancelled: 3 } as const;

function compare(a: Job, b: Job, key: SortKey): number {
  switch (key) {
    case 'priority':
      return priorityRank[a.priority] - priorityRank[b.priority];
    case 'state':
      return stateRank[a.state] - stateRank[b.state];
    case 'name':
    case 'tenant':
      return a[key].localeCompare(b[key]);
    default:
      return a[key] - b[key];
  }
}

const STATES = [
  { value: 'all', label: 'All' },
  { value: 'running', label: 'Running' },
  { value: 'queued', label: 'Queued' },
  { value: 'finished', label: 'Finished' },
];

export default function JobsPage() {
  const [params, setParams] = useSearchParams();
  const state = params.get('state') ?? 'all';
  const team = params.get('team') ?? '';
  const filterKey = `${state}|${team}`;
  const { version, announce } = useLive();
  const jobs = useResource(
    async () => ({ key: filterKey, ...(await api.jobs({ state: state === 'all' ? '' : state, tenant: team })) }),
    [filterKey, version],
  );
  const tenants = useResource(() => api.tenants(), []);
  const [sort, setSort] = useState<{ key: SortKey; dir: Direction }>({ key: 'submittedAt', dir: 'descending' });

  // Announce the number of results after the user changes a filter, but not on background updates.
  const announcedFor = useRef(filterKey);
  useEffect(() => {
    if (jobs.data?.key === filterKey && announcedFor.current !== filterKey) {
      announcedFor.current = filterKey;
      announce(showing(jobs.data.jobs.length));
    }
  }, [jobs.data, filterKey, announce]);

  const sorted = useMemo(() => {
    const list = [...(jobs.data?.jobs ?? [])];
    list.sort((a, b) => compare(a, b, sort.key) * (sort.dir === 'ascending' ? 1 : -1) || b.submittedAt - a.submittedAt);
    return list;
  }, [jobs.data, sort]);

  const setFilter = (name: 'state' | 'team', value: string) => {
    const next = new URLSearchParams(params);
    if (value && value !== 'all') next.set(name, value);
    else next.delete(name);
    setParams(next, { replace: true });
  };

  const sortBy = (key: SortKey, label: string) => {
    const dir: Direction = sort.key === key && sort.dir === 'ascending' ? 'descending' : 'ascending';
    setSort({ key, dir });
    announce(`Sorted by ${label}, ${dir}.`);
  };

  return (
    <>
      <PageHeading title="Jobs" />
      <div className="filters">
        <div className="field inline">
          <label htmlFor="state-filter">State</label>
          <select id="state-filter" value={state} onChange={(e) => setFilter('state', e.target.value)}>
            {STATES.map((s) => (
              <option key={s.value} value={s.value}>
                {s.label}
              </option>
            ))}
          </select>
        </div>
        <div className="field inline">
          <label htmlFor="team-filter">Team</label>
          <select id="team-filter" value={team} onChange={(e) => setFilter('team', e.target.value)}>
            <option value="">All teams</option>
            {tenants.data?.tenants.map((t) => (
              <option key={t.name} value={t.name}>
                {t.name}
              </option>
            ))}
          </select>
        </div>
        <Link className="button primary" to="/jobs/new">
          Submit a job
        </Link>
      </div>

      {jobs.loading && <Loading what="jobs" />}
      {jobs.error && !jobs.data && <ErrorMessage error={jobs.error} retry={jobs.reload} />}
      {jobs.data && (
        <>
          <p className="muted">{showing(sorted.length)} Newest first unless you sort by a column.</p>
          {sorted.length === 0 ? (
            <p>No jobs match these filters.</p>
          ) : (
            <div className="table-scroll" role="region" aria-labelledby="jobs-caption" tabIndex={0}>
              <table className="sortable">
                <caption id="jobs-caption">Jobs</caption>
                <thead>
                  <tr>
                    {columns.map((c) => (
                      <th
                        key={c.key}
                        scope="col"
                        className={c.numeric ? 'num' : undefined}
                        aria-sort={sort.key === c.key ? sort.dir : undefined}
                      >
                        <button type="button" onClick={() => sortBy(c.key, c.label)}>
                          {c.label}
                          <span aria-hidden="true" className="sort-icon">
                            {sort.key === c.key ? (sort.dir === 'ascending' ? '▲' : '▼') : '↕'}
                          </span>
                        </button>
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {sorted.map((j) => (
                    <tr key={j.id}>
                      <th scope="row">
                        <Link to={`/jobs/${j.id}`}>{j.name}</Link>
                      </th>
                      <td>{j.tenant}</td>
                      <td>{priorityLabel[j.priority]}</td>
                      <td className="num">{j.chips}</td>
                      <td>
                        <StateBadge state={j.state} />
                      </td>
                      <td>{clock(j.submittedAt)}</td>
                      <td className="num">{duration(j.waitSeconds)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </>
  );
}
