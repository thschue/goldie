// The Decisions page at /decisions (#736, handoff §3.8): the decisions from
// GET /api/v1/decisions?project=&status= for the selected Project. The API is
// stubbed per test (page.route) so every state is deterministic.
//
// Contract these specs pin down for the implementation:
// - a decision is a role=article in <main>; its title is a level 2 heading; the
//   article shows the status, the date (YYYY-MM-DD as sent) and the Scope as text;
// - the status filter is a role=group named "Status" of buttons (aria-pressed),
//   one per status: accepted (the default, pressed), proposed, superseded,
//   deprecated, rejected and All. A button's name starts with its label (a count
//   may follow). Pressing one requests the list with status=<value> (All is
//   status=all); the default request sends no status or status=accepted;
// - every request carries project=<the URL's project> and none when the URL has none;
// - states follow #587: an error is a role=alert showing the envelope message, the
//   empty state is a role=status, the loading skeletons carry aria-busy="true".
import { expect, test, type Page, type Route } from '@playwright/test';
import { eachViewport, evidence } from './ux';

const shop = 'github.com/acme/shop';
const apiProjects = /\/api\/v1\/projects$/;
const apiDecisions = /\/api\/v1\/decisions(\?.*)?$/;

type Decision = {
  name: string;
  title: string;
  status?: 'proposed' | 'accepted' | 'rejected' | 'deprecated' | 'superseded';
  date?: string;
  scope?: string;
  supersedes?: string[];
  superseded_by?: string;
};

const decision = (d: Decision) => ({
  status: 'accepted',
  date: '2023-05-02',
  scope: 'global',
  supersedes: [],
  superseded_by: '',
  keywords: [],
  ...d,
});

const json = (route: Route, status: number, body: unknown, headers: Record<string, string> = {}) =>
  route.fulfill({ status, contentType: 'application/json', headers, body: JSON.stringify(body) });

const envelope = (code: string, message: string, hint: string) => ({ error: { code, message, details: { hint } } });

const stubProjects = (page: Page) => page.route(apiProjects, (route) => json(route, 200, { projects: [shop] }));

const sample: Decision[] = [
  { name: 'use-postgres', title: 'Use PostgreSQL for the shop database', date: '2023-05-02', scope: 'global' },
  { name: 'retry-budget', title: 'Cap retries at three per request', date: '2024-01-17', scope: shop },
];

const byStatus: Record<string, Decision[]> = {
  accepted: sample,
  proposed: [{ name: 'adopt-grpc', title: 'Adopt gRPC between services', status: 'proposed', date: '2024-03-09' }],
  superseded: [
    {
      name: 'use-mysql',
      title: 'Use MySQL for the shop database',
      status: 'superseded',
      date: '2021-02-11',
      superseded_by: 'decision:use-postgres',
    },
  ],
  deprecated: [{ name: 'soap-api', title: 'Expose a SOAP API', status: 'deprecated', date: '2019-08-30' }],
  rejected: [{ name: 'use-mongo', title: 'Use MongoDB for orders', status: 'rejected', date: '2022-06-21' }],
};

// stubDecisions answers by the status in the query and records every request URL.
const stubDecisions = async (page: Page) => {
  const urls: URL[] = [];
  await page.route(apiDecisions, (route) => {
    const url = new URL(route.request().url());
    urls.push(url);
    const status = url.searchParams.get('status') ?? 'accepted';
    const list = status === 'all' ? Object.values(byStatus).flat() : (byStatus[status] ?? []);
    return json(route, 200, { decisions: list.map(decision) });
  });
  return urls;
};

const main = (page: Page) => page.getByRole('main');
const cards = (page: Page) => main(page).getByRole('article');
const card = (page: Page, title: string) =>
  cards(page).filter({ has: page.getByRole('heading', { level: 2, name: title, exact: true }) });
const filter = (page: Page) => main(page).getByRole('group', { name: 'Status' });
const pill = (page: Page, label: string) => filter(page).getByRole('button', { name: new RegExp(`^${label}`, 'i') });
const open = (page: Page, project: string | null = shop) =>
  page.goto(project === null ? '/decisions' : `/decisions?project=${encodeURIComponent(project)}`);

