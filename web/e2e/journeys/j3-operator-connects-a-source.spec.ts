// Journey J3, an operator connects a Source (#748, MVP, handoff §3.6, §3.7): the
// Sources page lists the configured Source with its sync status; after a
// triggered sync it shows the new sync result; a search run as a reader then
// appears in the Activity feed (the feed records requests; a sync is not a
// request). The operator signs in with GOLDIE_UX_OPERATOR_TOKEN, which ./run ux
// puts in the server's token file; the reader is the throwaway ux session.
// Shots: ux/journeys/j3-<step>-<width>-<theme>.png.
import { expect, test, type Page } from '@playwright/test';
import { eachViewport, evidence, problems, withNav } from '../ux';

const operatorToken = process.env.GOLDIE_UX_OPERATOR_TOKEN ?? '';
const readerState = process.env.GOLDIE_UX_STATE ?? '';

const main = (page: Page) => page.getByRole('main');
const h1 = (page: Page) => page.getByRole('heading', { level: 1 });
const rows = (page: Page) => main(page).getByRole('listitem');
const field = (page: Page) => main(page).locator('input[type="search"]');
const resync = (page: Page) => main(page).getByRole('button', { name: /^(resync|syncing)/i });
const lastPoll = (page: Page) => rows(page).first().locator('dt:text-is("Last poll") + dd');
const lastError = (page: Page) => rows(page).first().locator('dt:text-is("Last error") + dd');

// The Sources page's time format: UTC, medium date, short time.
const shown = (iso: string) =>
  `${new Date(iso).toLocaleString('en-GB', { dateStyle: 'medium', timeStyle: 'short', timeZone: 'UTC' })} UTC`.replace(/\s+/g, ' ');
const text = async (loc: ReturnType<Page['locator']>) => ((await loc.textContent()) ?? '').replace(/\s+/g, ' ').trim();

async function lastPollOf(page: Page): Promise<string> {
  const res = await page.request.get('/api/v1/sync');
  expect(res.status()).toBe(200);
  const body = (await res.json()) as { sources: { last_poll?: string }[] };
  expect(body.sources).toHaveLength(1);
  return body.sources[0].last_poll ?? '';
}

eachViewport((width, theme) => {
  test.use({ storageState: { cookies: [], origins: [] } });

  test('J3 an operator syncs the Source and a reader search shows in Activity', async ({ page, browser, baseURL }) => {
    const seen = await problems(page, baseURL ?? '');
    const shot = (step: string) => evidence(page, width, theme, '', `../journeys/j3-${step}`);
    const query = `journey probe ${width} ${theme} ${Date.now()}`;

    await test.step('sign in as the operator', async () => {
      expect(operatorToken, 'GOLDIE_UX_OPERATOR_TOKEN is set by ./run ux').not.toBe('');
      await page.goto('/signin');
      await page.getByLabel(/token/i).fill(operatorToken);
      await page.getByRole('button', { name: /sign in/i }).click();
      await expect(h1(page)).toHaveText('What are you deciding today?');
    });

    await test.step('the Sources page lists the configured Source with its sync status', async () => {
      const nav = await withNav(page);
      await nav.getByRole('link', { name: /^Sources/ }).click();
      await expect(page).toHaveURL((u) => u.pathname === '/ops/sources');
      await expect(h1(page)).toHaveText('Sources');
      await expect(rows(page)).toHaveCount(1);
      await expect(rows(page).first()).toContainText('Last poll');
      await expect(rows(page).first()).toContainText('Last success');
      await expect(lastError(page)).toHaveText('None');
      await expect(rows(page).first().getByText('Failing')).toHaveCount(0);
      await expect(lastPoll(page)).not.toHaveText('No poll has finished yet.');
      await shot('sources');
    });

    await test.step('a triggered sync shows the new sync result', async () => {
      const before = await lastPollOf(page);
      await resync(page).click();
      await expect(resync(page)).toHaveText(/syncing/i);
      await expect(page.getByText('Sync finished. Sources updated.')).toBeAttached({ timeout: 30_000 });
      await expect(resync(page)).toHaveText('Resync');
      const after = await lastPollOf(page);
      expect(after, 'the Source was polled again').not.toBe(before);
      expect(await text(lastPoll(page)), 'the page shows the new poll').toBe(shown(after));
      await expect(lastError(page)).toHaveText('None');
      await expect(rows(page).first().getByText('Failing')).toHaveCount(0);
      await shot('synced');
    });

    await test.step('a reader searches', async () => {
      const ctx = await browser.newContext({ baseURL, storageState: readerState });
      try {
        const reader = await ctx.newPage();
        await reader.goto('/');
        await field(reader).fill(query);
        await field(reader).press('Enter');
        await expect(main(reader).getByRole('status').filter({ hasText: /No golden path/ })).toBeVisible();
      } finally {
        await ctx.close();
      }
    });

    await test.step('the search appears in the Activity feed', async () => {
      const nav = await withNav(page);
      await nav.getByRole('link', { name: /^Activity/ }).click();
      await expect(page).toHaveURL((u) => u.pathname === '/ops/activity');
      await expect(h1(page)).toHaveText('Activity');
      const row = main(page).getByRole('row').filter({ hasText: query });
      await expect(row).toHaveCount(1);
      await expect(row).toContainText('search');
      await expect(row).toContainText(`Miss · No results for ${query}`);
      await shot('activity');
    });

    expect(seen.external, 'no request to another origin').toEqual([]);
    expect(seen.failed, 'no failed request').toEqual([]);
    expect(seen.errors, 'no page error or CSP violation').toEqual([]);
  });
});
