import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { baseRoutes, job, jobs, mockApi, renderAt } from './test/helpers.tsx';

const announcer = () => screen.getByTestId('announcer');

describe('job list', () => {
  it('sorts by a column, exposes the sort to assistive technology and announces it', async () => {
    mockApi(baseRoutes);
    renderAt('/jobs');
    const table = await screen.findByRole('table', { name: 'Jobs' });
    const user = userEvent.setup();
    await user.click(within(table).getByRole('button', { name: 'Chips' }));
    expect(within(table).getByRole('columnheader', { name: 'Chips' })).toHaveAttribute('aria-sort', 'ascending');
    expect(within(table).getAllByRole('rowheader').map((h) => h.textContent)).toEqual(['eval-2', 'train-1', 'sweep-3']);
    await waitFor(() => expect(announcer()).toHaveTextContent('Sorted by Chips, ascending.'));

    await user.click(within(table).getByRole('button', { name: 'Chips' }));
    expect(within(table).getByRole('columnheader', { name: 'Chips' })).toHaveAttribute('aria-sort', 'descending');
    expect(within(table).getByRole('columnheader', { name: 'Job' })).not.toHaveAttribute('aria-sort');
  });

  it('filters through the URL and announces how many jobs it shows', async () => {
    const calls = mockApi((url, method, body) =>
      url.pathname === '/api/v1/jobs' && url.searchParams.get('state') === 'queued'
        ? { body: { now: 3600, jobs: [jobs[2]] } }
        : baseRoutes(url, method, body),
    );
    renderAt('/jobs');
    await screen.findByRole('table', { name: 'Jobs' });
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'State' }), 'queued');
    await waitFor(() => expect(announcer()).toHaveTextContent('Showing 1 job.'));
    expect(calls.some((c) => c.path === '/api/v1/jobs?state=queued')).toBe(true);
    expect(screen.getAllByRole('rowheader').map((h) => h.textContent)).toEqual(['sweep-3']);
  });
});

describe('job page', () => {
  it('confirms before cancelling, then announces it and moves focus to the heading', async () => {
    let cancelled = false;
    const done = job({ state: 'cancelled', endedAt: 4000, endsAt: null, placement: null });
    const calls = mockApi((url, method, body) => {
      if (url.pathname === '/api/v1/jobs/j-1/cancel' && method === 'POST') {
        cancelled = true;
        return { body: done };
      }
      if (url.pathname === '/api/v1/jobs/j-1') return { body: cancelled ? done : job() };
      return baseRoutes(url, method, body);
    });
    renderAt('/jobs/j-1');
    const user = userEvent.setup();
    expect(await screen.findByText('2x2x1 at x 0, y 0, z 0')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Cancel job…' }));
    const dialog = screen.getByRole('dialog', { name: 'Cancel train-1?' });
    expect(dialog).toHaveAttribute('open');
    expect(dialog).toHaveAccessibleDescription('It stops now and frees its 4 chips. Its progress is lost.');
    await user.click(within(dialog).getByRole('button', { name: 'Keep job' }));
    expect(dialog).not.toHaveAttribute('open');
    expect(calls.some((c) => c.path.endsWith('/cancel'))).toBe(false);

    await user.click(screen.getByRole('button', { name: 'Cancel job…' }));
    await user.click(within(dialog).getByRole('button', { name: 'Cancel job' }));
    const heading = await screen.findByRole('heading', { level: 1, name: /Cancelled/ });
    expect(heading).toHaveFocus();
    expect(screen.queryByRole('button', { name: 'Cancel job…' })).not.toBeInTheDocument();
    await waitFor(() => expect(announcer()).toHaveTextContent('Cancelled train-1.'));
  });

  it('explains a queued job’s reservation', async () => {
    mockApi((url, method, body) =>
      url.pathname === '/api/v1/jobs/j-3'
        ? {
            body: {
              ...jobs[2],
              queuePosition: 1,
              reservation: { at: 7200, placement: { origin: { x: 0, y: 0, z: 1 }, size: { x: 2, y: 2, z: 1 } } },
              history: [{ at: 2700, state: 'queued', reason: 'submitted' }],
            },
          }
        : baseRoutes(url, method, body),
    );
    renderAt('/jobs/j-3');
    expect(await screen.findByText(/Starts by Day 1, 02:00 on 2x2x1 at x 0, y 0, z 1/)).toBeInTheDocument();
  });

  it('says when a job does not exist', async () => {
    mockApi(baseRoutes);
    renderAt('/jobs/j-404');
    expect(await screen.findByRole('heading', { level: 1, name: 'Job not found' })).toBeInTheDocument();
  });
});
