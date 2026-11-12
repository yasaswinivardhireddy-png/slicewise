import { useEffect } from 'react';
import { Link, useNavigate } from 'react-router';
import { api, type Pod } from '../api.ts';
import { ErrorMessage, Loading, PageHeading } from '../components/Layout.tsx';
import { PodGrid, colorClass, jobNumber } from '../components/PodGrid.tsx';
import { clock, count, percent, placementText, priorityLabel, shapeText } from '../format.ts';
import { useLive } from '../live.tsx';
import { useResource } from '../useResource.ts';

export default function PodPage() {
  const { version } = useLive();
  const navigate = useNavigate();
  const pod = useResource(() => api.pod(), [version]);

  // Reload now and then, so the simulated clock moves even when no job changes.
  useEffect(() => {
    const timer = setInterval(pod.reload, 5000);
    return () => clearInterval(timer);
  }, [pod.reload]);

  return (
    <>
      <PageHeading title="Pod" />
      {pod.loading && <Loading what="the pod" />}
      {pod.error && !pod.data && <ErrorMessage error={pod.error} retry={pod.reload} />}
      {pod.data && <PodView pod={pod.data} onOpen={(id) => navigate(`/jobs/${id}`)} />}
    </>
  );
}

function PodView({ pod, onOpen }: { pod: Pod; onOpen: (id: string) => void }) {
  const { recent } = useLive();
  return (
    <>
      <dl className="stats">
        <div>
          <dt>Chips in use</dt>
          <dd>
            {count(pod.busyChips)} of {count(pod.chips)} ({percent(pod.busyChips / pod.chips)})
          </dd>
        </div>
        <div>
          <dt>Queued jobs</dt>
          <dd>
            <Link to="/jobs?state=queued">{count(pod.queued)}</Link>
          </dd>
        </div>
        <div>
          <dt>Pod</dt>
          <dd>
            {shapeText(pod.dims)} torus, {count(pod.chips)} chips
          </dd>
        </div>
        <div>
          <dt>Simulated time</dt>
          <dd>
            {clock(pod.now)} ({pod.speed}× real time)
          </dd>
        </div>
        <div>
          <dt>Policy</dt>
          <dd>{pod.policy}</dd>
        </div>
      </dl>

      <h2>Chips</h2>
      <PodGrid pod={pod} onOpen={onOpen} />

      <h2 id="running-title">Running jobs</h2>
      {pod.running.length === 0 ? (
        <p>No jobs are running.</p>
      ) : (
        <div className="table-scroll" role="region" aria-labelledby="running-title" tabIndex={0}>
          <table>
            <caption className="visually-hidden">Running jobs and where they run</caption>
            <thead>
              <tr>
                <th scope="col">Job</th>
                <th scope="col">Team</th>
                <th scope="col">Priority</th>
                <th scope="col" className="num">
                  Chips
                </th>
                <th scope="col">Placement</th>
                <th scope="col">Ends</th>
              </tr>
            </thead>
            <tbody>
              {pod.running.map((j) => (
                <tr key={j.id}>
                  <th scope="row">
                    <span className={`swatch ${colorClass(j.id)}`} aria-hidden="true">
                      {jobNumber(j.id)}
                    </span>{' '}
                    <Link to={`/jobs/${j.id}`}>{j.name}</Link>
                  </th>
                  <td>{j.tenant}</td>
                  <td>{priorityLabel[j.priority]}</td>
                  <td className="num">{j.chips}</td>
                  <td>{j.placement ? placementText(j.placement) : ''}</td>
                  <td>{j.endsAt !== null ? clock(j.endsAt) : ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <h2>Recent activity</h2>
      {recent.length === 0 ? (
        <p className="muted">No activity since this page opened.</p>
      ) : (
        <ol className="activity" reversed>
          {recent.slice(0, 10).map((e) => (
            <li key={e.seq}>
              <span className="time">{clock(e.at)}</span> {e.detail}
            </li>
          ))}
        </ol>
      )}
    </>
  );
}
