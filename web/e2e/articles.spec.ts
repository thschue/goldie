// The article index at /articles (#724, handoff §3.2): the articles of the selected Project from
// GET /api/v1/articles?project= as cards. The API is stubbed per test (page.route) so every state
// is deterministic.
//
// Contract these specs pin down for the implementation:
// - a card is a role=listitem in <main>; its name is a level 2 heading; the card shows the id, a
//   Scope badge and a lifecycle badge as text ("published" when the API sends no status, else the
//   status: deprecated or draft);
// - a card holds a link to /items/article/<id> with the id as one encoded segment (ADR 0016);
// - an article that shadows another shows one "Overrides <id> from <scope>" line per Scope in
//   `overrides`, and nothing when `overrides` is empty;
// - the empty state is a role=status saying "No articles for <project> yet."; an error is a
//   role=alert holding the envelope's hint;
// - every string from a Source is text: invisible and bidi controls are stripped, markup is inert.
import { expect, test, type Page, type Route } from '@playwright/test';
import { eachViewport, evidence } from './ux';

const shop = 'github.com/acme/shop';
const apiProjects = /\/api\/v1\/projects$/;
const apiArticles = /\/api\/v1\/articles(\?.*)?$/;

type Article = { id: string; name: string; scope?: string; status?: 'deprecated' | 'draft'; overrides?: string[]; enforced?: boolean };

const article = (a: Article) => ({ keywords: [], scope: 'global', overrides: [], enforced: false, ...a });

const json = (route: Route, status: number, body: unknown) =>
  route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

const envelope = (code: string, message: string, hint: string) => ({ error: { code, message, details: { hint } } });

const stubProjects = (page: Page) => page.route(apiProjects, (route) => json(route, 200, { projects: [shop] }));

// stubArticles answers the list and records every request URL.
const stubArticles = async (page: Page, articles: Article[]) => {
  const urls: URL[] = [];
  await page.route(apiArticles, (route) => {
    urls.push(new URL(route.request().url()));
    return json(route, 200, { articles: articles.map(article) });
  });
  return urls;
};

const sample: Article[] = [
  { id: 'platform/deploy', name: 'Deploying to Kubernetes', scope: 'global' },
  { id: 'retry', name: 'Retry budgets', scope: shop, overrides: ['global'] },
  { id: 'soap', name: 'Exposing a SOAP API', scope: 'global', status: 'deprecated' },
];

const main = (page: Page) => page.getByRole('main');
const cards = (page: Page) => main(page).getByRole('listitem');
const card = (page: Page, name: string) => cards(page).filter({ has: page.getByRole('heading', { level: 2, name, exact: true }) });
const open = (page: Page, project = shop) => page.goto(`/articles?project=${encodeURIComponent(project)}`);

eachViewport((width, theme) => {
  test('the article index is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await stubArticles(page, sample);
    await open(page);
    await expect(cards(page)).toHaveCount(3);
    await evidence(page, width, theme, '', 'articles');
  });

  test('the article index error state is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiArticles, (route) =>
      json(route, 500, envelope('internal', 'The article store is unavailable.', 'Try again in a moment.')),
    );
    await open(page);
    await expect(main(page).getByRole('alert')).toBeVisible();
    await evidence(page, width, theme, '-error', 'articles');
  });

  test('the article index empty state is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await stubArticles(page, []);
    await open(page);
    await expect(main(page).getByRole('status')).toBeVisible();
    await evidence(page, width, theme, '-empty', 'articles');
  });
});

