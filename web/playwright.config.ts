// The UX evidence run (`./run ux`): screenshots and axe against a goldie
// server that ./run ux starts and stops. Everything goes to UX_DIR, the `ux`
// CI artifact.
import { defineConfig, devices } from '@playwright/test';

const baseURL = process.env.GOLDIE_UX_URL;
if (!baseURL) {
  throw new Error('GOLDIE_UX_URL is not set: run the specs with ./run ux, which starts the server');
}
const out = process.env.UX_DIR ?? '../ux';

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
  projects: [{ name: 'chromium' }],
});
