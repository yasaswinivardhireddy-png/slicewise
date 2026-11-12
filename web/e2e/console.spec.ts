import AxeBuilder from '@axe-core/playwright';
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';

const WCAG_22_AA = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'];
const SHOTS = process.env.SCREENSHOT_DIR;

/** Fails on any WCAG 2.2 A or AA violation that axe-core can detect, color contrast included. */
async function expectAccessible(page: Page) {
  const { violations } = await new AxeBuilder({ page }).withTags(WCAG_22_AA).analyze();
  expect(violations.map((v) => `${v.id} (${v.nodes.length}): ${v.help} at ${v.nodes[0]?.target.join(' ')}`)).toEqual([]);
}

let counter = 0;
const unique = (prefix: string) => `${prefix}-${Date.now() % 100_000}-${counter++}`;

interface CreatedJob {
  id: string;
  name: string;
  state: string;
  placement: { origin: { x: number; y: number; z: number } } | null;
}

async function createJob(request: APIRequestContext, overrides: Record<string, unknown> = {}): Promise<CreatedJob> {
  const res = await request.post('/api/v1/jobs', {
    data: { name: unique('e2e'), tenant: 'research', priority: 'batch', shape: '2x2x1', durationMinutes: 6000, ...overrides },
  });
  expect(res.status()).toBe(201);
  return (await res.json()) as CreatedJob;
}

/** What a screen reader would call the focused element: its aria-label or its text. */
const focusedName = (page: Page) =>
  page.evaluate(() => {
    const el = document.activeElement as HTMLElement | null;
    return el?.getAttribute('aria-label') ?? el?.textContent?.trim() ?? '';
  });

/** Presses Tab until focus reaches an element with this name, proving it is reachable by keyboard. */
async function tabTo(page: Page, name: string | RegExp, max = 60) {
  for (let i = 0; i < max; i++) {
    await page.keyboard.press('Tab');
    const current = await focusedName(page);
    if (typeof name === 'string' ? current === name : name.test(current)) return;
  }
  throw new Error(`Tab never reached ${String(name)}`);
}

async function settled(page: Page) {
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  await expect(page.getByText(/^Loading/)).toHaveCount(0);
}

test('every page passes axe WCAG 2.2 AA checks, including color contrast', async ({ page, request }) => {
  const job = await createJob(request);
  for (const path of ['/', '/jobs', '/jobs/new', `/jobs/${job.id}`, '/simulator', '/no-such-page']) {
    await page.goto(path);
    await settled(page);
    await expectAccessible(page);
  }

  await page.goto('/jobs/new');
  await settled(page);
  await page.getByRole('button', { name: 'Submit job' }).click();
  await expect(page.getByRole('region', { name: /problems/ })).toBeFocused();
  await expectAccessible(page);
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/form-errors.png`, fullPage: true });

  await page.goto(`/jobs/${job.id}`);
  await settled(page);
  await page.getByRole('button', { name: 'Cancel job…' }).click();
  await expect(page.getByRole('dialog', { name: `Cancel ${job.name}?` })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Keep job' })).toBeFocused();
  await expectAccessible(page);
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toBeHidden();
  await expect(page.getByRole('button', { name: 'Cancel job…' })).toBeFocused();
});

test('keyboard only: skip link, submit a job, find its chips on the pod, cancel it', async ({ page, request }) => {
  await page.goto('/');
  await settled(page);
  await page.keyboard.press('Tab');
  await expect(page.getByRole('link', { name: 'Skip to main content' })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.locator('main')).toBeFocused();

  // Reach the form through the navigation, tabbing from the top of a fresh page.
  await page.goto('/');
  await settled(page);
  await tabTo(page, 'Submit a job');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('heading', { level: 1, name: 'Submit a job' })).toBeFocused();

  const name = unique('kbd');
  await page.keyboard.press('Tab');
  await expect(page.getByRole('textbox', { name: 'Job name' })).toBeFocused();
  await page.keyboard.type(name);
  await page.keyboard.press('Tab');
  await expect(page.getByRole('combobox', { name: 'Team' })).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Tab');
  await expect(page.getByRole('radio', { name: 'Production' })).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(page.getByRole('radio', { name: 'Batch' })).toBeChecked();
  await page.keyboard.press('Tab');
  await expect(page.getByRole('combobox', { name: 'Slice shape' })).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Tab');
  await expect(page.getByRole('textbox', { name: 'Run time in minutes' })).toBeFocused();
  await page.keyboard.press('ControlOrMeta+A');
  await page.keyboard.type('6000');
  await page.keyboard.press('Enter');

  const heading = page.getByRole('heading', { level: 1, name: new RegExp(name) });
  await expect(heading).toBeFocused();
  await expect(page.getByTestId('announcer')).toHaveText(`Submitted ${name}. It is running.`);

  // Find the job on the pod with the arrow keys.
  const id = new URL(page.url()).pathname.split('/').pop() ?? '';
  const job = (await (await request.get(`/api/v1/jobs/${id}`)).json()) as CreatedJob;
  expect(job.placement).not.toBeNull();
  await page.goto('/');
  await settled(page);
  await tabTo(page, /^x 0, y 0, z 0:/);
  const { x, y, z } = job.placement!.origin;
  for (let i = 0; i < x; i++) await page.keyboard.press('ArrowRight');
  for (let i = 0; i < y; i++) await page.keyboard.press('ArrowDown');
  for (let i = 0; i < z; i++) await page.keyboard.press('PageDown');
  expect(await focusedName(page)).toContain(name);
  await page.keyboard.press('Enter');
  await expect(heading).toBeFocused();

  // Cancel it through the confirmation dialog.
  await tabTo(page, 'Cancel job…');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('button', { name: 'Keep job' })).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(page.getByRole('button', { name: 'Cancel job', exact: true })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('heading', { level: 1, name: /Cancelled/ })).toBeFocused();
  await expect(page.getByTestId('announcer')).toHaveText(`Cancelled ${name}.`);
});

test('the simulator compares the policies, and its results pass axe', async ({ page }) => {
  await page.goto('/simulator');
  await settled(page);
  await page.getByRole('textbox', { name: 'Number of jobs' }).fill('400');
  await page.getByRole('button', { name: 'Run simulation' }).click();
  const table = page.getByRole('table', { name: 'Policy comparison' });
  await expect(table.getByRole('row')).toHaveCount(6);
  await expect(page.getByTestId('announcer')).toContainText('Simulation finished');
  await expectAccessible(page);
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/simulator.png`, fullPage: true });
});

