// The Agents tab of /assets/agents (#589, handoff §3.5, §5.1, §5.2): a row per
// agent for the selected Project from GET /api/v1/agents?project=. The API is
// stubbed per test (page.route) so every state is deterministic. The states
// follow the Skills tab (#587), with the same contract:
// - the rows are role=listitem inside the role=tabpanel; a row name is a
//   heading (level 2);
// - an error is a role=alert in the panel, the 429 warning and the empty panel
//   are role=status, both in the panel;
// - the loading skeletons carry aria-busy="true" inside the panel.
import { expect, test, type Page, type Route } from '@playwright/test';
import { eachViewport, evidence } from './ux';

const shop = 'github.com/acme/shop';
const apiProjects = /\/api\/v1\/projects$/;
const apiAgents = /\/api\/v1\/agents(\?.*)?$/;

type Agent = { name: string; scope?: string; description?: string };

const agent = (a: Agent) => ({ description: '', ...a });

const json = (route: Route, status: number, body: unknown, headers: Record<string, string> = {}) =>
  route.fulfill({ status, contentType: 'application/json', headers, body: JSON.stringify(body) });

const envelope = (code: string, message: string, hint: string) => ({ error: { code, message, details: { hint } } });

const stubProjects = (page: Page) => page.route(apiProjects, (route) => json(route, 200, { projects: [shop] }));

const stubAgents = (page: Page, agents: Agent[]) =>
  page.route(apiAgents, (route) => json(route, 200, { agents: agents.map(agent) }));

const panel = (page: Page) => page.getByRole('tabpanel');
const rows = (page: Page) => panel(page).getByRole('listitem');
const open = (page: Page, project = shop) => page.goto(`/assets/agents?project=${encodeURIComponent(project)}`);

const sample: Agent[] = [
  { name: 'reviewer', scope: 'global', description: 'Reviews a change before merge.' },
  { name: 'builder', scope: shop, description: 'Builds the image.' },
  { name: 'planner', description: 'Plans the work.' },
];

eachViewport((width, theme) => {
  test('the Agents tab with rows is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await stubAgents(page, [
      ...sample,
      { name: 'a-very-long-agent-name-'.repeat(8), scope: shop, description: 'x'.repeat(200) },
    ]);
    await open(page);
    await expect(rows(page)).toHaveCount(4);
    await evidence(page, width, theme, '', 'assets-agents');
  });

  test('the Agents tab error state is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiAgents, (route) =>
      json(route, 500, envelope('internal', 'Something went wrong.', 'Try again in a moment.')),
    );
    await open(page);
    await expect(panel(page).getByRole('alert')).toBeVisible();
    await evidence(page, width, theme, '-error', 'assets-agents');
  });

  test('the Agents tab 429 state is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiAgents, (route) =>
      json(route, 429, envelope('rate_limited', 'Too many requests.', 'Wait, then retry.'), { 'Retry-After': '30' }),
    );
    await open(page);
    await expect(panel(page).getByRole('status')).toBeVisible();
    await evidence(page, width, theme, '-429', 'assets-agents');
  });

  test('the Agents tab loading skeletons are captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiAgents, async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 4000));
      await json(route, 200, { agents: [] }).catch(() => {});
    });
    await open(page);
    await expect(panel(page).locator('[aria-busy="true"]')).toBeVisible();
    await evidence(page, width, theme, '-loading', 'assets-agents');
  });

  test('the Agents tab empty state is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await stubAgents(page, []);
    await open(page);
    await expect(panel(page).getByRole('status')).toBeVisible();
    await evidence(page, width, theme, '-empty', 'assets-agents');
  });
});

