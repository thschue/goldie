// Search from Home (#497, handoff §3.2, §3.3, §5.1, §5.2, §8, §9). Home at `/`
// asks the question and has the search field; `/?q=` shows the hits. The API is
// stubbed per test (page.route) so every state is deterministic.
//
// Contract these specs pin down for the implementation:
// - the field is the only input[type=search] in main, with an accessible name,
//   and a button named /clear/i empties it;
// - the hits are a role=list inside main whose rows are role=listitem, each
//   holding exactly one link (the whole hit);
// - the Kind pills are buttons with aria-pressed whose text holds the Kind and
//   its count; they are inside main and carry the same names as the hits' Kinds;
// - a Miss is the only role=status panel with a dashed border in main; an
//   error is a role=alert in main;
// - the Item link's path is /items/<kind>/<id>, the id's slash encoded or not;
// - one GET /api/v1/search per query carries `q`, `project` when one is selected
//   and `kind` when a Kind pill is pressed; a Kind pill is a new call, the browser
//   no longer filters (#730);
// - the "Overrides" line names the hit's own kind and its name or its id:
//   "Overrides article:<name|id> from <scope>", one "from" per Scope.
import { expect, test, type Page, type Route } from '@playwright/test';
import { eachViewport, evidence, problems } from './ux';

const shop = 'github.com/acme/shop';
const apiSearch = /\/api\/v1\/search(\?|$)/;

type Hit = {
  id: string;
  name: string;
  kind?: string;
  scope?: string;
  matched?: string[];
  enforced?: boolean;
  overrides?: string[];
};

// A hit as the API sends it; an `enforced` or `overrides` the test leaves out
// stays out of the JSON (an older server).
const hit = (h: Hit) => ({ kind: 'article', scope: 'global', keywords: [], score: 4, matched: ['database'], ...h });

const json = (route: Route, status: number, body: unknown) =>
  route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

const results = (query: string, hits: Hit[]) => ({ query, results: hits.map(hit) });

const envelope = (message: string, hint: string) => ({ error: { code: 'internal', message, details: { hint } } });

// stubSearch answers every search with the same hits and counts the requests.
async function stubSearch(page: Page, hits: Hit[]) {
  const seen: string[] = [];
  await page.route(apiSearch, (route) => {
    const q = new URL(route.request().url()).searchParams.get('q') ?? '';
    seen.push(q);
    return json(route, 200, results(q, hits));
  });
  return seen;
}

const main = (page: Page) => page.getByRole('main');
const field = (page: Page) => main(page).locator('input[type="search"]');
const list = (page: Page) => main(page).getByRole('list');
const rows = (page: Page) => list(page).getByRole('listitem');
const links = (page: Page) => rows(page).getByRole('link');
const pill = (page: Page, kind: RegExp) => main(page).getByRole('button', { name: kind });
const missPanel = (page: Page) => main(page).getByRole('status').filter({ hasText: /No golden path/ });
const qParam = (page: Page) => new URL(page.url()).searchParams.get('q');

const sample: Hit[] = [
  { id: 'platform/deploy', name: 'Deploying to Kubernetes', matched: ['kubernetes', 'name'], enforced: false, overrides: ['global'] },
  { id: 'platform/logging', name: 'Structured logging', scope: 'org', matched: ['logging'], enforced: true, overrides: [] },
  { id: 'skill-retry', name: 'skill-retry', kind: 'skill', scope: 'global', matched: ['retries'], enforced: false, overrides: [] },
  { id: 'reviewer', name: 'reviewer', kind: 'agent', scope: 'global', matched: ['review'], enforced: false, overrides: [] },
  { id: 'platform/flags', name: 'Feature flags', matched: ['flags'], enforced: false, overrides: [] },
];

eachViewport((width, theme) => {
  test('Home with no query is captured and axe-clean', async ({ page }) => {
    await stubSearch(page, []);
    await page.goto('/');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('What are you deciding today?');
    await evidence(page, width, theme, '-home', 'search');
  });

  test('the results are captured and axe-clean', async ({ page }) => {
    await stubSearch(page, sample);
    await page.goto('/?q=database');
    await expect(rows(page)).toHaveCount(5);
    await evidence(page, width, theme, '-results', 'search');
  });

  test('a Miss is captured and axe-clean', async ({ page }) => {
    await stubSearch(page, []);
    await page.goto('/?q=quantum');
    await expect(missPanel(page)).toBeVisible();
    await evidence(page, width, theme, '-miss', 'search');
  });

  test('a search error is captured and axe-clean', async ({ page }) => {
    await page.route(apiSearch, (route) => json(route, 500, envelope('Something went wrong.', 'Try again in a moment.')));
    await page.goto('/?q=database');
    await expect(main(page).getByRole('alert')).toBeVisible();
    await evidence(page, width, theme, '-error', 'search');
  });
});

