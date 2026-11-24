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
// - #737 (handoff §2.2, §3.8): a decision is selected by `&d=<name>` on /decisions. The page
//   fetches GET /api/v1/decisions/<name> (with project=<the URL's project> when it has one) and
//   shows the decision in a second column beside the list, under it when narrow. Its Markdown
//   body is rendered by MarkdownBody (never raw HTML), the history is an <ol> inside main (the
//   cards are a <ul>) whose current entry has aria-current="step", and the binding note is
//   plain text in main that is not a role=status. A card's heading is what a click targets.
//   After a filter change exactly one role=status in main carries the result count
//   ("N decisions", "1 decision"); the initial load has none;
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
    // #737: a filter change is announced with the count; the empty state is gone.
    await expect(main(page).getByRole('status')).toContainText(/\b2 decisions\b/);
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

// #783: an error box closes a message lacking . ? or ! before its hint (handoff §8), as the
// article page does (#759).
test.describe('the Decisions list error: message then hint (#783)', () => {
  const norm = (s: string) => s.replace(/\s+/g, ' ').trim();
  const fail = async (page: Page, message: string, hint: string) => {
    await stubProjects(page);
    await page.route(apiDecisions, (route) => json(route, 503, envelope('not_ready', message, hint)));
    await open(page);
    return main(page).getByRole('alert');
  };

  test('a message without a closing period gets one before the hint', async ({ page }) => {
    const alert = await fail(page, 'The decision store is unavailable', 'Try again in a moment.');
    await expect(alert).toBeVisible();
    expect(norm(await alert.locator('p').first().innerText())).toBe('The decision store is unavailable. Try again in a moment.');
  });

  test('a message already ending in a question mark gets nothing extra before the hint', async ({ page }) => {
    const alert = await fail(page, 'Is the decision store down?', 'Try again in a moment.');
    await expect(alert).toBeVisible();
    expect(norm(await alert.locator('p').first().innerText())).toBe('Is the decision store down? Try again in a moment.');
  });

  test('a message without a period and without a hint is rendered unchanged', async ({ page }) => {
    const alert = await fail(page, 'The decision store is unavailable', '');
    await expect(alert).toBeVisible();
    expect(norm(await alert.locator('p').first().innerText())).toBe('The decision store is unavailable');
  });
});

// #737: the selected decision (handoff §2.2, §3.8, §8).
const apiDecision = /\/api\/v1\/decisions\/[^/?]+(\?.*)?$/;
const bindingNote =
  'Binding for agents. A change that contradicts this decision should stop and propose a new decision instead of silently deviating.';

const norm = (s: string) => s.replace(/\s+/g, ' ').trim();

type Detail = Decision & { content?: string; chain?: { name: string; title: string; status: string; scope: string }[] };

// stubDecision answers GET /api/v1/decisions/<name> from `details` (a miss is a 404 envelope)
// and records every request URL.
const stubDecision = async (page: Page, details: Detail[]) => {
  const urls: URL[] = [];
  await page.route(apiDecision, (route) => {
    const url = new URL(route.request().url());
    urls.push(url);
    const name = decodeURIComponent(url.pathname.split('/').pop() ?? '');
    const d = details.find((x) => x.name === name);
    if (!d) {
      return json(route, 404, envelope('not_found', `No decision named ${name}.`, 'List the decisions to find the name.'));
    }
    return json(route, 200, { ...decision(d), content: d.content ?? '', chain: d.chain ?? [] });
  });
  return urls;
};

const postgres: Detail = {
  name: 'use-postgres',
  title: 'Use PostgreSQL for the shop database',
  content: '## Context\n\nBODYMARK we need **transactions**.',
  keywords: ['database', 'sql'],
} as Detail;
const select = (page: Page, d: string, project: string | null = shop) =>
  page.goto(`/decisions?${project === null ? '' : `project=${encodeURIComponent(project)}&`}d=${encodeURIComponent(d)}`);
const body = (page: Page) => main(page).getByText('BODYMARK');