test.describe('the article index', () => {
  test('lists the articles of the Project as cards with name, id, Scope and lifecycle', async ({ page }) => {
    await stubProjects(page);
    const urls = await stubArticles(page, sample);
    await open(page);
    await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
    await expect(cards(page)).toHaveCount(3);

    const deploy = card(page, 'Deploying to Kubernetes');
    await expect(deploy).toContainText('platform/deploy');
    await expect(deploy).toContainText('global');
    await expect(deploy).toContainText('published');

    const retry = card(page, 'Retry budgets');
    await expect(retry).toContainText('retry');
    await expect(retry).toContainText(shop);
    await expect(retry).toContainText('published');

    await expect(card(page, 'Exposing a SOAP API')).toContainText('deprecated');
    await expect(card(page, 'Exposing a SOAP API')).not.toContainText('published');

    await expect(main(page).getByRole('alert')).toHaveCount(0);
    await expect(main(page).getByRole('status')).toHaveCount(0);
    expect(urls.length).toBeGreaterThan(0);
    expect(urls.every((u) => u.searchParams.get('project') === shop)).toBe(true);
  });

  test('an article that shadows another says so, one line per Scope, and one that does not says nothing', async ({ page }) => {
    await stubProjects(page);
    await stubArticles(page, [
      { id: 'retry', name: 'Retry budgets', scope: shop, overrides: ['global'] },
      { id: 'logging', name: 'Logging', scope: 'local', overrides: [shop, 'global'] },
      { id: 'plain', name: 'Plain one', scope: 'global', overrides: [] },
    ]);
    await open(page);
    await expect(cards(page)).toHaveCount(3);
    await expect(card(page, 'Retry budgets')).toContainText(/Overrides\s+\S*retry\S*\s+from global/);
    await expect(card(page, 'Logging')).toContainText(/Overrides\s+\S*logging\S*\s+from global/);
    await expect(card(page, 'Logging')).toContainText(new RegExp(`Overrides\\s+\\S*logging\\S*\\s+from ${shop}`));
    await expect(card(page, 'Plain one')).not.toContainText('Overrides');
  });

  test('a card links to /items/article/<id> with a nested id as one encoded segment', async ({ page }) => {
    await stubProjects(page);
    await stubArticles(page, [
      { id: 'platform/deploy', name: 'Deploying to Kubernetes' },
      { id: 'retry', name: 'Retry budgets' },
      { id: 'a b?c#d%2Fe', name: 'Hostile id' },
    ]);
    await open(page);
    await expect(cards(page)).toHaveCount(3);
    const path = async (name: string) =>
      new URL((await card(page, name).locator('a[href]').first().getAttribute('href')) ?? '', 'http://x').pathname;
    expect(await path('Deploying to Kubernetes')).toBe('/items/article/platform%2Fdeploy');
    expect(await path('Retry budgets')).toBe('/items/article/retry');
    expect(await path('Hostile id')).toBe('/items/article/a%20b%3Fc%23d%252Fe');
  });

  test('following a card opens the article page', async ({ page }) => {
    await stubProjects(page);
    await stubArticles(page, [{ id: 'platform/deploy', name: 'Deploying to Kubernetes' }]);
    await page.route(/\/api\/v1\/articles\/platform%2Fdeploy$/, (route) =>
      json(route, 200, { ...article({ id: 'platform/deploy', name: 'Deploying to Kubernetes' }), content: 'Use the chart.' }),
    );
    await open(page);
    await card(page, 'Deploying to Kubernetes').locator('a[href]').first().click();
    await expect(page).toHaveURL((u) => u.pathname === '/items/article/platform%2Fdeploy');
  });

  test('an empty list says "No articles for <project> yet." as a status, with no error and no cards', async ({ page }) => {
    await stubProjects(page);
    await stubArticles(page, []);
    await open(page);
    await expect(main(page).getByRole('status')).toContainText(`No articles for ${shop} yet.`);
    await expect(main(page).getByRole('alert')).toHaveCount(0);
    await expect(cards(page)).toHaveCount(0);
  });

  test('with no Project in the URL the global list is requested and shown (#748)', async ({ page }) => {
    await stubProjects(page);
    const urls = await stubArticles(page, [{ id: 'platform/deploy', name: 'Deploying to Kubernetes' }]);
    await page.goto('/articles');
    await expect(cards(page)).toHaveCount(1);
    expect(urls.map((u) => u.search)).toEqual(['']);
  });

  test('a failing list shows the standard error with its hint, and no list', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiArticles, (route) =>
      json(route, 503, envelope('not_ready', 'The article store is unavailable.', 'Try again in a moment, or ask the platform team.')),
    );
    await open(page);
    await expect(main(page).getByRole('alert')).toContainText('Try again in a moment, or ask the platform team.');
    await expect(cards(page)).toHaveCount(0);
    await expect(main(page).getByRole('status')).toHaveCount(0);
  });

  test('an error never shows more than the envelope: no stack, path or address', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiArticles, (route) =>
      route.fulfill({ status: 502, contentType: 'text/html', body: '<pre>panic at /srv/goldie/x.go:12 10.0.0.7:8080</pre>' }),
    );
    await open(page);
    const alert = main(page).getByRole('alert');
    await expect(alert).toBeVisible();
    await expect(alert).not.toContainText('panic');
    await expect(alert).not.toContainText('10.0.0.7');
    await expect(alert).not.toContainText('/srv/');
  });

  test('Source text is plain: markup is inert and invisible and bidi controls are stripped', async ({ page }) => {
    await stubProjects(page);
    await stubArticles(page, [
      { id: 'x', name: '<img src=x onerror="window.__pwn=1"> **bold** Ret‮ry​ budgets⁦﻿' },
      { id: 'pl‮at​form/de⁩ploy', name: 'Odd id', scope: 'sh‮op​', overrides: ['gl‮ob​al'] },
    ]);
    await open(page);
    await expect(cards(page)).toHaveCount(2);
    await expect(cards(page).first()).toContainText('<img src=x onerror="window.__pwn=1"> **bold** Retry budgets');
    await expect(main(page).locator('img[src="x"]')).toHaveCount(0);
    await expect(main(page).locator('strong')).toHaveCount(0);
    expect(await page.evaluate(() => (window as unknown as { __pwn?: number }).__pwn)).toBeUndefined();

    const odd = card(page, 'Odd id');
    await expect(odd).toContainText('platform/deploy');
    await expect(odd).toContainText('shop');
    await expect(odd).toContainText(/from global/);
    const shown = (await main(page).innerText()) + (await main(page).innerHTML());
    expect(shown).not.toMatch(/[​-‏‪-‮⁠-⁩﻿]/);
  });
});

