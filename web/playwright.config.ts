// The UX evidence run (`./run ux`): screenshots and axe against a goldie
// server that ./run ux starts and stops. Everything goes to UX_DIR, the `ux`
// CI artifact.
import { defineConfig, devices } from '@playwright/test';

const baseURL = process.env.GOLDIE_UX_URL;
if (!baseURL) {
  throw new Error('GOLDIE_UX_URL is not set: run the specs with ./run ux, which starts the server');
}
const out = process.env.UX_DIR ?? '../ux';

// The signed-in session cookie the setup project saves. It lives with the
// server's temporary files (./run ux sets GOLDIE_UX_STATE), never in the ux/
// artifact.
const state = process.env.GOLDIE_UX_STATE;
if (!state) {
  throw new Error('GOLDIE_UX_STATE is not set: run the specs with ./run ux, which keeps the session state in its own temporary directory');
}
export const statePath = state;
// The operator's session, saved by the same setup project for the `operator` state.
export const operatorStatePath = `${state}.operator`;
// The spec that runs once per auth state (#750), in the projects of that name.
const byState = /journeys\/by-state\.spec\.ts/;

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
    { name: 'chromium', testIgnore: [/auth\.setup\.ts/, byState], dependencies: ['setup'], use: { storageState: statePath } },
    // The journey steps once per auth state, on the one shared server.
    { name: 'anonymous', testMatch: byState, use: { storageState: { cookies: [], origins: [] } } },
    { name: 'reader', testMatch: byState, dependencies: ['setup'], use: { storageState: statePath } },
    { name: 'operator', testMatch: byState, dependencies: ['setup'], use: { storageState: operatorStatePath } },
  ],
});
