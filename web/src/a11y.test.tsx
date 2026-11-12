import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { baseRoutes, axeViolations, mockApi, renderAt } from './test/helpers.tsx';

/** Waits until the page has its heading and no "Loading…" messages. */
async function settled() {
  await screen.findByRole('heading', { level: 1 });
  await waitFor(() => expect(screen.queryByText(/^Loading/)).not.toBeInTheDocument());
}

describe('axe-core finds no accessibility violations', () => {
  it.each(['/', '/jobs', '/jobs/new', '/jobs/j-1', '/jobs/j-3', '/simulator', '/no-such-page'])('on %s', async (path) => {
    mockApi(baseRoutes);
    renderAt(path);
    await settled();
    expect(await axeViolations()).toEqual([]);
  });

  it('on the submit form showing errors', async () => {
    mockApi(baseRoutes);
    renderAt('/jobs/new');
    await settled();
    await userEvent.click(screen.getByRole('button', { name: 'Submit job' }));
    await screen.findByRole('region', { name: /problems/ });
    expect(await axeViolations()).toEqual([]);
  });

  it('with the cancel dialog open', async () => {
    mockApi(baseRoutes);
    renderAt('/jobs/j-1');
    await settled();
    await userEvent.click(await screen.findByRole('button', { name: 'Cancel job…' }));
    expect(await axeViolations()).toEqual([]);
  });
});
