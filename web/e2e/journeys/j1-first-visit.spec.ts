// Journey J1, first visit to sign in (#748, MVP): an anonymous visitor opens `/`
// and meets the standalone sign-in page, no shell around it; a reader token
// signs in and lands on Home with the shell. ./run ux runs it against the built
// binary on examples/knowledge. Each step leaves a shot at 375, 768 and 1280 px
// in light and dark: ux/journeys/j1-<step>-<width>-<theme>.png.
import { expect, test } from '@playwright/test';
import { eachViewport, evidence, expectNavCollapsed } from '../ux';

const token = process.env.GOLDIE_UX_TOKEN ?? '';

eachViewport((width, theme) => {
  test.use({ storageState: { cookies: [], origins: [] } });

  test('J1 an anonymous visitor signs in and lands on Home with the shell', async ({ page }) => {
    await test.step('open / signed out: the standalone sign-in page', async () => {
      await page.goto('/');
      await expect(page).toHaveURL((u) => u.pathname === '/signin');
      await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in');
      await expect(page.getByRole('main')).toHaveCount(1);
      await expect(page.getByRole('navigation'), 'no navigation landmark outside the shell').toHaveCount(0);
      await expect(page.locator('nav')).toHaveCount(0);
      await expect(page.locator('header').getByRole('button', { name: /menu/i })).toHaveCount(0);
      await evidence(page, width, theme, '', '../journeys/j1-signin');
    });

    await test.step('sign in with a reader token: Home with the shell', async () => {
      expect(token, 'GOLDIE_UX_TOKEN is set by ./run ux').not.toBe('');
      await page.getByLabel(/token/i).fill(token);
      await page.getByRole('button', { name: /sign in/i }).click();
      await expect(page).toHaveURL((u) => u.pathname === '/' && u.search === '');
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('What are you deciding today?');
      await expect(page.getByRole('main')).toHaveCount(1);
      await expectNavCollapsed(page, width);
      await evidence(page, width, theme, '', '../journeys/j1-home');
    });
  });
});