// #783: an error box closes a message lacking . ? or ! before its hint (handoff §8), as the
// article page does (#759).
test.describe('the article index error: message then hint (#783)', () => {
  const norm = (s: string) => s.replace(/\s+/g, ' ').trim();
  const fail = async (page: Page, message: string, hint: string) => {
    await stubProjects(page);
    await page.route(apiArticles, (route) => json(route, 503, envelope('not_ready', message, hint)));
    await open(page);
    return main(page).getByRole('alert');
  };

  test('a message without a closing period gets one before the hint', async ({ page }) => {
    const alert = await fail(page, 'The article store is unavailable', 'Try again in a moment.');
    await expect(alert).toBeVisible();
    expect(norm(await alert.locator('p').first().innerText())).toBe('The article store is unavailable. Try again in a moment.');
  });

  test('a message already ending in a question mark gets nothing extra before the hint', async ({ page }) => {
    const alert = await fail(page, 'Is the article store down?', 'Try again in a moment.');
    await expect(alert).toBeVisible();
    expect(norm(await alert.locator('p').first().innerText())).toBe('Is the article store down? Try again in a moment.');
  });

  test('a message without a period and without a hint is rendered unchanged', async ({ page }) => {
    const alert = await fail(page, 'The article store is unavailable', '');
    await expect(alert).toBeVisible();
    expect(norm(await alert.locator('p').first().innerText())).toBe('The article store is unavailable');
  });
});
