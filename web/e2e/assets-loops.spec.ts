// The Loops tab of /assets/loops (#647, handoff §3.5): a LoopDetail per loop for
// the selected Project from GET /api/v1/loops?project=. The API is stubbed per
// test (page.route) so every state is deterministic.
//
// Contract these specs pin down for the implementation:
// - a loop is role=article inside the role=tabpanel; its name is a level 2 heading;
// - the dependency closure is a list named "Closure" (role=list, aria-label) whose
//   listitems are the chips, one per agent and skill; with an empty closure the
//   list is not rendered at all;
// - the start commands are two CommandSnippets in the article, the exact texts
//   `goldie pull` and `/loop /goldie-<name>`, each with its own "Copy command" button;
// - states follow #587: an error is a role=alert in the panel, the 429 warning
//   and the empty panel are role=status, the loading skeletons carry aria-busy="true".
import { expect, test, type Page, type Route } from '@playwright/test';
import { eachViewport, evidence } from './ux';

const shop = 'github.com/acme/shop';
const apiProjects = /\/api\/v1\/projects$/;
const apiLoops = /\/api\/v1\/loops(\?.*)?$/;

type Loop = {
  name: string;
  description?: string;
  scope?: string;
  interval?: string;
  dynamic?: boolean;
  stop?: string;
  agents?: string[];
  skills?: string[];
};

const loop = ({ agents, skills, ...l }: Loop) => ({
  description: '',
  stop: '',
  dynamic: false,
  digest: '0'.repeat(64),
  ...l,
  closure: {
    agents: (agents ?? []).map((name) => ({ name, scope: 'global' })),
    skills: (skills ?? []).map((name) => ({ name, scope: 'global' })),
  },
});

const json = (route: Route, status: number, body: unknown, headers: Record<string, string> = {}) =>
  route.fulfill({ status, contentType: 'application/json', headers, body: JSON.stringify(body) });

const envelope = (code: string, message: string, hint: string) => ({ error: { code, message, details: { hint } } });

const stubProjects = (page: Page) => page.route(apiProjects, (route) => json(route, 200, { projects: [shop] }));

const stubLoops = (page: Page, loops: Loop[], extra: Record<string, unknown> = {}) =>
  page.route(apiLoops, (route) => json(route, 200, { loops: loops.map(loop), ...extra }));

const panel = (page: Page) => page.getByRole('tabpanel');
const details = (page: Page) => panel(page).getByRole('article');
const detail = (page: Page, name: string) =>
  details(page).filter({ has: page.getByRole('heading', { name, exact: true }) });
const open = (page: Page, project = shop) => page.goto(`/assets/loops?project=${encodeURIComponent(project)}`);

const sample: Loop[] = [
  {
    name: 'nightly',
    scope: 'global',
    description: 'Keep the nightly build green.',
    interval: '30m',
    stop: 'when CI is green',
    agents: ['reviewer', 'fixer'],
    skills: ['deploy'],
  },
  { name: 'drift', scope: shop, description: 'Watch for drift.', dynamic: true, stop: 'when the cluster matches git' },
  { name: 'bare', description: 'No dependencies.', interval: '1h', stop: 'never' },
];

eachViewport((width, theme) => {
  test('the Loops tab with loops is captured and axe-clean, with a valid heading order', async ({ page }) => {
    await stubProjects(page);
    await stubLoops(page, sample);
    await open(page);
    await expect(details(page)).toHaveCount(3);
    await evidence(page, width, theme, '', 'assets-loops');
  });

  test('the Loops tab with a long hostile name and text is captured and axe-clean, with no horizontal scroll', async ({ page }) => {
    await stubProjects(page);
    await stubLoops(page, [
      {
        name: 'a'.repeat(120),
        description: '<img src=x onerror=1> ' + 'long-word-'.repeat(30),
        interval: '5m',
        stop: '<script>1</script> ' + 'x'.repeat(150),
        agents: ['g'.repeat(80)],
        skills: ['s'.repeat(80)],
      },
    ]);
    await open(page);
    await expect(details(page)).toHaveCount(1);
    await evidence(page, width, theme, '-long', 'assets-loops');
  });

  test('the Loops tab error state is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiLoops, (route) =>
      json(route, 500, envelope('internal', 'Something went wrong.', 'Try again in a moment.')),
    );
    await open(page);
    await expect(panel(page).getByRole('alert')).toBeVisible();
    await evidence(page, width, theme, '-error', 'assets-loops');
  });

  test('the Loops tab loading skeletons are captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiLoops, async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 4000));
      await json(route, 200, { loops: [] }).catch(() => {});
    });
    await open(page);
    await expect(panel(page).locator('[aria-busy="true"]')).toBeVisible();
    await evidence(page, width, theme, '-loading', 'assets-loops');
  });

  test('the Loops tab empty state is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await stubLoops(page, []);
    await open(page);
    await expect(panel(page).getByRole('status')).toBeVisible();
    await evidence(page, width, theme, '-empty', 'assets-loops');
  });
});

