// The sign-in setup (#495): signs in once with the throwaway token ./run ux
// exports as GOLDIE_UX_TOKEN and saves the session cookie as storageState, so
// every other spec starts signed in. Specs about signing in override it with
// an empty state.
import { expect, test as setup } from '@playwright/test';
import { statePath } from '../playwright.config';

setup('sign in with the throwaway token', async ({ request, baseURL }) => {
  const token = process.env.GOLDIE_UX_TOKEN ?? '';
  expect(token, 'GOLDIE_UX_TOKEN is set by ./run ux').not.toBe('');
  const res = await request.post('/api/v1/session', { data: { token }, headers: { Origin: new URL(baseURL ?? '').origin } });
  expect(res.status()).toBe(200);
  await request.storageState({ path: statePath });
});
