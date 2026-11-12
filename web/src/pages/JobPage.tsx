import { useEffect, useState } from 'react';
import { Link, useLocation, useParams } from 'react-router';
import { ApiError, api, type Job } from '../api.ts';
import { ConfirmDialog } from '../components/ConfirmDialog.tsx';
import { ErrorMessage, Loading, PageHeading } from '../components/Layout.tsx';
import { StateBadge } from '../components/StateBadge.tsx';
import { clock, duration, placementText, plural, priorityLabel } from '../format.ts';
import { useLive } from '../live.tsx';
import { useResource } from '../useResource.ts';

export default function JobPage() {
  const { id = '' } = useParams();
  const { version, announce } = useLive();
  const location = useLocation();
  const job = useResource(() => api.job(id), [id, version]);
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [cancelError, setCancelError] = useState<string>();
  const [refocus, setRefocus] = useState(false);
  const submitted = (location.state as { submitted?: boolean } | null)?.submitted === true;

  // After a cancel, the dialog closes and returns focus to the "Cancel job…" button, which then
  // disappears with the job's new state. Once that has happened, move focus to the heading.
  const state = job.data?.state;
  useEffect(() => {
    if (refocus && !confirming && state === 'cancelled') {
      document.getElementById('page-heading')?.focus();
      setRefocus(false);
    }
  }, [refocus, confirming, state]);

  if (job.error instanceof ApiError && job.error.status === 404) {
    return (
      <>
        <PageHeading title="Job not found" />
        <p>
          There is no job with the ID {id}. <Link to="/jobs">Go to the job list</Link>.
        </p>
      </>
    );
  }

  const j = job.data;
  const cancel = async () => {
    if (!j) return;
    setBusy(true);
    setCancelError(undefined);
    try {
      await api.cancel(j.id);
      setRefocus(true);
      setConfirming(false);
      announce(`Cancelled ${j.name}.`);
      job.reload();
    } catch (err) {
      setCancelError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <PageHeading title={j ? `Job ${j.name}` : 'Job'}>
        {j ? (
          <>
            {j.name} <StateBadge state={j.state} />
          </>
        ) : (
          'Job'
        )}
      </PageHeading>
      {job.loading && <Loading what="the job" />}
      {job.error && !j && <ErrorMessage error={job.error} retry={job.reload} />}
      {submitted && j && <p className="notice success">Job submitted.</p>}
      {j && (
        <>
          <JobDetails job={j} />
          {(j.state === 'queued' || j.state === 'running') && (
            <p>
              <button type="button" className="danger" onClick={() => setConfirming(true)}>
                Cancel job…
              </button>
            </p>
          )}
          <h2>History</h2>
          <ol className="timeline">
            {j.history?.map((h, i) => (
              <li key={i}>
                <span className="time">{clock(h.at)}</span> <StateBadge state={h.state} /> {h.reason}
              </li>
            ))}
          </ol>
          <ConfirmDialog
            open={confirming}
            title={`Cancel ${j.name}?`}
            confirmLabel="Cancel job"
            dismissLabel="Keep job"
            busy={busy}
            error={cancelError}
            onConfirm={cancel}
            onDismiss={() => {
              setConfirming(false);
              setCancelError(undefined);
            }}
          >
            <p>
              {j.state === 'running'
                ? `It stops now and frees its ${j.chips} chips. Its progress is lost.`
                : 'It leaves the queue and never runs.'}
            </p>
          </ConfirmDialog>
        </>
      )}
    </>
  );
}

function JobDetails({ job: j }: { job: Job }) {
  return (
    <dl className="details">
      <dt>Team</dt>
      <dd>{j.tenant}</dd>
      <dt>Priority</dt>
      <dd>{priorityLabel[j.priority]}</dd>
      <dt>Slice</dt>
      <dd>
        {j.shape} ({j.chips} chips)
      </dd>
      <dt>Run time</dt>
      <dd>{duration(j.durationMinutes * 60)}</dd>
      <dt>Submitted</dt>
      <dd>{clock(j.submittedAt)}</dd>
      <dt>{j.startedAt === null ? 'Waiting for' : 'Waited'}</dt>
      <dd>{duration(j.waitSeconds)}</dd>
      {j.startedAt !== null && (
        <>
          <dt>{j.preemptions > 0 ? 'Last started' : 'Started'}</dt>
          <dd>{clock(j.startedAt)}</dd>
        </>
      )}
      {j.state === 'running' && j.placement && j.endsAt !== null && (
        <>
          <dt>Placement</dt>
          <dd>{placementText(j.placement)}</dd>
          <dt>Ends</dt>
          <dd>{clock(j.endsAt)}</dd>
        </>
      )}
      {j.state === 'queued' && (
        <>
          <dt>Queue position</dt>
          <dd>{j.queuePosition ?? 'unknown'}</dd>
          {j.reservation && (
            <>
              <dt>Reserved</dt>
              <dd>
                Starts by {clock(j.reservation.at)} on {placementText(j.reservation.placement)}. Smaller jobs may run ahead
                of it only if they cannot delay it.
              </dd>
            </>
          )}
        </>
      )}
      {j.endedAt !== null && (
        <>
          <dt>Ended</dt>
          <dd>{clock(j.endedAt)}</dd>
        </>
      )}
      <dt>Preemptions</dt>
      <dd>{j.preemptions === 0 ? 'None' : plural(j.preemptions, 'time')}</dd>
    </dl>
  );
}