test.describe('a selected decision (#737)', () => {
  test('d in the URL fetches the decision for the Project and shows its Markdown body', async ({ page }) => {
    await stubProjects(page);
    await stubDecisions(page);
    const urls = await stubDecision(page, [postgres]);
    await select(page, 'use-postgres');
    await expect(body(page)).toBeVisible();
    await expect(main(page).locator('strong', { hasText: 'transactions' })).toBeVisible();
    await expect(main(page).getByRole('heading', { level: 2, name: 'Context' })).toBeVisible();
    expect(urls.length).toBeGreaterThan(0);
    for (const u of urls) {
      expect(u.pathname).toBe('/api/v1/decisions/use-postgres');
      expect(u.searchParams.get('project')).toBe(shop);
    }
  });

  test('clicking a card sets d and keeps the Project', async ({ page }) => {
    await stubProjects(page);
    await stubDecisions(page);
    await stubDecision(page, [postgres]);
    await open(page);
    await expect(cards(page)).toHaveCount(2);
    await expect(body(page)).toHaveCount(0);
    await card(page, 'Use PostgreSQL for the shop database').getByRole('heading', { level: 2 }).click();
    await expect(page).toHaveURL((u) => u.pathname === '/decisions' && u.searchParams.get('d') === 'use-postgres');
    await expect(page).toHaveURL((u) => u.searchParams.get('project') === shop);
    await expect(body(page)).toBeVisible();
  });

  test('the selected decision sits beside the list when wide and under it when narrow', async ({ page }) => {
    await stubProjects(page);
    await stubDecisions(page);
    await stubDecision(page, [postgres]);
    for (const [width, height] of [
      [1280, 800],
      [375, 812],
    ]) {
      await page.setViewportSize({ width, height });
      await select(page, 'use-postgres');
      await expect(body(page)).toBeVisible();
      const list = (await card(page, 'Cap retries at three per request').boundingBox())!;
      const sel = (await body(page).boundingBox())!;
      if (width === 1280) {
        expect(sel.x, 'beside the list').toBeGreaterThanOrEqual(list.x + list.width - 1);
      } else {
        expect(sel.y, 'under the list').toBeGreaterThanOrEqual(list.y + list.height - 1);
      }
    }
  });

  test('a hostile body renders as inert content, never raw HTML', async ({ page }) => {
    const hostile = [
      'BODYMARK before <script>window.__pwn = "script"</script> after.',
      '',
      '<img src=x onerror="window.__pwn = \'img\'">',
      '',
      '<iframe src="https://evil.example/frame"></iframe>',
      '',
      '<a href="javascript:window.__pwn = \'raw\'">raw anchor</a>',
      '',
      '[md js link](javascript:window.__pwn = "md")',
      '',
      '[safe link](https://example.org/safe)',
    ].join('\n');
    await stubProjects(page);
    await stubDecisions(page);
    await stubDecision(page, [{ ...postgres, content: hostile }]);
    await select(page, 'use-postgres');
    await expect(body(page)).toBeVisible();
    await expect(main(page).locator('img[src="x"], iframe, script, style')).toHaveCount(0);
    await expect(main(page).locator('a[href^="javascript:" i]')).toHaveCount(0);
    await expect(main(page).getByRole('link', { name: 'safe link' })).toHaveAttribute('href', 'https://example.org/safe');
    await page.waitForTimeout(300);
    expect(await page.evaluate(() => (window as unknown as { __pwn?: string }).__pwn)).toBeUndefined();
  });

  test('an accepted decision shows the binding note; a proposed one does not', async ({ page }) => {
    await stubProjects(page);
    await stubDecisions(page);
    await stubDecision(page, [
      postgres,
      { name: 'adopt-grpc', title: 'Adopt gRPC between services', status: 'proposed', content: 'BODYMARK grpc' } as Detail,
    ]);
    await select(page, 'use-postgres');
    await expect(body(page)).toBeVisible();
    expect(norm(await main(page).innerText())).toContain(bindingNote);
    await select(page, 'adopt-grpc');
    await expect(main(page).getByText('BODYMARK grpc')).toBeVisible();
    expect(norm(await main(page).innerText())).not.toContain('should stop and propose a new decision');
  });

  test('the history lists the chain oldest to newest with the current one marked', async ({ page }) => {
    const chain = [
      { name: 'use-mysql', title: 'Use MySQL for the shop database', status: 'superseded', scope: 'global' },
      { name: 'use-postgres', title: 'Use PostgreSQL for the shop database', status: 'superseded', scope: 'global' },
      { name: 'use-cockroach', title: 'Use CockroachDB for the shop database', status: 'accepted', scope: 'global' },
    ];
    await stubProjects(page);
    await stubDecisions(page);
    await stubDecision(page, [{ ...postgres, status: 'superseded', superseded_by: 'decision:use-cockroach', chain }]);
    await select(page, 'use-postgres');
    await expect(body(page)).toBeVisible();
    const items = main(page).locator('ol li');
    await expect(items).toHaveCount(3);
    await expect(items.nth(0)).toContainText('Use MySQL for the shop database');
    await expect(items.nth(1)).toContainText('Use PostgreSQL for the shop database');
    await expect(items.nth(2)).toContainText('Use CockroachDB for the shop database');
    await expect(items.nth(0)).toContainText('superseded');
    await expect(items.nth(2)).toContainText('accepted');
    await expect(main(page).locator('ol [aria-current="step"]')).toHaveCount(1);
    await expect(items.nth(1).locator('xpath=self::*[@aria-current="step"] | .//*[@aria-current="step"]')).toHaveCount(1);
  });

  test('a superseded decision links to its successor', async ({ page }) => {
    await stubProjects(page);
    await page.route(apiDecisions, (route) =>
      json(route, 200, {
        decisions: [
          decision({
            name: 'use-mysql',
            title: 'Use MySQL for the shop database',
            status: 'superseded',
            superseded_by: 'decision:use-postgres',
          }),
        ],
      }),
    );
    await stubDecision(page, [
      {
        name: 'use-mysql',
        title: 'Use MySQL for the shop database',
        status: 'superseded',
        superseded_by: 'decision:use-postgres',
        content: 'BODYMARK mysql',
      } as Detail,
      postgres,
    ]);
    await select(page, 'use-mysql');
    await expect(main(page).getByText('BODYMARK mysql')).toBeVisible();
    const link = main(page).locator('a[href*="d=use-postgres"]');
    await expect(link.first()).toBeVisible();
    await link.first().click();
    await expect(body(page)).toBeVisible();
    await expect(page).toHaveURL((u) => u.searchParams.get('d') === 'use-postgres');
  });

  test('facts show the keywords the API serves and invent no source, path, format or number', async ({ page }) => {
    await stubProjects(page);
    await stubDecisions(page);
    await stubDecision(page, [postgres]);
    await select(page, 'use-postgres');
    await expect(body(page)).toBeVisible();
    await expect(main(page).getByText('database', { exact: true })).toBeVisible();
    await expect(main(page).getByText('sql', { exact: true })).toBeVisible();
    const text = norm(await main(page).innerText());
    expect(text).not.toMatch(/file path|format|madr|nygard|frontmatter|\bADR-\d+|\d{4}\/\d{4}/i);
    expect(text).not.toMatch(/\bSource\s*:/i);
  });

  test('an unknown name shows the API message and hint in an alert', async ({ page }) => {
    await stubProjects(page);
    await stubDecisions(page);
    await stubDecision(page, [postgres]);
    await select(page, 'no-such-decision');
    const alert = main(page).getByRole('alert');
    await expect(alert).toContainText('No decision named no-such-decision.');
    await expect(alert).toContainText('List the decisions to find the name.');
    await expect(body(page)).toHaveCount(0);
  });

  test('changing the filter announces the result count politely', async ({ page }) => {
    await stubProjects(page);
    await stubDecisions(page);
    await open(page);
    await expect(cards(page)).toHaveCount(2);
    await expect(main(page).getByRole('status')).toHaveCount(0);
    await pill(page, 'proposed').click();
    await expect(cards(page)).toHaveCount(1);
    const status = main(page).getByRole('status');
    await expect(status).toHaveCount(1);
    await expect(status).toContainText(/\b1 decision\b(?!s)/);
    expect(await status.getAttribute('aria-live')).not.toBe('assertive');
    await pill(page, 'All').click();
    await expect(cards(page)).toHaveCount(6);
    await expect(main(page).getByRole('status')).toContainText(/\b6 decisions\b/);
  });
});