test.describe('the Home search field', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('Home has the h1 question and one labelled search field with the placeholder', async ({ page }) => {
    await stubSearch(page, []);
    await page.goto('/');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('What are you deciding today?');
    await expect(field(page)).toHaveCount(1);
    await expect(field(page)).toHaveAccessibleName(/.+/);
    await expect(field(page)).toHaveAttribute('placeholder', 'database, logging, retries, feature flags…');
    await expect(page.getByRole('searchbox')).toHaveCount(1);
  });

  test('Home without a query makes no search request and shows no results or Miss', async ({ page }) => {
    const seen = await stubSearch(page, sample);
    await page.goto('/');
    await expect(field(page)).toBeVisible();
    await expect(rows(page)).toHaveCount(0);
    await expect(missPanel(page)).toHaveCount(0);
    expect(seen).toEqual([]);
  });

  test('Enter searches through the router: ?q= is set, the page does not reload, nothing is posted', async ({ page, baseURL }) => {
    const seen = await problems(page, baseURL ?? '');
    const posts: string[] = [];
    page.on('request', (req) => {
      if (req.method() !== 'GET') posts.push(`${req.method()} ${new URL(req.url()).pathname}`);
    });
    await stubSearch(page, sample);
    await page.goto('/');
    await page.evaluate(() => ((window as unknown as { marker: number }).marker = 42));
    await field(page).fill('database');
    await field(page).press('Enter');
    await expect(rows(page)).toHaveCount(5);
    expect(qParam(page)).toBe('database');
    expect(await page.evaluate(() => (window as unknown as { marker?: number }).marker), 'a full page load lost the marker').toBe(42);
    expect(posts, 'a non-GET request was made').toEqual([]);
    await expect(page.locator('form[method="post" i], form[action]')).toHaveCount(0);
    expect(seen.errors, 'page errors and CSP violations (form-action none)').toEqual([]);
    expect(seen.external).toEqual([]);
  });

  test('a search can be shared and reloaded: /?q= fills the field and shows the hits again', async ({ page }) => {
    await stubSearch(page, sample);
    await page.goto('/?q=database%20logging');
    await expect(field(page)).toHaveValue('database logging');
    await expect(rows(page)).toHaveCount(5);
    await page.reload();
    await expect(field(page)).toHaveValue('database logging');
    await expect(rows(page)).toHaveCount(5);
    expect(qParam(page)).toBe('database logging');
  });

  test('back and forward follow the searches', async ({ page }) => {
    const seen = await stubSearch(page, sample);
    await page.goto('/');
    await field(page).fill('first');
    await field(page).press('Enter');
    await expect(rows(page)).toHaveCount(5);
    await field(page).fill('second');
    await field(page).press('Enter');
    await expect.poll(() => qParam(page)).toBe('second');
    await page.goBack();
    await expect.poll(() => qParam(page)).toBe('first');
    await expect(field(page)).toHaveValue('first');
    await page.goBack();
    await expect(field(page)).toHaveValue('');
    await expect(rows(page)).toHaveCount(0);
    expect(seen).toContain('second');
  });

  test('a / anywhere on the page focuses the field, and inside the field it types a slash', async ({ page }) => {
    await stubSearch(page, sample);
    await page.goto('/');
    await page.locator('body').click({ position: { x: 5, y: 300 } });
    await page.keyboard.press('/');
    await expect(field(page)).toBeFocused();
    await expect(field(page)).toHaveValue('');
    await field(page).pressSequentially('ci/cd');
    await expect(field(page)).toHaveValue('ci/cd');
  });

  test('the clear button empties the field, drops ?q=, shows Home and keeps focus in the field', async ({ page }) => {
    await stubSearch(page, sample);
    await page.goto('/?q=database');
    await expect(rows(page)).toHaveCount(5);
    await main(page).getByRole('button', { name: /clear/i }).click();
    await expect(field(page)).toHaveValue('');
    await expect(field(page)).toBeFocused();
    await expect.poll(() => qParam(page)).toBeNull();
    await expect(rows(page)).toHaveCount(0);
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('What are you deciding today?');
  });

  test('a ?q= of only spaces is Home: no search request', async ({ page }) => {
    const seen = await stubSearch(page, sample);
    await page.goto('/?q=%20%20%20');
    await expect(field(page)).toBeVisible();
    await expect(rows(page)).toHaveCount(0);
    await expect(missPanel(page)).toHaveCount(0);
    expect(seen).toEqual([]);
  });
});

