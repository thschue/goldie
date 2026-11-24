// Home without a query (#721, handoff §3.2 items 1, 2 and 4): the suggestion chips, the Browse
// tiles with their counts, and the agent checklist. The API is stubbed per test (page.route).
//
// Contract these specs pin down for the implementation:
// - a chip is a link or a button inside main whose name is the word; it runs the search, so the URL
//   becomes /?q=<word> and the hits render;
// - a tile is a link inside main named after its Kind (Skills, Agents, Loops) whose text holds the
//   count (digits) when its list loaded, and no digits when it failed;
// - the checklist is the part of main headed "Put your agent on the golden path"; each step shows
//   its command, a "Copy command" button and one tick, a button with aria-pressed;
// - ticks are kept in localStorage; a storage that throws is never an error.
import { expect, test, type Page, type Route } from '@playwright/test';
import { eachViewport, evidence, problems } from './ux';

const apiSearch = /\/api\/v1\/search(\?|$)/;
const lists = { skills: /\/api\/v1\/skills(\?|$)/, agents: /\/api\/v1\/agents(\?|$)/, loops: /\/api\/v1\/loops(\?|$)/ };

const json = (route: Route, status: number, body: unknown) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
const named = (n: number, prefix: string) => Array.from({ length: n }, (_, i) => ({ name: `${prefix}-${i + 1}`, files: [{ path: 'SKILL.md', size: 16 }] }));

type Answer = number | 'fail';

// stubLists answers each list with that many entries, or with a 500 for 'fail'.
async function stubLists(page: Page, a: { skills: Answer; agents: Answer; loops: Answer }) {
  for (const key of ['skills', 'agents', 'loops'] as const) {
    const v = a[key];
    await page.route(lists[key], (route) =>
      v === 'fail' ? json(route, 500, { error: { code: 'internal', message: 'boom', details: {} } }) : json(route, 200, { [key]: named(v, key) }),
    );
  }
}

async function stubSearch(page: Page) {
  const seen: string[] = [];
  await page.route(apiSearch, (route) => {
    const q = new URL(route.request().url()).searchParams.get('q') ?? '';
    seen.push(q);
    return json(route, 200, {
      query: q,
      results: [{ id: 'platform/db', name: 'Databases', kind: 'article', scope: 'global', keywords: [], score: 4, matched: [q] }],
    });
  });
  return seen;
}

const main = (page: Page) => page.getByRole('main');
const chip = (page: Page, word: string) => main(page).getByRole('link', { name: word, exact: true }).or(main(page).getByRole('button', { name: word, exact: true }));
const tile = (page: Page, name: string) => main(page).getByRole('link', { name: new RegExp(`^\\s*${name}\\b`) });
const checklist = (page: Page) =>
  main(page)
    .locator('section, div, article')
    .filter({ has: page.getByText('Put your agent on the golden path') })
    .filter({ has: page.getByRole('button', { name: 'Copy command' }) })
    .last();
const ticks = (page: Page) => checklist(page).locator('button[aria-pressed]');

test.describe('the suggestion chips', () => {
  test('show "Try: database · logging · retries · feature flags" below the field', async ({ page }) => {
    await stubLists(page, { skills: 1, agents: 1, loops: 1 });
    await page.goto('/');
    await expect(main(page)).toContainText('Try:');
    for (const w of ['database', 'logging', 'retries', 'feature flags']) await expect(chip(page, w)).toBeVisible();
  });

  test('a chip runs that search: the URL becomes /?q=<word>, the field holds it and the hits render', async ({ page }) => {
    await stubLists(page, { skills: 1, agents: 1, loops: 1 });
    const seen = await stubSearch(page);
    await page.goto('/');
    await chip(page, 'feature flags').click();
    await expect(page).toHaveURL((u) => u.pathname === '/' && u.searchParams.get('q') === 'feature flags');
    await expect(main(page).locator('input[type="search"]')).toHaveValue('feature flags');
    await expect(main(page).getByRole('listitem')).toHaveCount(1);
    expect(seen).toEqual(['feature flags']);
  });
});