test.describe('the Loops tab', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('requests GET /api/v1/loops with the Project as the project query', async ({ page }) => {
    await stubProjects(page);
    const urls: string[] = [];
    await page.route(apiLoops, (route) => {
      urls.push(route.request().url());
      return json(route, 200, { loops: [] });
    });
    await open(page);
    await expect.poll(() => urls.length).toBeGreaterThan(0);
    const u = new URL(urls[0]);
    expect(u.pathname).toBe('/api/v1/loops');
    expect(u.searchParams.get('project')).toBe(shop);
  });

  test('sorts the loops by name, ascending, whatever order the API answers in', async ({ page }) => {
    await stubProjects(page);
    await stubLoops(page, sample);
    await open(page);
    await expect(details(page)).toHaveCount(3);
    await expect(details(page).getByRole('heading', { level: 2 })).toHaveText(['bare', 'drift', 'nightly']);
  });

  test('a loop shows the name, Scope, description, interval and stop condition', async ({ page }) => {
    await stubProjects(page);
    await stubLoops(page, sample);
    await open(page);
    const l = detail(page, 'nightly');
    await expect(l).toContainText('global');
    await expect(l).toContainText('Keep the nightly build green.');
    await expect(l).toContainText('30m');
    await expect(l).toContainText('when CI is green');
    await expect(l).not.toContainText(/dynamic/i);
    await expect(detail(page, 'drift')).toContainText(shop);
  });

  test('a dynamic loop shows "dynamic" instead of an interval', async ({ page }) => {
    await stubProjects(page);
    await stubLoops(page, sample);
    await open(page);
    await expect(detail(page, 'drift')).toContainText('dynamic');
    await expect(detail(page, 'drift')).toContainText('when the cluster matches git');
    await expect(detail(page, 'bare')).not.toContainText(/dynamic/i);
    await expect(detail(page, 'bare')).toContainText('1h');
  });

  test('a dynamic flag wins over a stray interval', async ({ page }) => {
    await stubProjects(page);
    await stubLoops(page, [{ name: 'both', dynamic: true, interval: '45m', stop: 's' }]);
    await open(page);
    await expect(detail(page, 'both')).toContainText('dynamic');
    await expect(detail(page, 'both')).not.toContainText('45m');
  });

  test('with no scope the loop shows no Scope chip', async ({ page }) => {
    await stubProjects(page);
    await stubLoops(page, [{ name: 'plain', description: 'D.', interval: '2h', stop: 'S.' }]);
    await open(page);
    await expect(details(page)).toHaveCount(1);
    const text = await details(page).innerText();
    expect(text).not.toMatch(/global|local|github\.com/);
  });

  test('every closure agent and skill appears as a chip', async ({ page }) => {
    await stubProjects(page);
    await stubLoops(page, sample);
    await open(page);
    const chips = detail(page, 'nightly').getByRole('list', { name: 'Closure' }).getByRole('listitem');
    await expect(chips).toHaveCount(3);
    const texts = (await chips.allTextContents()).join(' | ');
    for (const name of ['reviewer', 'fixer', 'deploy']) expect(texts).toContain(name);
  });

  test('a loop with an empty closure shows no chip row', async ({ page }) => {
    await stubProjects(page);
    await stubLoops(page, sample);
    await open(page);
    await expect(details(page)).toHaveCount(3);
    await expect(detail(page, 'bare').getByRole('list', { name: 'Closure' })).toHaveCount(0);
    await expect(detail(page, 'bare').getByRole('listitem')).toHaveCount(0);
  });

  test('each loop shows goldie pull and /loop /goldie-<name>, each with a Copy command button', async ({ page }) => {
    await stubProjects(page);
    await stubLoops(page, sample);
    await open(page);
    for (const name of ['bare', 'drift', 'nightly']) {
      const l = detail(page, name);
      await expect(l.getByText('goldie pull', { exact: true })).toBeVisible();
      await expect(l.getByText(`/loop /goldie-${name}`, { exact: true })).toBeVisible();
      const copy = l.getByRole('button', { name: 'Copy command', exact: true });
      await expect(copy).toHaveCount(2);
      for (const b of await copy.all()) await expect(b).toHaveAttribute('aria-label', 'Copy command');
    }
  });

  test('the loop copy button copies exactly /loop /goldie-<name>', async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await stubProjects(page);
    await stubLoops(page, sample);
    await open(page);
    const l = detail(page, 'nightly');
    const snippet = l.locator('div', { has: page.getByText('/loop /goldie-nightly', { exact: true }) }).last();
    const copy = snippet.getByRole('button', { name: 'Copy command', exact: true });
    await copy.focus();
    await page.keyboard.press('Enter');
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe('/loop /goldie-nightly');
  });

  test('a loop shows no usage figure and no featured marker (out of scope)', async ({ page }) => {
    await stubProjects(page);
    await stubLoops(page, sample);
    await open(page);
    await expect(details(page)).toHaveCount(3);
    await expect(panel(page)).not.toContainText(/usage|featured|uses|used/i);
  });

  test('renders a hostile description and stop condition as text, never as markup', async ({ page }) => {
    await stubProjects(page);
    await stubLoops(page, [
      { name: 'a-img', description: '<img src=x onerror=window.__pwn=1>', stop: '<script>window.__pwn=2</script>' },
      {
        name: 'b-link',
        description: '[click](javascript:window.__pwn=3)',
        stop: '<a href="javascript:window.__pwn=4">x</a>',
      },
      { name: 'c-bidi', description: 'safe‮txt.exe​', stop: 'ok⁦hidden⁩' },
    ]);
    await open(page);
    await expect(details(page)).toHaveCount(3);
    await expect(details(page).nth(0)).toContainText('<img src=x onerror=window.__pwn=1>');
    await expect(details(page).nth(0)).toContainText('<script>window.__pwn=2</script>');
    await expect(details(page).nth(1)).toContainText('[click](javascript:window.__pwn=3)');
    await expect(details(page).nth(1)).toContainText('<a href="javascript:window.__pwn=4">x</a>');
    expect(await panel(page).locator('img, script, a[href^="javascript:" i]').count()).toBe(0);
    expect(await page.evaluate(() => (window as unknown as { __pwn?: number }).__pwn)).toBeUndefined();
    const text = await details(page).nth(2).innerText();
    expect(text).not.toMatch(/[​-‏‪-‮⁠-⁩﻿]/);
  });

  test('a hostile chip name renders as text', async ({ page }) => {
    await stubProjects(page);
    await stubLoops(page, [
      { name: 'x', interval: '1m', stop: 's', agents: ['<img src=x onerror=window.__pwn=5>'], skills: [] },
    ]);
    await open(page);
    await expect(detail(page, 'x')).toContainText('<img src=x onerror=window.__pwn=5>');
    expect(await panel(page).locator('img').count()).toBe(0);
    expect(await page.evaluate(() => (window as unknown as { __pwn?: number }).__pwn)).toBeUndefined();
  });

  test('a long name wraps without horizontal scroll at 375 px', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 812 });
    await stubProjects(page);
    await stubLoops(page, [
      { name: 'n'.repeat(200), interval: '1m', stop: 's'.repeat(200), agents: ['a'.repeat(100)] },
    ]);
    await open(page);
    await expect(details(page)).toHaveCount(1);
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(0);
    const box = (await details(page).boundingBox())!;
    expect(box.x + box.width, 'the card stays inside the viewport').toBeLessThanOrEqual(375);
  });

  test('an unregistered Project shows the API hint', async ({ page }) => {
    await stubProjects(page);
    const hint = 'The Project github.com/acme/none is not registered on this server, so this is global guidance only.';
    await stubLoops(page, sample, { hint });
    await open(page, 'github.com/acme/none');
    await expect(panel(page)).toContainText(hint);
    await expect(details(page)).toHaveCount(3);
  });

  test('an empty list shows the neutral status panel, not an alert', async ({ page }) => {
    await stubProjects(page);
    await stubLoops(page, []);
    await open(page);
    await expect(panel(page).getByRole('status')).toContainText(`No loops for ${shop} yet.`);
    await expect(panel(page).getByRole('alert')).toHaveCount(0);
    await expect(details(page)).toHaveCount(0);
  });

  test('shows skeletons with aria-busy while a slow load runs, and no spinner', async ({ page }) => {
    await stubProjects(page);
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => (release = resolve));
    await page.route(apiLoops, async (route) => {
      await gate;
      await json(route, 200, { loops: sample.map(loop) });
    });
    await open(page);
    await expect(panel(page).locator('[aria-busy="true"]')).toBeVisible();
    await expect(details(page)).toHaveCount(0);
    expect(await page.locator('[role="progressbar"], [class*="spinner" i]').count(), 'no spinner').toBe(0);
    release();
    await expect(details(page)).toHaveCount(3);
    await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
  });

  test('an API error shows the danger alert with the first hint and Retry, which reloads', async ({ page }) => {
    await stubProjects(page);
    let calls = 0;
    await page.route(apiLoops, (route) => {
      calls++;
      if (calls === 1) return json(route, 500, envelope('internal', 'Something went wrong.', 'Try again in a moment.'));
      return json(route, 200, { loops: sample.map(loop) });
    });
    await open(page);
    const alert = panel(page).getByRole('alert');
    await expect(alert).toContainText("Couldn't load loops.");
    await expect(alert).toContainText('Try again in a moment.');
    await expect(details(page)).toHaveCount(0);
    await alert.getByRole('button', { name: 'Retry' }).click();
    await expect(details(page)).toHaveCount(3);
    await expect(panel(page).getByRole('alert')).toHaveCount(0);
  });

  test('an error never shows more than the envelope: no stack, path or address', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiLoops, (route) =>
      route.fulfill({
        status: 502,
        contentType: 'text/html',
        body: '<pre>panic at /srv/goldie/x.go:12 10.0.0.7:8080</pre>',
      }),
    );
    await open(page);
    const alert = panel(page).getByRole('alert');
    await expect(alert).toContainText("Couldn't load loops.");
    await expect(alert).not.toContainText('panic');
    await expect(alert).not.toContainText('10.0.0.7');
    await expect(alert).not.toContainText('/srv/');
  });

  test('a 429 disables Retry and shows a countdown until it ends', async ({ page }) => {
    await stubProjects(page);
    let calls = 0;
    await page.route(apiLoops, (route) => {
      calls++;
      if (calls === 1) {
        return json(route, 429, envelope('rate_limited', 'Too many requests.', 'Wait, then retry.'), {
          'Retry-After': '3',
        });
      }
      return json(route, 200, { loops: sample.map(loop) });
    });
    await open(page);
    await expect(panel(page)).toHaveText(/try again in [123] s/);
    const retry = panel(page).getByRole('button', { name: 'Retry' });
    await expect(retry).toBeDisabled();
    await expect(retry).toBeEnabled({ timeout: 6000 });
    await retry.click();
    await expect(details(page)).toHaveCount(3);
  });

  test('the headings under the h1 never skip a level', async ({ page }) => {
    await stubProjects(page);
    await stubLoops(page, sample);
    await open(page);
    await expect(details(page)).toHaveCount(3);
    const levels = await page
      .locator('main h1, main h2, main h3, main h4, main h5, main h6')
      .evaluateAll((els) => els.map((el) => Number(el.tagName.slice(1))));
    expect(levels[0], 'the h1 is first').toBe(1);
    levels.forEach((l, i) => {
      if (i > 0) expect(l, `heading #${i + 1} follows level ${levels[i - 1]}`).toBeLessThanOrEqual(levels[i - 1] + 1);
    });
  });
});