test.describe('a hit', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('is one link to its article holding the Kind badge, name, Scope badge and the matched text', async ({ page }) => {
    await stubSearch(page, sample);
    await page.goto('/?q=kubernetes');
    await expect(rows(page)).toHaveCount(5);
    await expect(links(page)).toHaveCount(5);
    for (let i = 0; i < 5; i++) await expect(rows(page).nth(i).getByRole('link')).toHaveCount(1);

    const first = rows(page).first().getByRole('link');
    await expect(first).toContainText('article');
    await expect(first).toContainText('Deploying to Kubernetes');
    await expect(first).toContainText('global');
    await expect(first).toContainText('kubernetes');
    const text = (await first.innerText()).replace(/\s+/g, ' ');
    expect(text.indexOf('article'), 'Kind before name').toBeLessThan(text.indexOf('Deploying to Kubernetes'));
    expect(text.indexOf('Deploying to Kubernetes'), 'name before Scope').toBeLessThan(text.indexOf('global'));

    const org = rows(page).nth(1).getByRole('link');
    await expect(org).toContainText('org');
    await expect(org).toContainText('Structured logging');
  });

  test('links to /items/<kind>/<id> for each Kind', async ({ page }) => {
    await stubSearch(page, sample);
    await page.goto('/?q=x');
    await expect(links(page)).toHaveCount(5);
    const paths = await links(page).evaluateAll((as) => as.map((a) => new URL((a as HTMLAnchorElement).href).pathname));
    expect(paths.map((p) => decodeURIComponent(p))).toEqual([
      '/items/article/platform/deploy',
      '/items/article/platform/logging',
      '/items/skill/skill-retry',
      '/items/agent/reviewer',
      '/items/article/platform/flags',
    ]);
  });

  test('following a hit opens its route inside the shell without a page load', async ({ page }) => {
    await stubSearch(page, sample);
    await page.goto('/?q=x');
    await page.evaluate(() => ((window as unknown as { marker: number }).marker = 7));
    await links(page).first().click();
    await expect(page).toHaveURL((u) => decodeURIComponent(u.pathname) === '/items/article/platform/deploy');
    await expect(page.getByRole('banner')).toBeVisible();
    expect(await page.evaluate(() => (window as unknown as { marker?: number }).marker)).toBe(7);
  });

  test('shows the API matched text verbatim, in the API order', async ({ page }) => {
    await stubSearch(page, [{ id: 'a/b', name: 'Deploying', matched: ['kubernetes', 'name', 'Mixed Case'] }]);
    await page.goto('/?q=x');
    const text = (await links(page).first().innerText()).replace(/\s+/g, ' ');
    const at = ['kubernetes', 'name', 'Mixed Case'].map((m) => text.indexOf(m));
    expect(at.every((i) => i >= 0), `matched terms in: ${text}`).toBe(true);
    expect([...at].sort((a, b) => a - b), 'in API order').toEqual(at);
  });

  test('shows the enforced shield only on an enforced hit', async ({ page }) => {
    await stubSearch(page, sample);
    await page.goto('/?q=x');
    const shield = (i: number) =>
      rows(page).nth(i).getByRole('link').getByRole('img', { name: /enforced/i }).or(rows(page).nth(i).getByRole('link').getByText(/enforced/i));
    await expect(shield(1)).toHaveCount(1);
    await expect(shield(1)).toBeVisible();
    for (const i of [0, 2, 3, 4]) await expect(shield(i), `hit ${i}`).toHaveCount(0);
  });

  test('says "Overrides article:<name> from <scope>" when overrides is not empty, and only then', async ({ page }) => {
    await stubSearch(page, [
      ...sample.slice(0, 2),
      { id: 'platform/multi', name: 'Multi level', scope: shop, overrides: ['org', 'global'] },
    ]);
    await page.goto('/?q=x');
    const first = (await links(page).nth(0).innerText()).replace(/\s+/g, ' ');
    expect(first).toMatch(/Overrides\s+article:(Deploying to Kubernetes|platform\/deploy)\s+from\s+global/);
    const multi = (await links(page).nth(2).innerText()).replace(/\s+/g, ' ');
    expect(multi).toMatch(/Overrides\s+article:(Multi level|platform\/multi)\s+from\s+org/);
    expect(multi, 'every shadowed Scope is named').toMatch(/from\s+global/);
    expect((await links(page).nth(1).innerText()).replace(/\s+/g, ' '), 'overrides is []').not.toMatch(/Overrides/);
  });

  test('renders HTML and Markdown in the name and the matched text as text', async ({ page }) => {
    let alerted = false;
    page.on('dialog', async (d) => {
      alerted = true;
      await d.dismiss();
    });
    await stubSearch(page, [
      {
        id: 'x/evil',
        name: '**bold** <img src=x onerror=alert(1)> [click](javascript:alert(2))',
        matched: ['<script>alert(3)</script>', '# heading', '`code`'],
        enforced: false,
        overrides: [],
      },
    ]);
    await page.goto('/?q=x');
    const link = links(page).first();
    await expect(link).toHaveCount(1);
    await expect(link).toContainText('**bold** <img src=x onerror=alert(1)> [click](javascript:alert(2))');
    await expect(link).toContainText('<script>alert(3)</script>');
    await expect(link).toContainText('# heading');
    await expect(link).toContainText('`code`');
    await expect(link.locator('img, script, b, strong, em, code, h1, h2, h3, a')).toHaveCount(0);
    await expect(links(page)).toHaveCount(1);
    expect(alerted).toBe(false);
  });
});

