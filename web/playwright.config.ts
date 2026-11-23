// The UX evidence run (`./run ux`): screenshots and axe against a goldie
// server that ./run ux starts and stops. Everything goes to UX_DIR, the `ux`
// CI artifact.
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { defineConfig, devices } from '@playwright/test';

const baseURL = process.env.GOLDIE_UX_URL;
if (!baseURL) {
  throw new Error('GOLDIE_UX_URL is not set: run the specs with ./run ux, which starts the server');
}
const out = process.env.UX_DIR ?? '../ux';

// The signed-in session cookie the setup project saves. It lives with the
// server's temporary files (./run ux sets GOLDIE_UX_STATE), never in the ux/
// artifact.
export const statePath = process.env.GOLDIE_UX_STATE ?? join(tmpdir(), 'goldie-ux-state.json');

export default defineConfig({
  testDir: './e2e',
  outputDir: `${out}/results`,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: [['list'], ['html', { outputFolder: `${out}/report`, open: 'never' }]],
  use: {
    ...devices['Desktop Chrome'],
    baseURL,
    // Deterministic shots: no motion, and the theme comes from emulation.
    reducedMotion: 'reduce',
    trace: 'retain-on-failure',
  },
  projects: [
    { name: 'setup', testMatch: /auth\.setup\.ts/ },
    { name: 'chromium', testIgnore: /auth\.setup\.ts/, dependencies: ['setup'], use: { storageState: statePath } },
  ],
});
