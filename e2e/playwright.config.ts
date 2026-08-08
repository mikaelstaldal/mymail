import { defineConfig } from '@playwright/test';

// 8090, not 8080: the default MyMail server port is what a developer is most
// likely to already have running, and test-e2e.sh refuses to start when the port
// is taken. MyCal owns 8089 and MyNotes 8091, so the three sibling suites can
// run at the same time without fighting over a port.
const port = 8090;

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  workers: 1,
  reporter: 'list',
  use: {
    baseURL: `http://localhost:${port}`,
    // `on-first-retry` captures nothing while retries are 0 — a pairing MyCal's
    // config shipped with for a while, so a CI failure there left only the list
    // reporter's text behind. These assertions are geometry ("expected 8,
    // received 9.5"), which is near-undebuggable without a trace, and the workflow
    // step puts this suite before publication. (That step has run since 2026-08-08,
    // so these artefacts are what a red CI run leaves behind for whoever has to
    // diagnose it without reproducing locally.)
    // Retries stay at 0: a flaky gate trains people to re-run red builds, and the
    // first real failure gets re-run with them.
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { browserName: 'chromium' },
    },
  ],
});