test.describe('Browse the golden path', () => {
  test('shows a tile each for Skills, Agents and Loops with their name, purpose, count and link', async ({ page }) => {
    await stubLists(page, { skills: 3, agents: 2, loops: 5 });
    await page.goto('/');
    await expect(main(page)).toContainText('Browse the golden path');
    for (const [name, tab, count] of [
      ['Skills', 'skills', 3],
      ['Agents', 'agents', 2],
      ['Loops', 'loops', 5],
    ] as const) {
      const t = tile(page, name);
      await expect(t).toHaveAttribute('href', `/assets/${tab}`);
      await expect(t).toContainText(String(count));
      const words = (await t.innerText()).replace(/\d+/g, '').replace(name, '').trim();
      expect(words.length, `a one-line purpose in the ${name} tile`).toBeGreaterThan(10);
    }
    await expect(main(page).locator('a[href="/assets/articles"]')).toHaveCount(0);
  });

  test('a Kind with no Items shows 0 and still links', async ({ page }) => {
    await stubLists(page, { skills: 0, agents: 0, loops: 0 });
    await page.goto('/');
    for (const [name, tab] of [
      ['Skills', 'skills'],
      ['Agents', 'agents'],
      ['Loops', 'loops'],
    ] as const) {
      await expect(tile(page, name)).toContainText('0');
      await expect(tile(page, name)).toHaveAttribute('href', `/assets/${tab}`);
    }
  });

  test('a tile opens its tab', async ({ page }) => {
    await stubLists(page, { skills: 1, agents: 1, loops: 1 });
    await page.goto('/');
    await tile(page, 'Agents').click();
    await expect(page).toHaveURL((u) => u.pathname === '/assets/agents');
  });

  test('a list that fails leaves its tile without a count; the rest of Home renders and no error page appears', async ({ page, baseURL }) => {
    const seen = await problems(page, baseURL ?? '');
    await stubLists(page, { skills: 'fail', agents: 4, loops: 7 });
    await page.goto('/');
    await expect(tile(page, 'Agents')).toContainText('4');
    await expect(tile(page, 'Loops')).toContainText('7');
    await expect(tile(page, 'Skills')).toBeVisible();
    await expect(tile(page, 'Skills')).not.toContainText(/\d/);
    await expect(tile(page, 'Skills')).toHaveAttribute('href', '/assets/skills');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('What are you deciding today?');
    await expect(main(page).locator('input[type="search"]')).toBeVisible();
    await expect(main(page).getByRole('alert')).toHaveCount(0);
    await expect(main(page)).not.toContainText(/boom|Page not found/i);
    await expect(checklist(page)).toBeVisible();
    expect(seen.errors, 'page errors and CSP violations').toEqual([]);
  });

  test('every list failing leaves three tiles without counts and no placeholder', async ({ page }) => {
    await stubLists(page, { skills: 'fail', agents: 'fail', loops: 'fail' });
    await page.goto('/');
    for (const name of ['Skills', 'Agents', 'Loops']) await expect(tile(page, name)).not.toContainText(/\d/);
    await expect(main(page)).not.toContainText('Nothing to show yet');
    await expect(main(page).getByRole('alert')).toHaveCount(0);
  });
});

test.describe('Put your agent on the golden path', () => {
  test("shows the three steps with this server's URL, a copy button and a tick each", async ({ page, baseURL }) => {
    await stubLists(page, { skills: 1, agents: 1, loops: 1 });
    await page.goto('/');
    const box = checklist(page);
    await expect(box).toContainText('goldie init');
    await expect(box).toContainText('goldie instructions apply');
    const origin = new URL(baseURL ?? '').origin.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    await expect(box).toContainText(new RegExp(`claude mcp add --transport http goldie ${origin}(/mcp)?\\s`));
    await expect(box.getByRole('button', { name: 'Copy command' })).toHaveCount(3);
    await expect(ticks(page)).toHaveCount(3);
    for (const t of await ticks(page).all()) await expect(t).toHaveAttribute('aria-pressed', 'false');
  });

  test('copy puts the step command on the clipboard', async ({ page, context, baseURL }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: new URL(baseURL ?? '').origin });
    await stubLists(page, { skills: 1, agents: 1, loops: 1 });
    await page.goto('/');
    await checklist(page).getByRole('button', { name: 'Copy command' }).first().click();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('goldie init');
  });

  test('a tick toggles, and is kept per browser across a reload, one step at a time', async ({ page }) => {
    await stubLists(page, { skills: 1, agents: 1, loops: 1 });
    await page.goto('/');
    await ticks(page).nth(1).click();
    await expect(ticks(page).nth(1)).toHaveAttribute('aria-pressed', 'true');
    await expect(ticks(page).nth(0)).toHaveAttribute('aria-pressed', 'false');
    await page.reload();
    await expect(ticks(page).nth(1)).toHaveAttribute('aria-pressed', 'true');
    await expect(ticks(page).nth(0)).toHaveAttribute('aria-pressed', 'false');
    await expect(ticks(page).nth(2)).toHaveAttribute('aria-pressed', 'false');
    await ticks(page).nth(1).click();
    await page.reload();
    await expect(ticks(page).nth(1)).toHaveAttribute('aria-pressed', 'false');
  });

  test('Home still works when localStorage throws', async ({ page, baseURL }) => {
    const seen = await problems(page, baseURL ?? '');
    await page.addInitScript(() => {
      const fail = () => {
        throw new DOMException('The operation is insecure.', 'SecurityError');
      };
      Object.defineProperty(window, 'localStorage', { configurable: true, get: fail });
    });
    await stubLists(page, { skills: 2, agents: 2, loops: 2 });
    await page.goto('/');
    await expect(checklist(page)).toContainText('goldie init');
    await expect(tile(page, 'Skills')).toContainText('2');
    await expect(ticks(page)).toHaveCount(3);
    await ticks(page).first().click();
    await expect(main(page)).toContainText('Browse the golden path');
    expect(seen.errors, 'page errors and CSP violations').toEqual([]);
  });
});

test('the placeholder "Nothing to show yet" is gone from Home', async ({ page }) => {
  await stubLists(page, { skills: 1, agents: 1, loops: 1 });
  await page.goto('/');
  await expect(main(page)).not.toContainText('Nothing to show yet');
  await expect(main(page)).not.toContainText('pages come online');
});

eachViewport((width, theme) => {
  test('Home without a query is captured, axe-clean and error-free', async ({ page, baseURL }) => {
    const seen = await problems(page, baseURL ?? '');
    await stubLists(page, { skills: 3, agents: 2, loops: 5 });
    await page.goto('/');
    await expect(tile(page, 'Skills')).toContainText('3');
    await evidence(page, width, theme, '', 'home');
    expect(seen.errors, 'page errors and CSP violations').toEqual([]);
  });
});
