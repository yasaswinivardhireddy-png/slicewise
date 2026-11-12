import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { baseRoutes, mockApi, renderAt } from './test/helpers.tsx';

describe('pod grid', () => {
  it('names every chip and moves one tab stop with the arrow, Home, End and Page keys', async () => {
    mockApi(baseRoutes);
    renderAt('/');
    const layer0 = await screen.findByRole('grid', { name: 'Layer z = 0' });
    expect(screen.getByRole('grid', { name: 'Layer z = 1' })).toBeInTheDocument();
    const cells = within(layer0).getAllByRole('gridcell');
    expect(cells).toHaveLength(8);
    const first = cells[0]!;
    expect(first).toHaveAccessibleName('x 0, y 0, z 0: job 1, train-1, research, production priority');
    expect(first).toHaveAttribute('tabindex', '0');
    expect(cells[1]).toHaveAttribute('tabindex', '-1');

    const user = userEvent.setup();
    first.focus();
    await user.keyboard('{ArrowRight}{ArrowRight}');
    expect(document.activeElement).toHaveAccessibleName('x 2, y 0, z 0: job 2, eval-2, serving, batch priority');
    await user.keyboard('{End}{ArrowRight}');
    expect(document.activeElement).toHaveAccessibleName('x 3, y 0, z 0: idle');
    await user.keyboard('{PageDown}');
    expect(document.activeElement).toHaveAccessibleName('x 3, y 0, z 1: idle');
    await user.keyboard('{Control>}{Home}{/Control}');
    expect(document.activeElement).toHaveAccessibleName('x 0, y 0, z 1: idle');
    await user.keyboard('{PageUp}{ArrowDown}');
    expect(document.activeElement).toHaveAccessibleName('x 0, y 1, z 0: job 1, train-1, research, production priority');
    expect(screen.getAllByRole('gridcell').filter((c) => c.tabIndex === 0)).toHaveLength(1);
    expect(screen.getByText(/Chip at x 0, y 1, z 0/)).toHaveTextContent('train-1');

    await user.keyboard('{Enter}');
    expect(await screen.findByRole('heading', { level: 1, name: /train-1/ })).toBeInTheDocument();
  });

  it('lists running jobs and summarizes the pod', async () => {
    mockApi(baseRoutes);
    renderAt('/');
    const table = await screen.findByRole('table', { name: 'Running jobs and where they run' });
    expect(within(table).getByRole('link', { name: 'eval-2' })).toHaveAttribute('href', '/jobs/j-2');
    expect(within(table).getByText('1x2x1 at x 2, y 0, z 0')).toBeInTheDocument();
    expect(screen.getByText('6 of 16 (37.5%)')).toBeInTheDocument();
    expect(screen.getByText('Day 1, 01:00 (60× real time)')).toBeInTheDocument();
  });

  it('remembers whether to announce background updates', async () => {
    mockApi(baseRoutes);
    renderAt('/');
    const toggle = screen.getByRole('button', { name: 'Announce updates' });
    expect(toggle).toHaveAttribute('aria-pressed', 'false');
    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-pressed', 'true');
    expect(localStorage.getItem('slicewise.announceUpdates')).toBe('on');
  });
});
