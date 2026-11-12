import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react';
import { useNavigate } from 'react-router';
import { ApiError, api, type Dims, type Priority } from '../api.ts';
import { ErrorMessage, PageHeading } from '../components/Layout.tsx';
import { SHAPES, chipsIn, priorityLabel, shapeFits, shapeText, stateLabel } from '../format.ts';
import { useLive } from '../live.tsx';
import { useResource } from '../useResource.ts';

export interface Draft {
  name: string;
  tenant: string;
  priority: Priority | '';
  shape: string;
  duration: string;
}

type Field = 'name' | 'tenant' | 'priority' | 'shape' | 'durationMinutes';
export type Errors = Partial<Record<Field | 'form', string>>;

const FIELDS: Field[] = ['name', 'tenant', 'priority', 'shape', 'durationMinutes'];
/** The element each error summary link moves focus to. */
const FIELD_IDS: Record<Field, string> = {
  name: 'name',
  tenant: 'tenant',
  priority: 'priority-production',
  shape: 'shape',
  durationMinutes: 'duration',
};

const PRIORITIES: { value: Priority; hint: string }[] = [
  { value: 'production', hint: 'Runs first and may preempt batch and best-effort jobs.' },
  { value: 'batch', hint: 'The usual choice for training and evaluation.' },
  { value: 'best-effort', hint: 'Uses spare chips and may be preempted.' },
];

/** Checks a draft the same way the server does, so most mistakes show up before submitting. */
export function validate(d: Draft, pod?: Dims): Errors {
  const errors: Errors = {};
  if (!d.name) errors.name = 'Enter a job name.';
  else if (!/^[a-z0-9][a-z0-9-]{2,39}$/.test(d.name))
    errors.name = 'Use 3 to 40 lowercase letters, numbers or hyphens, starting with a letter or number.';
  if (!d.tenant) errors.tenant = 'Choose a team.';
  if (!d.priority) errors.priority = 'Choose a priority.';
  if (!d.shape) errors.shape = 'Choose a slice shape.';
  else if (pod && !shapeFits(d.shape, pod)) errors.shape = `Choose a slice shape that fits the ${shapeText(pod)} pod.`;
  const minutes = Number(d.duration.trim());
  if (!d.duration.trim()) errors.durationMinutes = 'Enter a run time in minutes.';
  else if (!Number.isInteger(minutes) || minutes < 1 || minutes > 10_080)
    errors.durationMinutes = 'Enter a whole number of minutes from 1 to 10,080 (7 days).';
  return errors;
}

function FieldRow({ id, label, hint, error, children }: { id: string; label: string; hint?: string; error?: string; children: ReactNode }) {
  return (
    <div className={error ? 'field has-error' : 'field'}>
      <label htmlFor={id}>{label}</label>
      {hint && (
        <p id={`${id}-hint`} className="hint">
          {hint}
        </p>
      )}
      {error && (
        <p id={`${id}-error`} className="field-error">
          <span className="visually-hidden">Error:</span>{' '}{error}
        </p>
      )}
      {children}
    </div>
  );
}

const describedBy = (id: string, hint: boolean, error?: string) =>
  [hint ? `${id}-hint` : '', error ? `${id}-error` : ''].filter(Boolean).join(' ') || undefined;