test.describe('absent enforced and overrides (#529)', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('a hit without them shows no enforced chip, no overrides line and no error', async ({ page, baseURL }) => {
    const seen = await problems(page, baseURL ?? '');
    await stubSearch(page, [
      { id: 'platform/old', name: 'From an old server' },
      { id: 'platform/known', name: 'Known false', enforced: false, overrides: [] },
    ]);
    await page.goto('/?q=x');
    await expect(rows(page)).toHaveCount(2);
    const unknown = rows(page).first();
    await expect(unknown).toContainText('From an old server');
    await expect(unknown.getByText(/enforced/i)).toHaveCount(0);
    await expect(unknown.getByRole('img', { name: /enforced/i })).toHaveCount(0);
    expect((await unknown.innerText()).replace(/\s+/g, ' ')).not.toMatch(/Overrides/);
    await expect(main(page).getByRole('alert')).toHaveCount(0);
    expect(seen.errors).toEqual([]);
  });

  test('only one of the two absent still renders the other', async ({ page }) => {
    await stubSearch(page, [
      { id: 'platform/a', name: 'Enforced only', enforced: true },
      { id: 'platform/b', name: 'Overrides only', overrides: ['global'] },
    ]);
    await page.goto('/?q=x');
    await expect(rows(page).first().getByRole('img', { name: /enforced/i }).or(rows(page).first().getByText(/enforced/i))).toHaveCount(1);
    expect((await rows(page).first().innerText()).replace(/\s+/g, ' ')).not.toMatch(/Overrides/);
    expect((await rows(page).nth(1).innerText()).replace(/\s+/g, ' ')).toMatch(/Overrides\s+article:\S.*from\s+global/);
    await expect(rows(page).nth(1).getByText(/enforced/i)).toHaveCount(0);
    await expect(main(page).getByRole('alert')).toHaveCount(0);
  });
});

// The search call carries the Project and the Kind (#730): the API does the
// scoping and the filtering, the browser shows what it answers.
const apiProjects = /\/api\/v1\/projects$/;
const apiProjectItems = /\/api\/v1\/projects\/[^/]+\/items$/;
const billing = 'github.com/acme/billing';
const none = 'github.com/acme/none';

type Call = URLSearchParams;

// stubScoped answers like the API: a Kind narrows the hits, and a Project's copy
// of an Item replaces the global one with `overrides: ['global']`. Every request's
// parameters are recorded. A Project the test names unregistered gets the hint.
async function stubScoped(page: Page, opts: { unregistered?: string; hint?: string } = {}) {
  const calls: Call[] = [];
  await page.route(apiProjects, (route) => json(route, 200, { projects: [shop, billing] }));
  await page.route(apiProjectItems, (route) => json(route, 200, { items: [], suppressions: [] }));
  await page.route(apiSearch, (route) => {
    const p = new URL(route.request().url()).searchParams;
    calls.push(p);
    const project = p.get('project');
    const kind = p.get('kind');
    let hits = sample.filter((h) => !kind || (h.kind ?? 'article') === kind);
    if (project === shop) {
      hits = hits.map((h) => (h.id === 'platform/deploy' ? { ...h, scope: shop, name: 'Deploying to Kubernetes (shop)', overrides: ['global'] } : h));
    }
    const body: Record<string, unknown> = results(p.get('q') ?? '', hits);
    if (project && project === opts.unregistered) body.hint = opts.hint;
    return json(route, 200, body);
  });
  return calls;
}