eachViewport((width, theme) => {
  test('the Decisions list is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await stubDecisions(page);
    await open(page);
    await expect(cards(page)).toHaveCount(2);
    await evidence(page, width, theme, '', 'decisions');
  });

  test('the Decisions error state is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiDecisions, (route) =>
      json(route, 500, envelope('internal', 'The decision store is unavailable.', 'Try again in a moment.')),
    );
    await open(page);
    await expect(main(page).getByRole('alert')).toBeVisible();
    await evidence(page, width, theme, '-error', 'decisions');
  });

  test('the Decisions loading skeletons are captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiDecisions, async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 4000));
      await json(route, 200, { decisions: [] }).catch(() => {});
    });
    await open(page);
    await expect(main(page).locator('[aria-busy="true"]')).toBeVisible();
    await evidence(page, width, theme, '-loading', 'decisions');
  });

  test('the Decisions empty state is captured and axe-clean', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiDecisions, (route) => json(route, 200, { decisions: [] }));
    await open(page);
    await expect(main(page).getByRole('status')).toContainText(/no .*decisions/i);
    await evidence(page, width, theme, '-empty', 'decisions');
  });
});

test.describe('the Decisions page', () => {
  test('lists the accepted decisions with title, status, date and Scope, and no placeholder', async ({ page }) => {
    await stubProjects(page);
    const urls = await stubDecisions(page);
    await open(page);
    await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
    await expect(cards(page)).toHaveCount(2);
    const pg = card(page, 'Use PostgreSQL for the shop database');
    await expect(pg).toContainText('accepted');
    await expect(pg).toContainText('2023-05-02');
    await expect(pg).toContainText('global');
    const retry = card(page, 'Cap retries at three per request');
    await expect(retry).toContainText('accepted');
    await expect(retry).toContainText('2024-01-17');
    await expect(retry).toContainText(shop);
    await expect(main(page).getByRole('status')).toHaveCount(0);
    await expect(main(page).getByRole('alert')).toHaveCount(0);
    expect(urls.length).toBeGreaterThan(0);
    for (const u of urls) expect([null, 'accepted']).toContain(u.searchParams.get('status'));
  });

  test('keeps project from the URL on the request and on the page', async ({ page }) => {
    await stubProjects(page);
    const urls = await stubDecisions(page);
    await open(page);
    await expect(cards(page)).toHaveCount(2);
    expect(urls.every((u) => u.searchParams.get('project') === shop)).toBe(true);
    await expect(page).toHaveURL((u) => u.pathname === '/decisions' && u.searchParams.get('project') === shop);
    await pill(page, 'proposed').click();
    await expect(cards(page)).toHaveCount(1);
    await expect(page).toHaveURL((u) => u.pathname === '/decisions' && u.searchParams.get('project') === shop);
    expect(urls.every((u) => u.searchParams.get('project') === shop)).toBe(true);
  });

  test('sends no project when the URL has none', async ({ page }) => {
    await stubProjects(page);
    const urls = await stubDecisions(page);
    await open(page, null);
    await expect(cards(page)).toHaveCount(2);
    expect(urls.every((u) => !u.searchParams.has('project'))).toBe(true);
  });

  test('a decision title is text, never markup', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiDecisions, (route) =>
      json(route, 200, {
        decisions: [decision({ name: 'x', title: '<img src=x onerror="window.__pwn=1"> Use <b>bold</b>' })],
      }),
    );
    await open(page);
    await expect(cards(page)).toHaveCount(1);
    await expect(cards(page).first()).toContainText('<b>bold</b>');
    await expect(main(page).locator('img[src="x"]')).toHaveCount(0);
    expect(await page.evaluate(() => (window as unknown as { __pwn?: number }).__pwn)).toBeUndefined();
  });

  test('shows skeletons with aria-busy while a slow load runs, and no spinner', async ({ page }) => {
    await stubProjects(page);
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => (release = resolve));
    await page.route(apiDecisions, async (route) => {
      await gate;
      await json(route, 200, { decisions: sample.map(decision) });
    });
    await open(page);
    await expect(main(page).locator('[aria-busy="true"]')).toBeVisible();
    await expect(cards(page)).toHaveCount(0);
    await expect(main(page).getByRole('status')).toHaveCount(0);
    expect(await page.locator('[role="progressbar"], [class*="spinner" i]').count(), 'no spinner').toBe(0);
    release();
    await expect(cards(page)).toHaveCount(2);
    await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
  });

  test('an empty list shows the empty state, not an error and not a list', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiDecisions, (route) => json(route, 200, { decisions: [] }));
    await open(page);
    await expect(main(page).getByRole('status')).toContainText(/no .*decisions/i);
    await expect(main(page).getByRole('alert')).toHaveCount(0);
    await expect(cards(page)).toHaveCount(0);
    await expect(main(page).locator('[aria-busy="true"]')).toHaveCount(0);
  });

  test('an API error shows the envelope message in an alert and no list', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiDecisions, (route) =>
      json(route, 503, envelope('not_ready', 'The decision store is unavailable.', 'Try again in a moment.')),
    );
    await open(page);
    const alert = main(page).getByRole('alert');
    await expect(alert).toContainText('The decision store is unavailable.');
    await expect(cards(page)).toHaveCount(0);
    await expect(main(page).getByRole('status')).toHaveCount(0);
    await expect(main(page).locator('[aria-busy="true"]')).toHaveCount(0);
  });

  test('a 400 for the project shows the envelope message', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiDecisions, (route) =>
      json(route, 400, envelope('invalid_argument', 'project is not a valid project id.', 'Use host/org/repo.')),
    );
    await open(page, 'not a project');
    await expect(main(page).getByRole('alert')).toContainText('project is not a valid project id.');
    await expect(cards(page)).toHaveCount(0);
  });

  test('an error never shows more than the envelope: no stack, path or address', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiDecisions, (route) =>
      route.fulfill({
        status: 502,
        contentType: 'text/html',
        body: '<pre>panic at /srv/goldie/x.go:12 10.0.0.7:8080</pre>',
      }),
    );
    await open(page);
    const alert = main(page).getByRole('alert');
    await expect(alert).toBeVisible();
    await expect(alert).not.toContainText('panic');
    await expect(alert).not.toContainText('10.0.0.7');
    await expect(alert).not.toContainText('/srv/');
  });

  test('the status filter defaults to accepted and offers every other lifecycle', async ({ page }) => {
    await stubProjects(page);
    await stubDecisions(page);
    await open(page);
    await expect(cards(page)).toHaveCount(2);
    for (const label of ['accepted', 'proposed', 'superseded', 'deprecated', 'rejected', 'All']) {
      await expect(pill(page, label)).toHaveCount(1);
    }
    await expect(pill(page, 'accepted')).toHaveAttribute('aria-pressed', 'true');
    await expect(pill(page, 'proposed')).toHaveAttribute('aria-pressed', 'false');
  });

  test('choosing a status requests it and lists those decisions', async ({ page }) => {
    await stubProjects(page);
    const urls = await stubDecisions(page);
    await open(page);
    await expect(cards(page)).toHaveCount(2);
    for (const [status, title, date] of [
      ['proposed', 'Adopt gRPC between services', '2024-03-09'],
      ['superseded', 'Use MySQL for the shop database', '2021-02-11'],
      ['deprecated', 'Expose a SOAP API', '2019-08-30'],
      ['rejected', 'Use MongoDB for orders', '2022-06-21'],
    ]) {
      await pill(page, status).click();
      await expect(cards(page)).toHaveCount(1);
      await expect(card(page, title)).toContainText(status);
      await expect(card(page, title)).toContainText(date);
      await expect(pill(page, status)).toHaveAttribute('aria-pressed', 'true');
      await expect(pill(page, 'accepted')).toHaveAttribute('aria-pressed', 'false');
      expect(urls.at(-1)?.searchParams.get('status')).toBe(status);
    }
  });

  test('All requests status=all and lists every lifecycle; accepted returns to the default list', async ({ page }) => {
    await stubProjects(page);
    const urls = await stubDecisions(page);
    await open(page);
    await expect(cards(page)).toHaveCount(2);
    await pill(page, 'All').click();
    await expect(cards(page)).toHaveCount(6);
    expect(urls.at(-1)?.searchParams.get('status')).toBe('all');
    await pill(page, 'accepted').click();
    await expect(cards(page)).toHaveCount(2);
    await expect(card(page, 'Adopt gRPC between services')).toHaveCount(0);
  });

  test('a status with no decisions shows the empty state and keeps the filter usable', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiDecisions, (route) => {
      const status = new URL(route.request().url()).searchParams.get('status') ?? 'accepted';
      return json(route, 200, { decisions: status === 'accepted' ? sample.map(decision) : [] });
    });
    await open(page);
    await expect(cards(page)).toHaveCount(2);
    await pill(page, 'rejected').click();
    await expect(main(page).getByRole('status')).toBeVisible();
    await expect(cards(page)).toHaveCount(0);
    await pill(page, 'accepted').click();
    await expect(cards(page)).toHaveCount(2);
    await expect(main(page).getByRole('status')).toHaveCount(0);
  });

  test('a failed status request shows the error and the filter can recover', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiDecisions, (route) => {
      const status = new URL(route.request().url()).searchParams.get('status') ?? 'accepted';
      if (status === 'proposed') {
        return json(route, 500, envelope('internal', 'Could not read proposed decisions.', 'Retry.'));
      }
      return json(route, 200, { decisions: sample.map(decision) });
    });
    await open(page);
    await expect(cards(page)).toHaveCount(2);
    await pill(page, 'proposed').click();
    await expect(main(page).getByRole('alert')).toContainText('Could not read proposed decisions.');
    await expect(cards(page)).toHaveCount(0);
    await pill(page, 'accepted').click();
    await expect(cards(page)).toHaveCount(2);
    await expect(main(page).getByRole('alert')).toHaveCount(0);
  });
});
