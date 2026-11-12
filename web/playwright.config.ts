import { defineConfig, devices } from '@playwright/test';

// The browser tests run against real `slicewise` servers serving the built console: one with an
// empty pod for the scripted flows, and one running the demo workload for a busy, colorful pod.
const bin = process.env.SLICEWISE_BIN ?? '../slicewise';
const channel = process.env.PW_CHANNEL;

export default defineConfig({
  testDir: 'e2e',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
  use: {
    baseURL: 'http://127.0.0.1:8080',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'], ...(channel ? { channel } : {}) } }],
  webServer: [
    {
      command: `${bin} -addr 127.0.0.1:8080 -demo=false -speed 60`,
      url: 'http://127.0.0.1:8080/healthz',
      reuseExistingServer: !process.env.CI,
      timeout: 30_000,
    },
    {
      command: `${bin} -addr 127.0.0.1:8081 -demo=true -speed 30 -seed 7`,
      url: 'http://127.0.0.1:8081/healthz',
      reuseExistingServer: !process.env.CI,
      timeout: 30_000,
    },
  ],
});