test.describe('the Kind pills', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('are one per Kind plus All, with All pressed first', async ({ page }) => {
    await stubScoped(page);
    await page.goto('/?q=x');
    await expect(rows(page)).toHaveCount(5);
    await expect(pill(page, /all/i)).toHaveAttribute('aria-pressed', 'true');
    for (const kind of [/article/i, /skill/i, /agent/i]) await expect(pill(page, kind)).toHaveAttribute('aria-pressed', 'false');
  });

  test('a Kind pill sets kind on the API call and in the URL, and shows what the API answers', async ({ page }) => {
    const calls = await stubScoped(page);
    await page.goto('/?q=x');
    await expect(rows(page)).toHaveCount(5);
    expect(calls[0].has('kind'), 'All sends no kind').toBe(false);

    await pill(page, /skill/i).click();
    await expect(pill(page, /skill/i)).toHaveAttribute('aria-pressed', 'true');
    await expect(pill(page, /all/i)).toHaveAttribute('aria-pressed', 'false');
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).toContainText('skill-retry');
    expect(new URL(page.url()).searchParams.get('kind')).toBe('skill');
    expect(new URL(page.url()).searchParams.get('q')).toBe('x');
    expect(calls.at(-1)?.getAll('kind')).toEqual(['skill']);
    expect(calls.at(-1)?.get('q')).toBe('x');

    await pill(page, /agent/i).click();
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).toContainText('reviewer');
    expect(calls.at(-1)?.getAll('kind')).toEqual(['agent']);

    await pill(page, /all/i).click();
    await expect(rows(page)).toHaveCount(5);
    await expect(pill(page, /all/i)).toHaveAttribute('aria-pressed', 'true');
    expect(new URL(page.url()).searchParams.has('kind')).toBe(false);
    expect(calls.at(-1)?.has('kind')).toBe(false);
  });

  test('a shared /?q=&kind= link searches that Kind at once and shows its pill pressed', async ({ page }) => {
    const calls = await stubScoped(page);
    await page.goto('/?q=x&kind=skill');
    await expect(rows(page)).toHaveCount(1);
    expect(calls).toHaveLength(1);
    expect(calls[0].getAll('kind')).toEqual(['skill']);
    await expect(pill(page, /skill/i)).toHaveAttribute('aria-pressed', 'true');
    await page.reload();
    await expect(pill(page, /skill/i)).toHaveAttribute('aria-pressed', 'true');
    await expect(rows(page)).toHaveCount(1);
  });

  test('the browser no longer filters: every hit the API sends is shown whatever pill is pressed', async ({ page }) => {
    await stubSearch(page, sample);
    await page.goto('/?q=x');
    await expect(rows(page)).toHaveCount(5);
    await pill(page, /skill/i).click();
    await expect.poll(() => new URL(page.url()).searchParams.get('kind')).toBe('skill');
    await expect(rows(page)).toHaveCount(5);
  });

  test('a Kind with a Project sends both, and the Project stays in the URL', async ({ page }) => {
    const calls = await stubScoped(page);
    await page.goto(`/?q=x&project=${encodeURIComponent(shop)}`);
    await expect(rows(page)).toHaveCount(5);
    await pill(page, /skill/i).click();
    await expect(rows(page)).toHaveCount(1);
    expect(calls.at(-1)?.get('project')).toBe(shop);
    expect(calls.at(-1)?.get('kind')).toBe('skill');
    expect(new URL(page.url()).searchParams.get('project')).toBe(shop);
  });
});

