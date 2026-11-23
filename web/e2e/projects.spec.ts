// The Project selector and the Project's Item list (#496, handoff §2.1, §5.1,
// §5.2, §9). The selector sits in the top bar and reads GET /api/v1/projects;
// the list renders on Home (`/?project=host/org/repo`) from
// GET /api/v1/projects/{id}/items. The API is stubbed per test (page.route) so
// every state is deterministic: the shared server's own Projects never matter.
//
// Contract these specs pin down for the implementation:
// - the selector is a role=combobox named "Project" in the header (a native
//   select or an ARIA combobox), whose choices are role=option;
// - the list is a role=list inside main whose rows are role=listitem;
// - an error is a role=alert, the 429 warning is a role=status, both in main.
import { expect, test, type Page, type Route } from '@playwright/test';
import { eachViewport, evidence } from './ux';

const shop = 'github.com/acme/shop';
const none = 'github.com/acme/none';
const apiList = /\/api\/v1\/projects$/;
const apiItems = /\/api\/v1\/projects\/[^/]+\/items$/;

type Item = { id: string; name: string; status?: 'published' | 'deprecated'; scope?: string; enforced?: boolean };

const item = (i: Item) => ({
  keywords: [],
  content: 'body',
  status: 'published',
  scope: 'global',
  enforced: false,
  ...i,
});

const json = (route: Route, status: number, body: unknown, headers: Record<string, string> = {}) =>
  route.fulfill({ status, contentType: 'application/json', headers, body: JSON.stringify(body) });

const envelope = (code: string, message: string, hint: string) => ({ error: { code, message, details: { hint } } });

const stubProjects = (page: Page, projects: string[] = [shop, 'github.com/acme/billing']) =>
  page.route(apiList, (route) => json(route, 200, { projects }));

const stubItems = (page: Page, items: Item[], extra: Record<string, unknown> = {}) =>
  page.route(apiItems, (route) => json(route, 200, { items: items.map(item), suppressions: [], ...extra }));

const main = (page: Page) => page.getByRole('main');
const selector = (page: Page) => page.locator('header').getByRole('combobox', { name: /project/i });
const rows = (page: Page) => main(page).getByRole('listitem');
const projectParam = (page: Page) => new URL(page.url()).searchParams.get('project');

const sample: Item[] = [
  { id: 'platform/deploy', name: 'Deploying to Kubernetes' },
  { id: 'platform/logging', name: 'Structured logging', scope: shop, enforced: true },
  { id: 'platform/old-deploy', name: 'Legacy deploy', status: 'deprecated' },
];

eachViewport((width, theme) => {
  test('the Project selector and the Item list are captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await stubItems(page, sample);
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    await expect(rows(page)).toHaveCount(3);
    await evidence(page, width, theme, '', 'projects');
  });

  test('the loading skeletons are captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiItems, async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 4000));
      await json(route, 200, { items: [], suppressions: [] }).catch(() => {});
    });
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    await expect(page.locator('[aria-busy="true"]')).toBeVisible();
    await evidence(page, width, theme, '-loading', 'projects');
  });

  test('the error and rate-limit states are captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiItems, (route) =>
      json(route, 500, envelope('internal', 'Something went wrong.', 'Try again in a moment.')),
    );
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    await expect(main(page).getByRole('alert')).toBeVisible();
    await evidence(page, width, theme, '-error', 'projects');
  });
});

