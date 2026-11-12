import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import type { Simulation } from './api.ts';
import { baseRoutes, job, mockApi, renderAt } from './test/helpers.tsx';

const announcer = () => screen.getByTestId('announcer');

describe('submit form', () => {
  it('lists every problem in a focused summary that links to each field', async () => {
    mockApi(baseRoutes);
    renderAt('/jobs/new');
    const user = userEvent.setup();
    await screen.findByRole('option', { name: /research/ });
    await user.click(screen.getByRole('button', { name: 'Submit job' }));

    const summary = screen.getByRole('region', { name: 'There are 4 problems' });
    expect(summary).toHaveFocus();
    const name = screen.getByRole('textbox', { name: 'Job name' });
    expect(name).toHaveAttribute('aria-invalid', 'true');
    expect(name).toHaveAccessibleDescription(/Error: Enter a job name\./);
    expect(screen.getByRole('group', { name: 'Priority' })).toHaveAccessibleDescription('Error: Choose a priority.');

    await user.click(within(summary).getByRole('link', { name: 'Choose a team.' }));
    expect(screen.getByRole('combobox', { name: 'Team' })).toHaveFocus();
    await user.click(within(summary).getByRole('link', { name: 'Choose a priority.' }));
    expect(screen.getByRole('radio', { name: 'Production' })).toHaveFocus();
  });

  it('offers only shapes that fit the pod and submits a valid job', async () => {
    const calls = mockApi((url, method, body) => {
      if (url.pathname === '/api/v1/jobs' && method === 'POST') return { status: 201, body: job({ id: 'j-9', name: 'train-llm-7' }) };
      if (url.pathname === '/api/v1/jobs/j-9') return { body: job({ id: 'j-9', name: 'train-llm-7' }) };
      return baseRoutes(url, method, body);
    });
    renderAt('/jobs/new');
    const user = userEvent.setup();
    await screen.findByRole('option', { name: /research/ });
    const shape = screen.getByRole('combobox', { name: 'Slice shape' });
    await screen.findByRole('option', { name: '2x2x4 (16 chips)' });
    expect(within(shape).queryByRole('option', { name: /2x4x4/ })).not.toBeInTheDocument();

    await user.type(screen.getByRole('textbox', { name: 'Job name' }), 'train-llm-7');
    await user.selectOptions(screen.getByRole('combobox', { name: 'Team' }), 'research');
    await user.click(screen.getByRole('radio', { name: 'Batch' }));
    await user.selectOptions(shape, '2x2x1');
    const runTime = screen.getByRole('textbox', { name: 'Run time in minutes' });
    await user.clear(runTime);
    await user.type(runTime, '90');
    await user.click(screen.getByRole('button', { name: 'Submit job' }));

    expect(await screen.findByRole('heading', { level: 1, name: /train-llm-7/ })).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({
      name: 'train-llm-7',
      tenant: 'research',
      priority: 'batch',
      shape: '2x2x1',
      durationMinutes: 90,
    });
    expect(screen.getByText('Job submitted.')).toBeInTheDocument();
    await waitFor(() => expect(announcer()).toHaveTextContent('Submitted train-llm-7. It is running.'));
  });

  it('shows the server’s field errors the same way', async () => {
    mockApi((url, method, body) =>
      url.pathname === '/api/v1/jobs' && method === 'POST'
        ? { status: 400, body: { title: 'The job has problems', status: 400, errors: { name: 'That name is taken.' } } }
        : baseRoutes(url, method, body),
    );
    renderAt('/jobs/new');
    const user = userEvent.setup();
    await screen.findByRole('option', { name: /research/ });
    await user.type(screen.getByRole('textbox', { name: 'Job name' }), 'train-1');
    await user.selectOptions(screen.getByRole('combobox', { name: 'Team' }), 'serving');
    await user.click(screen.getByRole('radio', { name: 'Best effort' }));
    await user.selectOptions(screen.getByRole('combobox', { name: 'Slice shape' }), '2x2x2');
    await user.click(screen.getByRole('button', { name: 'Submit job' }));

    const summary = await screen.findByRole('region', { name: 'There is a problem' });
    expect(summary).toHaveFocus();
    expect(within(summary).getByRole('link', { name: 'That name is taken.' })).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Job name' })).toHaveAttribute('aria-invalid', 'true');
  });
});

const simulation: Simulation = {
  pod: '8x8x8',
  seed: 1,
  jobs: 500,
  load: 0.9,
  elapsedMs: 420,
  results: [
    { policy: 'FIFO, first fit', utilization: 0.657, meanWaitHours: 55.8, p95WaitHours: 104.2, productionP95WaitHours: 104.5, backfilled: 0, preemptions: 0, lostChipHours: 0, fragmentationDelayed: 312 },
    { policy: 'All: adds fair share and preemption', utilization: 0.859, meanWaitHours: 2.9, p95WaitHours: 10.6, productionP95WaitHours: 0, backfilled: 1459, preemptions: 301, lostChipHours: 11181, fragmentationDelayed: 84 },
  ],
};

describe('policy simulator', () => {
  it('checks the parameters, then shows the comparison as a table and charts', async () => {
    const calls = mockApi((url, method) => (url.pathname === '/api/v1/simulations' && method === 'POST' ? { body: simulation } : undefined));
    renderAt('/simulator');
    const user = userEvent.setup();
    const jobs = screen.getByRole('textbox', { name: 'Number of jobs' });
    await user.clear(jobs);
    await user.type(jobs, '5');
    await user.click(screen.getByRole('button', { name: 'Run simulation' }));
    expect(jobs).toHaveFocus();
    expect(jobs).toHaveAccessibleDescription(/Error: Enter a whole number from 50 to 3,000\./);
    expect(calls.filter((c) => c.path === '/api/v1/simulations')).toHaveLength(0);

    await user.clear(jobs);
    await user.type(jobs, '500');
    await user.click(screen.getByRole('button', { name: 'Run simulation' }));
    const table = await screen.findByRole('table', { name: 'Policy comparison' });
    expect(within(table).getAllByRole('row')).toHaveLength(3);
    expect(within(table).getByRole('rowheader', { name: 'FIFO, first fit' })).toBeInTheDocument();
    expect(calls.find((c) => c.path === '/api/v1/simulations')?.body).toEqual({ seed: 1, jobs: 500, load: 0.9 });
    expect(screen.getByRole('img', { name: /Utilization by policy/ })).toHaveAccessibleName(
      'Utilization by policy Lowest: FIFO, first fit, 65.7%. Highest: All: adds fair share and preemption, 85.9%.',
    );
    await waitFor(() => expect(announcer()).toHaveTextContent('Simulation finished in 0.4 seconds.'));
  });
});