test.describe('search with a selected Project', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('sends q and project, never drafts, and shows the Project copy overriding the global one', async ({ page }) => {
    const calls = await stubScoped(page);
    await page.goto(`/?q=kubernetes&project=${encodeURIComponent(shop)}`);
    await expect(rows(page)).toHaveCount(5);
    expect(calls).toHaveLength(1);
    expect(calls[0].get('q')).toBe('kubernetes');
    expect(calls[0].getAll('project')).toEqual([shop]);
    expect(calls[0].has('drafts'), 'drafts is never sent by default').toBe(false);
    const first = (await links(page).first().innerText()).replace(/\s+/g, ' ');
    expect(first).toContain('Deploying to Kubernetes (shop)');
    expect(first).toContain(shop);
    expect(first).toMatch(/Overrides\s+article:\S.*from\s+global/);
    await expect(main(page)).not.toContainText(/Deploying to Kubernetes(?! \(shop\))/);
  });

  test('without a Project sends no project parameter at all, and no drafts', async ({ page }) => {
    const calls = await stubScoped(page);
    await page.goto('/?q=kubernetes');
    await expect(rows(page)).toHaveCount(5);
    expect(calls[0].has('project'), 'a blank project is a 400').toBe(false);
    expect(calls[0].has('drafts')).toBe(false);
    expect(calls[0].has('kind')).toBe(false);
    await expect(links(page).first()).toContainText('Deploying to Kubernetes');
    expect((await links(page).first().innerText()).replace(/\s+/g, ' ')).not.toContain('(shop)');
  });

  test('a drafts parameter in the address is not passed on to the API', async ({ page }) => {
    const calls = await stubScoped(page);
    await page.goto('/?q=x&drafts=true');
    await expect(rows(page)).toHaveCount(5);
    expect(calls[0].has('drafts')).toBe(false);
  });

  test('a Project id with URL syntax in it travels as one encoded value', async ({ page }) => {
    const calls = await stubScoped(page);
    const odd = 'github.com/acme/re&po#x?kind=skill';
    await page.goto(`/?q=x&project=${encodeURIComponent(odd)}`);
    await expect(rows(page)).toHaveCount(5);
    expect(calls[0].getAll('project')).toEqual([odd]);
    expect(calls[0].has('kind'), 'the id did not smuggle a kind').toBe(false);
  });

  test('choosing a Project on the results searches again with it', async ({ page }) => {
    const calls = await stubScoped(page);
    await page.goto('/?q=kubernetes');
    await expect(rows(page)).toHaveCount(5);
    await page.locator('header').getByRole('combobox', { name: /project/i }).click();
    await page.getByRole('option').filter({ hasText: billing }).click();
    await expect.poll(() => calls.some((c) => c.get('project') === billing && c.get('q') === 'kubernetes')).toBe(true);
    expect(new URL(page.url()).searchParams.get('q')).toBe('kubernetes');
  });

  test('Home keeps the question and the one search field, and makes no search without a query', async ({ page }) => {
    const calls = await stubScoped(page);
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('What are you deciding today?');
    await expect(field(page)).toHaveCount(1);
    await expect(field(page)).toHaveAccessibleName(/.+/);
    await expect(field(page)).toHaveAttribute('placeholder', 'database, logging, retries, feature flags…');
    expect(calls, 'no query, no search').toEqual([]);
  });

  test('typing in the field with a Project selected searches it and keeps the Project in the URL', async ({ page }) => {
    const calls = await stubScoped(page);
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    await field(page).fill('database');
    await field(page).press('Enter');
    await expect(rows(page)).toHaveCount(5);
    expect(new URL(page.url()).searchParams.get('q')).toBe('database');
    expect(new URL(page.url()).searchParams.get('project')).toBe(shop);
    expect(calls.at(-1)?.get('project')).toBe(shop);
    expect(calls.at(-1)?.get('q')).toBe('database');
    await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
  });

  test('clearing the field with a Project selected drops q, keeps the Project and shows no hits', async ({ page }) => {
    await stubScoped(page);
    await page.goto(`/?q=database&project=${encodeURIComponent(shop)}`);
    await expect(rows(page)).toHaveCount(5);
    await main(page).getByRole('button', { name: /clear/i }).click();
    await expect(field(page)).toHaveValue('');
    await expect.poll(() => qParam(page)).toBeNull();
    expect(new URL(page.url()).searchParams.get('project')).toBe(shop);
    await expect(links(page).filter({ hasText: 'skill-retry' })).toHaveCount(0);
  });

  test('a ?q= of only spaces with a Project is not a search', async ({ page }) => {
    const calls = await stubScoped(page);
    await page.goto(`/?q=%20%20&project=${encodeURIComponent(shop)}`);
    await expect(field(page)).toBeVisible();
    await expect(missPanel(page)).toHaveCount(0);
    expect(calls).toEqual([]);
  });
});

test.describe('the unregistered-Project hint', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  const hint = `The Project ${none} is not registered on this server, so this is global guidance only. To register it, add a projects/${none}/ directory to one of goldie's Sources.`;
  const shown = (page: Page) => main(page).getByText('is not registered on this server');

  test('shows the API hint once, above the results, as served', async ({ page }) => {
    await stubScoped(page, { unregistered: none, hint });
    await page.goto(`/?q=x&project=${encodeURIComponent(none)}`);
    await expect(rows(page)).toHaveCount(5);
    await expect(shown(page)).toHaveCount(1);
    await expect(shown(page)).toContainText(hint);
    const above = await shown(page).evaluate((el) => {
      const ul = document.querySelector('main ul');
      return !!ul && !!(el.compareDocumentPosition(ul) & Node.DOCUMENT_POSITION_FOLLOWING);
    });
    expect(above, 'the hint comes before the hits').toBe(true);
  });

  test('stays once after the Kind changes, and is gone for a registered Project', async ({ page }) => {
    await stubScoped(page, { unregistered: none, hint });
    await page.goto(`/?q=x&project=${encodeURIComponent(none)}`);
    await expect(shown(page)).toHaveCount(1);
    await pill(page, /skill/i).click();
    await expect(rows(page)).toHaveCount(1);
    await expect(shown(page)).toHaveCount(1);
    await page.goto(`/?q=x&project=${encodeURIComponent(shop)}`);
    await expect(rows(page)).toHaveCount(5);
    await expect(main(page)).not.toContainText('not registered');
  });

  test('is shown with a Miss too, and nothing is shown without a hint', async ({ page }) => {
    await page.route(apiProjects, (route) => json(route, 200, { projects: [shop] }));
    await page.route(apiProjectItems, (route) => json(route, 200, { items: [], suppressions: [] }));
    await page.route(apiSearch, (route) => {
      const withProject = new URL(route.request().url()).searchParams.has('project');
      return json(route, 200, withProject ? { query: 'quantum', results: [], hint } : { query: 'quantum', results: [] });
    });
    await page.goto(`/?q=quantum&project=${encodeURIComponent(none)}`);
    await expect(missPanel(page)).toBeVisible();
    await expect(shown(page)).toHaveCount(1);
    await page.goto('/?q=quantum');
    await expect(missPanel(page)).toBeVisible();
    await expect(main(page)).not.toContainText('not registered');
  });

  test('is plain text: markup and links in it are not rendered or followed', async ({ page }) => {
    let alerted = false;
    page.on('dialog', async (d) => {
      alerted = true;
      await d.dismiss();
    });
    const evil = 'Register at <a href="https://evil.example/x">here</a> <img src=x onerror=alert(1)> **bold** [x](javascript:alert(2))';
    await stubScoped(page, { unregistered: none, hint: evil });
    await page.goto(`/?q=x&project=${encodeURIComponent(none)}`);
    await expect(rows(page)).toHaveCount(5);
    await expect(main(page)).toContainText(evil);
    await expect(main(page).locator('a[href*="evil.example"], img, script, strong, b')).toHaveCount(0);
    expect(alerted).toBe(false);
  });
});