test.describe('the Project selector', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('lists the Projects from GET /api/v1/projects, full host/org/repo, each with a registered chip', async ({ page }) => {
    await stubProjects(page);
    await stubItems(page, sample);
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    await expect(selector(page)).toContainText(shop);
    await selector(page).click();
    const options = page.getByRole('option');
    await expect(options).toHaveCount(2);
    await expect(options.nth(0)).toContainText(shop);
    await expect(options.nth(0)).toContainText('registered');
    await expect(options.nth(0)).not.toContainText('unregistered');
    await expect(options.nth(1)).toContainText('github.com/acme/billing');
  });

  test('shows the id in a mono face', async ({ page }) => {
    await stubProjects(page);
    await stubItems(page, sample);
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    const family = await selector(page).getByText(shop, { exact: false }).first().evaluate((el) => getComputedStyle(el).fontFamily);
    expect(family).toMatch(/mono/i);
  });

  test('a Project the server does not list carries an unregistered chip with text', async ({ page }) => {
    await stubProjects(page);
    await stubItems(page, [], { hint: `The Project ${none} is not registered on this server, so this is global guidance only.` });
    await page.goto(`/?project=${encodeURIComponent(none)}`);
    await expect(selector(page)).toContainText(none);
    await expect(selector(page)).toContainText('unregistered');
    await expect(main(page)).toContainText('not registered on this server');
  });

  test('shows org/repo below 768 px and the full id from 768 px', async ({ page }) => {
    await stubProjects(page);
    await stubItems(page, sample);
    await page.setViewportSize({ width: 767, height: 900 });
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    await expect(selector(page)).toContainText('acme/shop');
    await expect(selector(page)).not.toContainText('github.com');
    await page.setViewportSize({ width: 768, height: 900 });
    await expect(selector(page)).toContainText(shop);
  });

  test('holds the selection in the project query parameter, never in storage, and keeps other parameters', async ({ page }) => {
    await stubProjects(page);
    await stubItems(page, sample);
    await page.goto('/?kind=article');
    await selector(page).click();
    await page.getByRole('option').filter({ hasText: 'github.com/acme/billing' }).click();
    await expect.poll(() => projectParam(page)).toBe('github.com/acme/billing');
    expect(new URL(page.url()).searchParams.get('kind')).toBe('article');
    const stored = await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }));
    expect(stored, 'the Project is not in web storage').not.toContain('billing');
    await page.reload();
    await expect(selector(page)).toContainText('github.com/acme/billing');
  });

  test('can be operated by keyboard alone: open, move, choose, and Escape closes', async ({ page }) => {
    await stubProjects(page);
    await stubItems(page, sample);
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    await selector(page).focus();
    await expect(selector(page)).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.getByRole('option').first()).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('option')).toHaveCount(0);
    await expect(selector(page), 'focus stays on the selector after Escape').toBeFocused();
    await page.keyboard.press('Enter');
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('Enter');
    await expect.poll(() => projectParam(page)).toBe('github.com/acme/billing');
  });

  test('renders a hostile Project id from the list as text', async ({ page }) => {
    await stubProjects(page, ['github.com/acme/<img src=x onerror=window.__pwn=1>']);
    await stubItems(page, []);
    await page.goto('/');
    await selector(page).click();
    await expect(page.getByRole('option').first()).toContainText('<img src=x onerror=window.__pwn=1>');
    expect(await page.locator('header img').count()).toBe(0);
    expect(await page.evaluate(() => (window as unknown as { __pwn?: number }).__pwn)).toBeUndefined();
  });
});