test('pages reflow at 320 CSS pixels without horizontal scrolling (WCAG 1.4.10)', async ({ page, request }) => {
  const job = await createJob(request);
  await page.setViewportSize({ width: 320, height: 720 });
  for (const path of ['/', '/jobs', '/jobs/new', `/jobs/${job.id}`, '/simulator']) {
    await page.goto(path);
    await settled(page);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow, `${path} scrolls sideways by ${overflow}px`).toBeLessThanOrEqual(0);
  }
});

test('honors reduced motion and forced colors (Windows high contrast)', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('/jobs/new');
  await settled(page);
  const button = page.getByRole('button', { name: 'Submit job' });
  expect(await button.evaluate((el) => getComputedStyle(el).transitionDuration)).toBe('0s');

  await page.emulateMedia({ reducedMotion: 'reduce', forcedColors: 'active' });
  await page.goto('/');
  await settled(page);
  await page.keyboard.press('Tab');
  await page.keyboard.press('Tab');
  expect(await page.evaluate(() => getComputedStyle(document.activeElement!).outlineStyle)).not.toBe('none');
  await expectAccessible(page);
});

test('changes made elsewhere appear without a reload and can be announced', async ({ page, request }) => {
  await page.goto('/jobs');
  await settled(page);
  await expect(page.getByText('Live', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Announce updates' }).click();
  await expect(page.getByRole('button', { name: 'Announce updates' })).toHaveAttribute('aria-pressed', 'true');
  const job = await createJob(request, { name: unique('live') });
  await expect(page.getByRole('link', { name: job.name })).toBeVisible();
  await expect(page.getByTestId('announcer')).toContainText(/^Update: .*started/, { timeout: 15_000 });
});

test.describe('busy demo pod', () => {
  test.use({ baseURL: 'http://127.0.0.1:8081', viewport: { width: 1280, height: 900 } });

  test('passes axe with every job color in use', async ({ page }) => {
    test.setTimeout(120_000);
    await page.goto('/');
    await settled(page);
    await expect.poll(() => page.locator('.cell.busy').count()).toBeGreaterThan(100);
    // Jobs keep starting, so within a short wait every one of the 8 job colors is on screen.
    await expect
      .poll(() => page.locator('.cell.busy').evaluateAll((cells) => new Set(cells.map((c) => c.className)).size), { timeout: 45_000 })
      .toBe(8);
    await expectAccessible(page);
    if (SHOTS) {
      await page.screenshot({ path: `${SHOTS}/pod.png`, fullPage: true });
      await page.goto('/jobs');
      await settled(page);
      await page.screenshot({ path: `${SHOTS}/jobs.png` });
      await page.goto('/jobs?state=queued');
      await settled(page);
      const queued = page.getByRole('table', { name: 'Jobs' }).getByRole('link').first();
      if (await queued.count()) {
        await queued.click();
        await settled(page);
        await page.screenshot({ path: `${SHOTS}/job.png`, fullPage: true });
      }
    }
  });
});