test.describe('a Miss', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('shows the gold dashed role=status panel with the §8 copy and the query', async ({ page }) => {
    await stubSearch(page, []);
    await page.goto('/?q=quantum%20retries');
    const panel = missPanel(page);
    await expect(panel).toHaveCount(1);
    await expect(panel).toContainText('No golden path for “quantum retries” yet.');
    await expect(panel).toContainText('Try other words — Items are found by their keywords.');
    await expect(panel).toContainText('This search was recorded as a Miss, so the platform team knows guidance is wanted here.');
    await expect(panel.locator('strong, b').filter({ hasText: 'Miss' })).toHaveCount(1);
    await expect(panel).toHaveCSS('border-top-style', 'dashed');
    await expect(rows(page)).toHaveCount(0);
    await expect(main(page).getByRole('alert')).toHaveCount(0);
  });

  test('shows a hostile query as text, never as markup', async ({ page }) => {
    let alerted = false;
    page.on('dialog', async (d) => {
      alerted = true;
      await d.dismiss();
    });
    await stubSearch(page, []);
    await page.goto(`/?q=${encodeURIComponent('<img src=x onerror=alert(1)><b>x</b>')}`);
    await expect(missPanel(page)).toContainText('No golden path for “<img src=x onerror=alert(1)><b>x</b>” yet.');
    await expect(missPanel(page).locator('img, b b, script')).toHaveCount(0);
    expect(alerted).toBe(false);
  });

  test('is never shown for an error: the alert has the envelope message and hint, and Retry searches again', async ({ page }) => {
    let fail = true;
    await page.route(apiSearch, (route) =>
      fail ? json(route, 500, envelope('Something went wrong.', 'Try again in a moment.')) : json(route, 200, results('database', sample)),
    );
    await page.goto('/?q=database');
    const alert = main(page).getByRole('alert');
    await expect(alert).toBeVisible();
    await expect(alert).toContainText('Something went wrong.');
    await expect(alert).toContainText('Try again in a moment.');
    await expect(main(page)).not.toContainText('No golden path');
    await expect(main(page).getByRole('status').filter({ hasText: /No golden path/ })).toHaveCount(0);
    fail = false;
    await alert.getByRole('button', { name: /retry/i }).click();
    await expect(rows(page)).toHaveCount(5);
    await expect(main(page).getByRole('alert')).toHaveCount(0);
  });

  test('is never shown when the API is unreachable or answers something that is not an envelope', async ({ page }) => {
    await page.route(apiSearch, (route) => route.fulfill({ status: 502, contentType: 'text/html', body: '<pre>panic at /srv/x.go 10.0.0.7</pre>' }));
    await page.goto('/?q=database');
    await expect(main(page).getByRole('alert')).toBeVisible();
    await expect(main(page)).not.toContainText('No golden path');
    await expect(main(page)).not.toContainText(/panic|\/srv|10\.0\.0\.7/);

    await page.unroute(apiSearch);
    await page.route(apiSearch, (route) => route.abort('connectionrefused'));
    await page.goto('/?q=other');
    await expect(main(page).getByRole('alert')).toBeVisible();
    await expect(main(page)).not.toContainText('No golden path');
  });
});

