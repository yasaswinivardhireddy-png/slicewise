import { useState, type FormEvent } from 'react';
import { api, type Simulation } from '../api.ts';
import { BarChart } from '../components/BarChart.tsx';
import { PageHeading } from '../components/Layout.tsx';
import { count, hours, percent } from '../format.ts';
import { useLive } from '../live.tsx';

interface Params {
  seed: string;
  jobs: string;
  load: string;
}

type Errors = Partial<Record<keyof Params, string>>;

export function validateParams(p: Params): Errors {
  const errors: Errors = {};
  const seed = Number(p.seed);
  const jobs = Number(p.jobs);
  const load = Number(p.load);
  if (!p.seed.trim() || !Number.isInteger(seed) || seed < 0) errors.seed = 'Enter a whole number of 0 or more.';
  if (!p.jobs.trim() || !Number.isInteger(jobs) || jobs < 50 || jobs > 3000) errors.jobs = 'Enter a whole number from 50 to 3,000.';
  if (!p.load.trim() || Number.isNaN(load) || load < 0.3 || load > 1.5) errors.load = 'Enter a number from 0.3 to 1.5.';
  return errors;
}

const FIELDS: { key: keyof Params; label: string; hint: string }[] = [
  { key: 'seed', label: 'Workload seed', hint: 'The same seed always generates the same jobs.' },
  { key: 'jobs', label: 'Number of jobs', hint: 'From 50 to 3,000.' },
  { key: 'load', label: 'Offered load', hint: 'Requested chip-time divided by the pod’s chip-time, from 0.3 to 1.5.' },
];

export default function SimulatorPage() {
  const { announce } = useLive();
  const [params, setParams] = useState<Params>({ seed: '1', jobs: '1000', load: '0.9' });
  const [errors, setErrors] = useState<Errors>({});
  const [running, setRunning] = useState(false);
  const [failure, setFailure] = useState<string>();
  const [result, setResult] = useState<Simulation>();

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (running) return;
    const found = validateParams(params);
    setErrors(found);
    const bad = FIELDS.find((f) => found[f.key]);
    if (bad) {
      document.getElementById(bad.key)?.focus();
      return;
    }
    setRunning(true);
    setFailure(undefined);
    announce('Running the simulation…');
    try {
      const sim = await api.simulate({ seed: Number(params.seed), jobs: Number(params.jobs), load: Number(params.load) });
      setResult(sim);
      const best = sim.results.reduce((a, b) => (b.utilization > a.utilization ? b : a));
      announce(`Simulation finished in ${(sim.elapsedMs / 1000).toFixed(1)} seconds. Highest utilization: ${best.policy}, ${percent(best.utilization)}.`);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setRunning(false);
    }
  };

  return (
    <>
      <PageHeading title="Policy simulator" />
      <p>
        Replays the same synthetic workload under five scheduling policies, from first-in-first-out to Slicewise’s
        default, and compares utilization and queue waits. Jobs ask for slices from 4 to 256 chips of an 8x8x8 pod.
      </p>
      <form noValidate onSubmit={onSubmit} className="form inline-form">
        {FIELDS.map((f) => (
          <div key={f.key} className={errors[f.key] ? 'field has-error' : 'field'}>
            <label htmlFor={f.key}>{f.label}</label>
            <p id={`${f.key}-hint`} className="hint">
              {f.hint}
            </p>
            {errors[f.key] && (
              <p id={`${f.key}-error`} className="field-error">
                <span className="visually-hidden">Error:</span>{' '}{errors[f.key]}
              </p>
            )}
            <input
              id={f.key}
              type="text"
              inputMode="decimal"
              autoComplete="off"
              className="short"
              value={params[f.key]}
              onChange={(e) => setParams((p) => ({ ...p, [f.key]: e.target.value }))}
              aria-invalid={errors[f.key] ? true : undefined}
              aria-describedby={`${f.key}-hint${errors[f.key] ? ` ${f.key}-error` : ''}`}
            />
          </div>
        ))}
        <button type="submit" className="primary" aria-disabled={running || undefined}>
          {running ? 'Running…' : 'Run simulation'}
        </button>
      </form>
      {failure && (
        <p className="notice error" role="alert">
          {failure}
        </p>
      )}

      <section aria-labelledby="results-title" aria-busy={running}>
        <h2 id="results-title">Results</h2>
        {!result ? (
          <p className="muted">Run a simulation to see results.</p>
        ) : (
          <>
            <p>
              {count(result.jobs)} jobs on an {result.pod} pod at offered load {result.load}, seed {result.seed}. Simulated in{' '}
              {count(result.elapsedMs)} ms.
            </p>
            <div className="table-scroll" role="region" aria-labelledby="sim-caption" tabIndex={0}>
              <table className="nowrap-rows">
                <caption id="sim-caption">Policy comparison</caption>
                <thead>
                  <tr>
                    <th scope="col">Policy</th>
                    <th scope="col" className="num">
                      Utilization
                    </th>
                    <th scope="col" className="num">
                      Mean wait
                    </th>
                    <th scope="col" className="num">
                      p95 wait
                    </th>
                    <th scope="col" className="num">
                      Production p95 wait
                    </th>
                    <th scope="col" className="num">
                      Backfilled
                    </th>
                    <th scope="col" className="num">
                      Preemptions
                    </th>
                    <th scope="col" className="num">
                      Work lost (chip-hours)
                    </th>
                    <th scope="col" className="num">
                      Delayed by fragmentation
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {result.results.map((r) => (
                    <tr key={r.policy}>
                      <th scope="row">{r.policy}</th>
                      <td className="num">{percent(r.utilization)}</td>
                      <td className="num">{hours(r.meanWaitHours)}</td>
                      <td className="num">{hours(r.p95WaitHours)}</td>
                      <td className="num">{hours(r.productionP95WaitHours)}</td>
                      <td className="num">{count(r.backfilled)}</td>
                      <td className="num">{count(r.preemptions)}</td>
                      <td className="num">{count(Math.round(r.lostChipHours))}</td>
                      <td className="num">{count(r.fragmentationDelayed)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="charts">
              <BarChart
                title="Utilization by policy"
                summary={summaryOf(result, 'utilization')}
                bars={result.results.map((r) => ({ label: r.policy, value: r.utilization }))}
                format={percent}
              />
              <BarChart
                title="Mean queue wait by policy"
                summary={summaryOf(result, 'meanWaitHours')}
                bars={result.results.map((r) => ({ label: r.policy, value: r.meanWaitHours }))}
                format={hours}
              />
            </div>
          </>
        )}
      </section>
    </>
  );
}

function summaryOf(sim: Simulation, key: 'utilization' | 'meanWaitHours'): string {
  const fmt = key === 'utilization' ? percent : hours;
  const sorted = [...sim.results].sort((a, b) => a[key] - b[key]);
  const low = sorted[0];
  const high = sorted[sorted.length - 1];
  if (!low || !high) return '';
  return `Lowest: ${low.policy}, ${fmt(low[key])}. Highest: ${high.policy}, ${fmt(high[key])}.`;
}
