// Journey J2, a reader browses and searches (#748, MVP, handoff §3.2 to §3.4,
// §5): from Home browse to an Item's detail and back; search a term from the
// examples, see the hit cards and open one; an empty query and a no-hit query
// show their states. The reader is the throwaway ux token the storageState
// setup signed in. Shots: ux/journeys/j2-<step>-<width>-<theme>.png.
import { expect, test, type Page } from '@playwright/test';
import { eachViewport, evidence, problems } from '../ux';

const main = (page: Page) => page.getByRole('main');
const h1 = (page: Page) => page.getByRole('heading', { level: 1 });
const field = (page: Page) => main(page).locator('input[type="search"]');
const hits = (page: Page) => main(page).getByRole('list').getByRole('listitem');
const miss = (page: Page) => main(page).getByRole('status').filter({ hasText: /No golden path/ });
const article = 'Autonomous agent loops';
const home = 'What are you deciding today?';

eachViewport((width, theme) => {
  test('J2 a reader browses to an Item and back, then searches', async ({ page, baseURL }) => {
    const seen = await problems(page, baseURL ?? '');
    const shot = (step: string) => evidence(page, width, theme, '', `../journeys/j2-${step}`);

    await test.step('Home offers the Browse tiles', async () => {
      await page.goto('/');
      await expect(h1(page)).toHaveText(home);
      await expect(main(page).getByRole('link', { name: /^Articles/ })).toBeVisible();
      await shot('home');
    });

    await test.step('browse: the Articles tile lists the articles', async () => {
      await main(page).getByRole('link', { name: /^Articles/ }).click();
      await expect(page).toHaveURL((u) => u.pathname === '/articles');
      await expect(h1(page)).toHaveText('Articles');
      await expect(main(page).getByRole('link', { name: article })).toBeVisible();
      await shot('articles');
    });

    await test.step('browse: an article opens its Item detail', async () => {
      await main(page).getByRole('link', { name: article }).click();
      await expect(page).toHaveURL((u) => u.pathname === '/items/article/agent-loops');
      await expect(h1(page)).toHaveText(article);
      await shot('item');
    });

    await test.step('back goes to Home', async () => {
      await page.getByRole('link', { name: /Back to home/ }).click();
      await expect(page).toHaveURL((u) => u.pathname === '/' && u.search === '');
      await expect(h1(page)).toHaveText(home);
    });

    await test.step('search a term from the examples: hit cards', async () => {
      await field(page).fill('loops');
      await field(page).press('Enter');
      await expect(page).toHaveURL((u) => u.pathname === '/' && u.searchParams.get('q') === 'loops');
      await expect(hits(page)).toHaveCount(1);
      await expect(hits(page).first()).toContainText(article);
      await expect(hits(page).first()).toContainText('article');
      await expect(miss(page)).toHaveCount(0);
      await shot('hits');
    });

    await test.step('open a hit: the Item, with the way back to the search', async () => {
      await hits(page).first().getByRole('link').click();
      await expect(page).toHaveURL((u) => u.pathname === '/items/article/agent-loops');
      await expect(h1(page)).toHaveText(article);
      await page.getByRole('link', { name: /Back to search/ }).click();
      await expect(page).toHaveURL((u) => u.pathname === '/' && u.searchParams.get('q') === 'loops');
      await expect(hits(page)).toHaveCount(1);
    });

    await test.step('an empty query shows Home, no hits and no Miss', async () => {
      await page.getByRole('button', { name: /clear/i }).click();
      await expect(field(page)).toHaveValue('');
      await field(page).press('Enter');
      await expect(page).toHaveURL((u) => u.pathname === '/' && u.search === '');
      await expect(h1(page)).toHaveText(home);
      await expect(hits(page)).toHaveCount(0);
      await expect(miss(page)).toHaveCount(0);
      await expect(main(page).getByRole('link', { name: /^Articles/ })).toBeVisible();
      await shot('empty-query');
    });

    await test.step('a query with no hit shows the Miss panel', async () => {
      await field(page).fill('zzqx');
      await field(page).press('Enter');
      await expect(miss(page)).toContainText('No golden path for “zzqx” yet.');
      await expect(hits(page)).toHaveCount(0);
      await shot('no-hit');
    });

    expect(seen.external, 'no request to another origin').toEqual([]);
    expect(seen.failed, 'no failed request').toEqual([]);
    expect(seen.errors, 'no page error or CSP violation').toEqual([]);
  });
});