test.describe('rapid typing', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  // The first search answers late, after the second one has been shown. Its
  // answer must never overwrite the newer one, nor flip the Miss and error
  // panels, nor the URL.
  test('a slow older response never overwrites a newer one', async ({ page }) => {
    let slowDone = false;
    await page.route(apiSearch, async (route) => {
      const q = new URL(route.request().url()).searchParams.get('q');
      if (q === 'slow') {
        await new Promise((resolve) => setTimeout(resolve, 1500));
        await json(route, 200, results('slow', [{ id: 'old/stale', name: 'Stale answer' }])).catch(() => {});
        slowDone = true;
        return;
      }
      await json(route, 200, results('fast', [{ id: 'new/fresh', name: 'Fresh answer' }]));
    });
    await page.goto('/');
    await field(page).fill('slow');
    await field(page).press('Enter');
    await field(page).fill('fast');
    await field(page).press('Enter');
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).toContainText('Fresh answer');
    await expect.poll(() => slowDone, { timeout: 5000 }).toBe(true);
    await page.evaluate(() => new Promise((resolve) => setTimeout(resolve, 200)));
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).toContainText('Fresh answer');
    await expect(main(page)).not.toContainText('Stale answer');
    expect(qParam(page)).toBe('fast');
    await expect(field(page)).toHaveValue('fast');
  });

  test('a slow older Miss never replaces newer hits', async ({ page }) => {
    let slowDone = false;
    await page.route(apiSearch, async (route) => {
      const q = new URL(route.request().url()).searchParams.get('q');
      if (q === 'nothing') {
        await new Promise((resolve) => setTimeout(resolve, 1500));
        await json(route, 200, results('nothing', [])).catch(() => {});
        slowDone = true;
        return;
      }
      await json(route, 200, results('database', sample));
    });
    await page.goto('/');
    await field(page).fill('nothing');
    await field(page).press('Enter');
    await field(page).fill('database');
    await field(page).press('Enter');
    await expect(rows(page)).toHaveCount(5);
    await expect.poll(() => slowDone, { timeout: 5000 }).toBe(true);
    await page.evaluate(() => new Promise((resolve) => setTimeout(resolve, 200)));
    await expect(rows(page)).toHaveCount(5);
    await expect(missPanel(page)).toHaveCount(0);
  });

  test('a slow older error never replaces newer hits', async ({ page }) => {
    let slowDone = false;
    await page.route(apiSearch, async (route) => {
      const q = new URL(route.request().url()).searchParams.get('q');
      if (q === 'boom') {
        await new Promise((resolve) => setTimeout(resolve, 1500));
        await json(route, 500, envelope('Something went wrong.', 'Try again in a moment.')).catch(() => {});
        slowDone = true;
        return;
      }
      await json(route, 200, results('database', sample));
    });
    await page.goto('/');
    await field(page).fill('boom');
    await field(page).press('Enter');
    await field(page).fill('database');
    await field(page).press('Enter');
    await expect(rows(page)).toHaveCount(5);
    await expect.poll(() => slowDone, { timeout: 5000 }).toBe(true);
    await page.evaluate(() => new Promise((resolve) => setTimeout(resolve, 200)));
    await expect(rows(page)).toHaveCount(5);
    await expect(main(page).getByRole('alert')).toHaveCount(0);
  });

  test('going back to Home while a search is in flight never lets its answer appear', async ({ page }) => {
    let done = false;
    await page.route(apiSearch, async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 1000));
      await json(route, 200, results('late', sample)).catch(() => {});
      done = true;
    });
    await page.goto('/');
    await field(page).fill('late');
    await field(page).press('Enter');
    await main(page).getByRole('button', { name: /clear/i }).click();
    await expect.poll(() => done, { timeout: 5000 }).toBe(true);
    await page.evaluate(() => new Promise((resolve) => setTimeout(resolve, 200)));
    await expect(rows(page)).toHaveCount(0);
    await expect(missPanel(page)).toHaveCount(0);
    await expect(field(page)).toHaveValue('');
  });

  // The auth guard asks the session before every navigation, so a search's
  // navigation can land after the user has typed on. It must not put the
  // submitted words back into the field over the new ones.
  test('a late navigation never overwrites what was typed since', async ({ page }) => {
    await stubSearch(page, sample);
    await page.goto('/');
    let release = () => {};
    const held = new Promise<void>((resolve) => (release = resolve));
    let holding = true;
    await page.route('**/api/v1/session', async (route) => {
      if (holding && route.request().method() === 'GET') {
        holding = false;
        await held;
      }
      await route.continue().catch(() => {});
    });
    await field(page).fill('first');
    await field(page).press('Enter');
    await field(page).fill('second');
    release();
    await expect.poll(() => qParam(page)).toBe('first');
    await expect(rows(page)).toHaveCount(5);
    await expect(field(page)).toHaveValue('second');
    await field(page).press('Enter');
    await expect.poll(() => qParam(page)).toBe('second');
    await expect(field(page)).toHaveValue('second');
  });
});