test.describe("the Project's Item list", () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('requests the Project with its id as one %2F-encoded path segment (ADR 0016)', async ({ page }) => {
    await stubProjects(page);
    const paths: string[] = [];
    await page.route(apiItems, (route) => {
      paths.push(new URL(route.request().url()).pathname);
      return json(route, 200, { items: [], suppressions: [] });
    });
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    await expect.poll(() => paths).toEqual(['/api/v1/projects/github.com%2Facme%2Fshop/items']);
  });

  test('encodes every reserved character of the id and sends no query', async ({ page }) => {
    await stubProjects(page);
    const urls: string[] = [];
    await page.route(apiItems, (route) => {
      urls.push(route.request().url());
      return json(route, 400, envelope('invalid_project', 'Not a Project id.', 'Pass project as host/org/repo.'));
    });
    await page.goto(`/?project=${encodeURIComponent('github.com/acme/re?po#x')}`);
    await expect.poll(() => urls.length).toBeGreaterThan(0);
    const u = new URL(urls[0]);
    expect(u.pathname).toBe('/api/v1/projects/github.com%2Facme%2Fre%3Fpo%23x/items');
    expect(u.search).toBe('');
    expect(u.hash).toBe('');
  });

  test('shows each Item with Kind badge, name, Scope badge and a lifecycle badge', async ({ page }) => {
    await stubProjects(page);
    await stubItems(page, sample);
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    await expect(rows(page)).toHaveCount(3);
    const first = rows(page).nth(0);
    await expect(first).toContainText('Deploying to Kubernetes');
    await expect(first).toContainText('article');
    await expect(first).toContainText('global');
    await expect(first).toContainText('published');
    const second = rows(page).nth(1);
    await expect(second).toContainText('Structured logging');
    await expect(second).toContainText(shop);
    await expect(rows(page).nth(2)).toContainText('deprecated');
  });

  test('a lifecycle badge is a label plus a shape, and the shapes differ, never colour alone (§5.2)', async ({ page }) => {
    await stubProjects(page);
    await stubItems(page, sample);
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    await expect(rows(page)).toHaveCount(3);
    // The badge element is found by its label; its own subtree, or a wrapper
    // below the row that holds no Item name, must hold an svg shape.
    const shapeOf = (row: ReturnType<typeof rows>, label: string) =>
      row.getByText(label, { exact: true }).evaluate((el) => {
        for (let n: Element | null = el; n && n.tagName !== 'LI' && n.getAttribute('role') !== 'listitem'; n = n.parentElement) {
          if (/Deploying|Legacy/.test(n.textContent ?? '')) break;
          const svg = n.querySelector('svg');
          if (svg) return svg.innerHTML;
        }
        return '';
      });
    const published = await shapeOf(rows(page).nth(0), 'published');
    const deprecated = await shapeOf(rows(page).nth(2), 'deprecated');
    expect(published, 'published badge has a shape').not.toBe('');
    expect(deprecated, 'deprecated badge has a shape').not.toBe('');
    expect(deprecated, 'the two lifecycle shapes differ').not.toBe(published);
  });

  test('renders hostile Item names as text, never as markup', async ({ page }) => {
    await stubProjects(page);
    await stubItems(page, [
      { id: 'x/img', name: '<img src=x onerror=window.__pwn=1>' },
      { id: 'x/script', name: '<script>window.__pwn=2</script>' },
      { id: 'x/bidi', name: 'safe‮txt.exe' },
      { id: 'x/link', name: '[click](javascript:window.__pwn=3)' },
    ]);
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    await expect(rows(page)).toHaveCount(4);
    await expect(rows(page).nth(0)).toContainText('<img src=x onerror=window.__pwn=1>');
    await expect(rows(page).nth(1)).toContainText('<script>window.__pwn=2</script>');
    await expect(rows(page).nth(3)).toContainText('[click](javascript:window.__pwn=3)');
    expect(await main(page).locator('img, script, a[href^="javascript:" i]').count()).toBe(0);
    expect(await page.evaluate(() => (window as unknown as { __pwn?: number }).__pwn)).toBeUndefined();
  });

  test('shows skeletons with aria-busy while loading, and no spinner', async ({ page }) => {
    await stubProjects(page);
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => (release = resolve));
    await page.route(apiItems, async (route) => {
      await gate;
      await json(route, 200, { items: sample.map(item), suppressions: [] });
    });
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    const busy = page.locator('[aria-busy="true"]');
    await expect(busy).toBeVisible();
    await expect(rows(page)).toHaveCount(0);
    expect(await page.locator('[role="progressbar"], [class*="spinner" i]').count(), 'no spinner').toBe(0);
    release();
    await expect(rows(page)).toHaveCount(3);
    await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
  });

  test('shows the neutral empty-Kind panel, not an alert', async ({ page }) => {
    await stubProjects(page);
    await stubItems(page, []);
    await page.goto(`/?project=${encodeURIComponent(shop)}&kind=article`);
    const panel = main(page).getByRole('status');
    await expect(panel).toHaveText(`No articles for ${shop} yet.`);
    await expect(main(page).getByRole('alert')).toHaveCount(0);
  });

  test('an API error shows the danger alert with the message, the envelope hint and Retry, which reloads', async ({ page }) => {
    await stubProjects(page);
    let calls = 0;
    await page.route(apiItems, (route) => {
      calls++;
      if (calls === 1) return json(route, 500, envelope('internal', 'Something went wrong.', 'Try again in a moment.'));
      return json(route, 200, { items: sample.map(item), suppressions: [] });
    });
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    const alert = main(page).getByRole('alert');
    await expect(alert).toContainText('Something went wrong.');
    await expect(alert).toContainText('Try again in a moment.');
    await expect(rows(page)).toHaveCount(0);
    await alert.getByRole('button', { name: 'Retry' }).click();
    await expect(rows(page)).toHaveCount(3);
    await expect(main(page).getByRole('alert')).toHaveCount(0);
  });

  test('an error never shows more than the envelope: no stack, path or address', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiItems, (route) =>
      route.fulfill({ status: 502, contentType: 'text/html', body: '<pre>panic at /srv/goldie/x.go:12 10.0.0.7:8080</pre>' }),
    );
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    const alert = main(page).getByRole('alert');
    await expect(alert).toBeVisible();
    await expect(alert).not.toContainText('panic');
    await expect(alert).not.toContainText('10.0.0.7');
    await expect(alert).not.toContainText('/srv/');
    await expect(alert.getByRole('button', { name: 'Retry' })).toBeVisible();
  });

  test('a 429 shows the countdown warning and keeps Retry disabled until it ends', async ({ page }) => {
    await stubProjects(page);
    let calls = 0;
    await page.route(apiItems, (route) => {
      calls++;
      if (calls === 1) {
        return json(route, 429, envelope('rate_limited', 'Too many requests.', 'Wait, then retry.'), { 'Retry-After': '3' });
      }
      return json(route, 200, { items: sample.map(item), suppressions: [] });
    });
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    const warning = main(page).getByRole('status');
    await expect(warning).toHaveText(/Too many requests — try again in [123] s\./);
    const retry = main(page).getByRole('button', { name: 'Retry' });
    await expect(retry).toBeDisabled();
    await expect(warning, 'the countdown ticks').toHaveText(/try again in [12] s\./);
    await expect(retry).toBeEnabled({ timeout: 6000 });
    await retry.click();
    await expect(rows(page)).toHaveCount(3);
  });

  test('an unknown Project id (404) shows the not-found state, not a blank page', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiItems, (route) =>
      json(route, 404, envelope('not_found', 'No such Project.', 'Pick a Project from the selector.')),
    );
    await page.goto(`/?project=${encodeURIComponent('github.com/acme/ghost')}`);
    await expect(page.getByRole('heading', { level: 1 })).toHaveText(/not found/i);
    await expect(main(page).getByRole('alert')).toHaveCount(0);
    await expect(rows(page)).toHaveCount(0);
    expect((await main(page).innerText()).trim().length).toBeGreaterThan(0);
  });

  test('an invalid Project id (400) shows the not-found state and never echoes the id as markup', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiItems, (route) =>
      json(route, 400, envelope('invalid_project', 'Not a Project id.', 'Pass project as host/org/repo.')),
    );
    await page.goto(`/?project=${encodeURIComponent('<img src=x onerror=window.__pwn=1>')}`);
    await expect(page.getByRole('heading', { level: 1 })).toHaveText(/not found/i);
    expect(await page.locator('main img, header img').count()).toBe(0);
    expect(await page.evaluate(() => (window as unknown as { __pwn?: number }).__pwn)).toBeUndefined();
  });
});