test.describe('the Agents tab', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('requests GET /api/v1/agents with the Project as the project query', async ({ page }) => {
    await stubProjects(page);
    const urls: string[] = [];
    await page.route(apiAgents, (route) => {
      urls.push(route.request().url());
      return json(route, 200, { agents: [] });
    });
    await open(page);
    await expect.poll(() => urls.length).toBeGreaterThan(0);
    const u = new URL(urls[0]);
    expect(u.pathname).toBe('/api/v1/agents');
    expect(u.searchParams.get('project')).toBe(shop);
  });

  test('sorts the rows by name, ascending, whatever order the API answers in', async ({ page }) => {
    await stubProjects(page);
    await stubAgents(page, sample);
    await open(page);
    await expect(rows(page)).toHaveCount(3);
    await expect(rows(page).getByRole('heading')).toHaveText(['builder', 'planner', 'reviewer']);
  });

  test('a row shows the name, Scope, description and the path .claude/agents/goldie-<name>.md in mono', async ({
    page,
  }) => {
    await stubProjects(page);
    await stubAgents(page, sample);
    await open(page);
    const row = rows(page).filter({ hasText: 'reviewer' }).first();
    await expect(row.getByRole('heading', { name: 'reviewer', exact: true })).toBeVisible();
    await expect(row).toContainText('global');
    await expect(row).toContainText('Reviews a change before merge.');
    const path = row.getByText('.claude/agents/goldie-reviewer.md', { exact: true });
    await expect(path).toBeVisible();
    expect(await path.evaluate((el) => getComputedStyle(el).fontFamily)).toMatch(/mono/i);
    await expect(rows(page).filter({ hasText: 'builder' })).toContainText(shop);
    await expect(rows(page).filter({ hasText: 'planner' })).toContainText('.claude/agents/goldie-planner.md');
  });

  test('a row shows no usage figure (the MVP cut)', async ({ page }) => {
    await stubProjects(page);
    await stubAgents(page, sample);
    await open(page);
    await expect(rows(page)).toHaveCount(3);
    await expect(panel(page)).not.toContainText(/uses|used|30\s?d|30 days|usage/i);
  });

  test('renders a hostile description as text, never as markup', async ({ page }) => {
    await stubProjects(page);
    await stubAgents(page, [
      { name: 'a-img', description: '<img src=x onerror=window.__pwn=1>' },
      { name: 'b-script', description: '<script>window.__pwn=2</script>' },
      { name: 'c-link', description: '[click](javascript:window.__pwn=3)' },
      { name: 'd-bidi', description: 'safe‮txt.exe​' },
    ]);
    await open(page);
    await expect(rows(page)).toHaveCount(4);
    await expect(rows(page).nth(0)).toContainText('<img src=x onerror=window.__pwn=1>');
    await expect(rows(page).nth(1)).toContainText('<script>window.__pwn=2</script>');
    await expect(rows(page).nth(2)).toContainText('[click](javascript:window.__pwn=3)');
    await expect(rows(page).nth(3)).toContainText('safetxt.exe');
    expect(await panel(page).locator('img, script, a[href^="javascript:" i]').count()).toBe(0);
    expect(await page.evaluate(() => (window as unknown as { __pwn?: number }).__pwn)).toBeUndefined();
  });

  test('a long name wraps without horizontal scroll', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 800 });
    await stubProjects(page);
    await stubAgents(page, [{ name: 'a-very-long-agent-name-without-any-break-'.repeat(6), scope: shop, description: 'Long.' }]);
    await open(page);
    await expect(rows(page)).toHaveCount(1);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow, 'no horizontal scroll').toBeLessThanOrEqual(0);
  });

  test('an empty list shows the neutral status "No agents for <project> yet.", not an alert', async ({ page }) => {
    await stubProjects(page);
    await stubAgents(page, []);
    await open(page);
    await expect(panel(page).getByRole('status')).toContainText(`No agents for ${shop} yet.`);
    await expect(panel(page).getByRole('alert')).toHaveCount(0);
    await expect(rows(page)).toHaveCount(0);
  });

  test('shows skeletons with aria-busy while a slow load runs, and no spinner', async ({ page }) => {
    await stubProjects(page);
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => (release = resolve));
    await page.route(apiAgents, async (route) => {
      await gate;
      await json(route, 200, { agents: sample.map(agent) });
    });
    await open(page);
    await expect(panel(page).locator('[aria-busy="true"]')).toBeVisible();
    await expect(rows(page)).toHaveCount(0);
    expect(await page.locator('[role="progressbar"], [class*="spinner" i]').count(), 'no spinner').toBe(0);
    release();
    await expect(rows(page)).toHaveCount(3);
    await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
  });

  test('an API error shows the danger alert with the first hint and Retry, which reloads', async ({ page }) => {
    await stubProjects(page);
    let calls = 0;
    await page.route(apiAgents, (route) => {
      calls++;
      if (calls === 1) return json(route, 500, envelope('internal', 'Something went wrong.', 'Try again in a moment.'));
      return json(route, 200, { agents: sample.map(agent) });
    });
    await open(page);
    const alert = panel(page).getByRole('alert');
    await expect(alert).toContainText("Couldn't load agents.");
    await expect(alert).toContainText('Try again in a moment.');
    await expect(rows(page)).toHaveCount(0);
    await alert.getByRole('button', { name: 'Retry' }).click();
    await expect(rows(page)).toHaveCount(3);
    await expect(panel(page).getByRole('alert')).toHaveCount(0);
  });

  test('an error never shows more than the envelope: no stack, path or address', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiAgents, (route) =>
      route.fulfill({ status: 502, contentType: 'text/html', body: '<pre>panic at /srv/goldie/x.go:12 10.0.0.7:8080</pre>' }),
    );
    await open(page);
    const alert = panel(page).getByRole('alert');
    await expect(alert).toContainText("Couldn't load agents.");
    await expect(alert).not.toContainText('panic');
    await expect(alert).not.toContainText('10.0.0.7');
    await expect(alert).not.toContainText('/srv/');
  });

  test('a 429 disables Retry and shows a countdown until it ends', async ({ page }) => {
    await stubProjects(page);
    let calls = 0;
    await page.route(apiAgents, (route) => {
      calls++;
      if (calls === 1) {
        return json(route, 429, envelope('rate_limited', 'Too many requests.', 'Wait, then retry.'), { 'Retry-After': '3' });
      }
      return json(route, 200, { agents: sample.map(agent) });
    });
    await open(page);
    await expect(panel(page)).toHaveText(/try again in [123] s/);
    const retry = panel(page).getByRole('button', { name: 'Retry' });
    await expect(retry).toBeDisabled();
    await expect(retry).toBeEnabled({ timeout: 6000 });
    await retry.click();
    await expect(rows(page)).toHaveCount(3);
  });

  test('the headings under the h1 never skip a level', async ({ page }) => {
    await stubProjects(page);
    await stubAgents(page, sample);
    await open(page);
    await expect(rows(page)).toHaveCount(3);
    const levels = await page
      .locator('main h1, main h2, main h3, main h4, main h5, main h6')
      .evaluateAll((els) => els.map((el) => Number(el.tagName.slice(1))));
    expect(levels[0], 'the h1 is first').toBe(1);
    levels.forEach((l, i) => {
      if (i > 0) expect(l, `heading #${i + 1} follows level ${levels[i - 1]}`).toBeLessThanOrEqual(levels[i - 1] + 1);
    });
  });
});