export default function NewJobPage() {
  const navigate = useNavigate();
  const { announce } = useLive();
  const tenants = useResource(() => api.tenants(), []);
  const pod = useResource(() => api.pod(), []);
  const [draft, setDraft] = useState<Draft>({ name: '', tenant: '', priority: '', shape: '', duration: '60' });
  const [errors, setErrors] = useState<Errors>({});
  const [submitting, setSubmitting] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const summary = useRef<HTMLElement>(null);

  // Each failed submission moves focus to the error summary, which lists every problem.
  useEffect(() => {
    if (attempt > 0) summary.current?.focus();
  }, [attempt]);

  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => setDraft((d) => ({ ...d, [key]: value }));
  const dims = pod.data?.dims;
  const shapes = dims ? SHAPES.filter((s) => shapeFits(s, dims)) : SHAPES;

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (submitting) return;
    const found = validate(draft, dims);
    setErrors(found);
    if (Object.keys(found).length > 0) {
      setAttempt((n) => n + 1);
      return;
    }
    setSubmitting(true);
    try {
      const job = await api.submit({
        name: draft.name,
        tenant: draft.tenant,
        priority: draft.priority,
        shape: draft.shape,
        durationMinutes: Number(draft.duration),
      });
      announce(`Submitted ${job.name}. It is ${stateLabel[job.state].toLowerCase()}.`);
      navigate(`/jobs/${job.id}`, { state: { submitted: true } });
    } catch (err) {
      const fieldErrors = err instanceof ApiError ? err.fieldErrors : {};
      setErrors(Object.keys(fieldErrors).length > 0 ? fieldErrors : { form: err instanceof Error ? err.message : String(err) });
      setAttempt((n) => n + 1);
      setSubmitting(false);
    }
  };

  const problems = FIELDS.filter((f) => errors[f]);
  const total = problems.length + (errors.form ? 1 : 0);

  return (
    <>
      <PageHeading title="Submit a job" />
      <p>The scheduler places the job on a free slice of the pod, or queues it until one frees up.</p>
      {tenants.error && <ErrorMessage error={tenants.error} retry={tenants.reload} />}

      {total > 0 && (
        <section className="error-summary" ref={summary} tabIndex={-1} aria-labelledby="error-summary-title">
          <h2 id="error-summary-title">{total === 1 ? 'There is a problem' : `There are ${total} problems`}</h2>
          {errors.form && <p>{errors.form}</p>}
          <ul>
            {problems.map((f) => (
              <li key={f}>
                <a
                  href={`#${FIELD_IDS[f]}`}
                  onClick={(e) => {
                    e.preventDefault();
                    document.getElementById(FIELD_IDS[f])?.focus();
                  }}
                >
                  {errors[f]}
                </a>
              </li>
            ))}
          </ul>
        </section>
      )}

      <form noValidate onSubmit={onSubmit} className="form">
        <FieldRow id="name" label="Job name" hint="3 to 40 lowercase letters, numbers or hyphens, such as train-llm-7." error={errors.name}>
          <input
            id="name"
            type="text"
            autoComplete="off"
            spellCheck={false}
            value={draft.name}
            onChange={(e) => set('name', e.target.value)}
            aria-invalid={errors.name ? true : undefined}
            aria-describedby={describedBy('name', true, errors.name)}
          />
        </FieldRow>

        <FieldRow id="tenant" label="Team" error={errors.tenant}>
          <select
            id="tenant"
            value={draft.tenant}
            onChange={(e) => set('tenant', e.target.value)}
            aria-invalid={errors.tenant ? true : undefined}
            aria-describedby={describedBy('tenant', false, errors.tenant)}
          >
            <option value="">Choose a team</option>
            {tenants.data?.tenants.map((t) => (
              <option key={t.name} value={t.name}>
                {t.name} (quota {t.quota} chips, using {t.used})
              </option>
            ))}
          </select>
        </FieldRow>

        <fieldset className={errors.priority ? 'field has-error' : 'field'} aria-describedby={errors.priority ? 'priority-error' : undefined}>
          <legend>Priority</legend>
          {errors.priority && (
            <p id="priority-error" className="field-error">
              <span className="visually-hidden">Error:</span>{' '}{errors.priority}
            </p>
          )}
          {PRIORITIES.map((p) => (
            <div className="choice" key={p.value}>
              <input
                type="radio"
                id={`priority-${p.value}`}
                name="priority"
                value={p.value}
                checked={draft.priority === p.value}
                onChange={() => set('priority', p.value)}
                aria-describedby={`priority-${p.value}-hint`}
              />
              <label htmlFor={`priority-${p.value}`}>{priorityLabel[p.value]}</label>
              <p id={`priority-${p.value}-hint`} className="hint">
                {p.hint}
              </p>
            </div>
          ))}
        </fieldset>

        <FieldRow id="shape" label="Slice shape" hint="Chips along x, y and z. The scheduler may rotate the slice to fit." error={errors.shape}>
          <select
            id="shape"
            value={draft.shape}
            onChange={(e) => set('shape', e.target.value)}
            aria-invalid={errors.shape ? true : undefined}
            aria-describedby={describedBy('shape', true, errors.shape)}
          >
            <option value="">Choose a shape</option>
            {shapes.map((s) => (
              <option key={s} value={s}>
                {s} ({chipsIn(s)} chips)
              </option>
            ))}
          </select>
        </FieldRow>

        <FieldRow id="duration" label="Run time in minutes" hint="From 1 to 10,080 (7 days) of simulated time." error={errors.durationMinutes}>
          <input
            id="duration"
            type="text"
            inputMode="numeric"
            autoComplete="off"
            className="short"
            value={draft.duration}
            onChange={(e) => set('duration', e.target.value)}
            aria-invalid={errors.durationMinutes ? true : undefined}
            aria-describedby={describedBy('duration', true, errors.durationMinutes)}
          />
        </FieldRow>

        <button type="submit" className="primary" aria-disabled={submitting || undefined}>
          {submitting ? 'Submitting…' : 'Submit job'}
        </button>
      </form>
    </>
  );
}
