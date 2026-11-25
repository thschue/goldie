// The activity feed on the local stream (#630, slice 2 of #22; handoff §3.6, §5.1,
// §5.3, §9). `/ops/activity` reads GET /api/v1/activity (server-sent events, the
// contract's ActivityEvent, ActivityDropped and ActivityLive) through api() and
// shows a table. The stream is stubbed in the page: an init script replaces
// window.fetch for that one path with a ReadableStream the spec feeds frame by
// frame (page.route cannot send a body in pieces). The error states use page.route.
// The throwaway ux token is a reader, so the operator specs stub GET /api/v1/session.
//
// Contract these specs pin down for the implementation (the issue leaves them open):
// - the page is one role=table in main with seven th scope=col headers, in the order
//   Time, Event, Query → Item, Result, Via, Project, Replica; a row is a tr of the
//   tbody and its cells are its direct children, in the same order;
// - the stream is the merged one: GET /api/v1/activity?replicas=all (#631);
// - a row's action is a button in its Time cell with aria-pressed; the pressed one
//   is the selected event;
// - "Misses only" is a checkbox or a button named /misses only/i; Pause is a button
//   named Pause that becomes one named Resume;
// - the miss count is a role=status in main whose text holds the word miss or
//   misses (not "missed") and the count as a number;
// - the header is the text "Live" alone for one replica (#809), and "Live · merged from all
//   replicas (n of m answering)" for several; the replica's name is in the table's Replica column;
// - the h1 is "Activity".
// Not pinned: the row cap (only that it is below the 10,000 events a replica's
// history can hold, and that the newest rows are the ones kept), and the time format.
// #810 adds (see the specs at the end): the Via cell shows the surface, the Result cell the served
// Item or "No Item matched", the detail adds Query → Item, Latency, Via and Trace id, and the
// surface pills and the Latency tile.
import { expect, test, type Locator, type Page, type Route } from '@playwright/test';
import { eachViewport, evidence, expectFocusRing, problems, sideNav } from './ux';

type Ev = {
  id: number;
  time: string;
  kind: 'search' | 'lookup' | 'get';
  query: string;
  project: string;
  token: string;
  client_version: string;
  trace_id: string;
  miss: boolean;
  replica: string;
  late: boolean;
  via?: string;
  served?: string;
  latency_us?: number;
};

// An ActivityEvent as the API sends it: every field present.
const ev = (id: number, o: Partial<Ev> = {}): Ev => ({
  id,
  time: new Date().toISOString(),
  kind: 'search',
  query: `query ${id}`,
  project: '',
  token: 'ci',
  client_version: '1.5.0',
  trace_id: '',
  miss: false,
  replica: 'goldie-0',
  late: false,
  ...o,
});

const frame = (e: Ev) => `data: ${JSON.stringify(e)}\n\n`;
const liveMarker = 'event: live\ndata: {}\n\n';
const droppedFrame = (n: number) => `event: dropped\ndata: ${JSON.stringify({ dropped: n })}\n\n`;

const json = (route: Route, status: number, body: unknown) =>
  route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

const envelope = (code: string, message: string, hint: string) => ({ error: { code, message, details: { hint } } });

type Hook = { urls: string[]; controllers: ReadableStreamDefaultController<Uint8Array>[]; aborted: number };
type Win = Window & { __activity: Hook; __send: (text: string) => void; __pwned?: number };

// stubStream answers the page's own fetch of the activity path with a stream the
// spec controls. Call it before goto.
async function stubStream(page: Page) {
  await page.addInitScript(() => {
    const w = window as unknown as Win;
    const hook: Hook = { urls: [], controllers: [], aborted: 0 };
    w.__activity = hook;
    const enc = new TextEncoder();
    const real = window.fetch.bind(window);
    window.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
      const raw = input instanceof Request ? input.url : String(input);
      const url = new URL(raw, location.href);
      if (url.pathname !== '/api/v1/activity') return real(input, init);
      hook.urls.push(url.pathname + url.search);
      const body = new ReadableStream<Uint8Array>({
        start(c) {
          hook.controllers.push(c);
          init?.signal?.addEventListener('abort', () => {
            hook.aborted++;
            try {
              c.error(new DOMException('aborted', 'AbortError'));
            } catch {
              // already closed
            }
          });
        },
      });
      return Promise.resolve(new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } }));
    };
    w.__send = (text: string) => {
      const c = hook.controllers[hook.controllers.length - 1];
      c.enqueue(enc.encode(text));
    };
  });
}

// links is the operator-only part of the Session (#807): grafana_url and trace_url_template.
type Links = { grafana_url?: string; trace_url_template?: string };
const operator = (page: Page, links: Links = {}) =>
  page.route('**/api/v1/session', (route) =>
    route.request().method() === 'GET' ? json(route, 200, { name: 'ops', role: 'operator', ...links }) : route.continue(),
  );

const send = (page: Page, text: string) => page.evaluate((t) => (window as unknown as Win).__send(t), text);

// open starts the page as an operator, waits for the stream request, and sends the
// history and the live marker.
async function open(page: Page, history: Ev[] = [], path = '/ops/activity', links: Links = {}) {
  await operator(page, links);
  await stubStream(page);
  await page.goto(path);
  await page.waitForFunction(() => (window as unknown as Win).__activity.controllers.length > 0);
  await send(page, history.map(frame).join('') + liveMarker);
}

const main = (page: Page) => page.getByRole('main');
const h1 = (page: Page) => page.getByRole('heading', { level: 1 });
const table = (page: Page) => main(page).getByRole('table');
const rows = (page: Page) => table(page).locator('tbody tr');
const rowOf = (page: Page, text: string | RegExp) => rows(page).filter({ hasText: text });
const cell = (row: Locator, i: number) => row.locator(':scope > td, :scope > th').nth(i);
const col = { time: 0, event: 1, query: 2, result: 3, via: 4, project: 5, replica: 6 } as const;
const missesOnly = (page: Page) =>
  main(page)
    .getByRole('checkbox', { name: /misses only/i })
    .or(main(page).getByRole('button', { name: /misses only/i }));
const pause = (page: Page) => main(page).getByRole('button', { name: 'Pause', exact: true });
const resume = (page: Page) => main(page).getByRole('button', { name: 'Resume', exact: true });
// The feed's own miss count; the Live stream stage summary is also a role=status that mentions misses.
const missStatus = (page: Page) => main(page).locator('p.misses[role="status"]');
const text = async (l: Locator) => ((await l.innerText()) ?? '').replace(/\s+/g, ' ').trim();

// mainTextOutsideTable is what a person reads in main besides the table.
const mainTextOutsideTable = (page: Page) =>
  main(page).evaluate((el) => {
    const copy = el.cloneNode(true) as HTMLElement;
    copy.querySelectorAll('table').forEach((t) => t.remove());
    return (copy.innerText ?? copy.textContent ?? '').replace(/\s+/g, ' ');
  });

const sample: Ev[] = [
  ev(1, { query: 'database', project: 'github.com/acme/shop' }),
  ev(2, { kind: 'lookup', query: 'platform/logging', project: 'github.com/acme/shop' }),
  ev(3, { kind: 'search', query: 'quantum', miss: true, project: 'github.com/acme/api', replica: 'goldie-0' }),
  ev(4, { kind: 'get', query: 'skill:review' }),
  ev(5, { kind: 'get', query: 'agent:review' }),
  ev(6, { kind: 'get', query: 'skill:ghost', miss: true }),
  ev(7, { kind: 'get', query: 'agent:ghost', miss: true }),
  ev(8, { kind: 'lookup', query: 'platform/ghost', miss: true }),
];

const emptyCopy = /No requests yet\. Run\s+`?goldie search`?\s+or ask your agent something\./;
const filteredCopy = 'Nothing matches these filters in the last five minutes.';
const droppedCopy = 'Some events were missed because this page fell behind.';

eachViewport((width, theme) => {
  test('the feed with rows is captured and axe-clean', async ({ page }) => {
    await open(page, [
      ...sample,
      ev(9, { query: 'a-very-long-query-without-spaces-'.repeat(8), project: 'github.com/acme/' + 'long-'.repeat(12) }),
      ev(10, { query: 'late one', late: true }),
    ]);
    await expect(rows(page)).toHaveCount(10);
    await evidence(page, width, theme, '-rows', 'activity');
  });

  test('the empty feed is captured and axe-clean', async ({ page }) => {
    await open(page);
    await expect(main(page)).toContainText(emptyCopy);
    await evidence(page, width, theme, '-empty', 'activity');
  });

  test('the dropped notice is captured and axe-clean', async ({ page }) => {
    await open(page, sample);
    await send(page, droppedFrame(3));
    await expect(main(page)).toContainText(droppedCopy);
    await evidence(page, width, theme, '-dropped', 'activity');
  });

  test('the reader state is captured and axe-clean', async ({ page }) => {
    await page.route('**/api/v1/activity*', (route) =>
      json(route, 403, envelope('forbidden', 'this token cannot do that', 'Use an operator token.')),
    );
    await page.goto('/ops/activity');
    await expect(main(page)).toContainText('This page is for operators.');
    await evidence(page, width, theme, '-reader', 'activity');
  });

  test('the too-many-streams state is captured and axe-clean', async ({ page }) => {
    await operator(page);
    await page.route('**/api/v1/activity*', (route) =>
      json(route, 503, envelope('too_many_streams', 'too many activity streams are open', 'Close another stream, or retry in a minute.')),
    );
    await page.goto('/ops/activity');
    await expect(main(page)).toContainText('Close another stream, or retry in a minute.');
    await evidence(page, width, theme, '-503', 'activity');
  });
});

test.describe('the table', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('is one real table with seven column headers in order, under the h1 Activity', async ({ page }) => {
    await open(page, sample);
    await expect(h1(page)).toHaveText('Activity');
    await expect(main(page).getByRole('table')).toHaveCount(1);
    const heads = table(page).locator('thead th');
    await expect(heads).toHaveCount(7);
    await expect(heads.locator('xpath=self::th[@scope="col"]')).toHaveCount(7);
    const names = (await heads.allInnerTexts()).map((t) => t.replace(/\s+/g, ' ').trim());
    expect(names[0]).toBe('Time');
    expect(names[1]).toBe('Event');
    expect(names[2]).toMatch(/^Query\s*→\s*Item$/);
    expect(names.slice(3)).toEqual(['Result', 'Via', 'Project', 'Replica']);
    await expect(rows(page)).toHaveCount(sample.length);
  });

  test('puts the event, project and replica of an event in its row, and a miss reads Miss in words', async ({ page }) => {
    await open(page, sample);
    const hitRow = rowOf(page, 'database');
    await expect(cell(hitRow, col.event)).toContainText(/search/i);
    await expect(cell(hitRow, col.project)).toHaveText('github.com/acme/shop');
    await expect(cell(hitRow, col.replica)).toHaveText('goldie-0');
    expect(await text(cell(hitRow, col.result)), 'a served row is not a Miss').not.toMatch(/miss/i);
    expect((await text(cell(hitRow, col.result))).length, 'a served row says what it was').toBeGreaterThan(0);
    await expect(cell(rowOf(page, 'platform/logging'), col.event)).toContainText(/lookup/i);
    await expect(cell(rowOf(page, 'skill:review'), col.event)).toContainText(/get/i);
    await expect(cell(rowOf(page, 'quantum'), col.result), 'colour is not the only sign of a Miss').toContainText(/miss/i);
  });

  test('is fed by the merged stream of all replicas', async ({ page }) => {
    await open(page, sample);
    const urls = await page.evaluate(() => (window as unknown as Win).__activity.urls);
    expect(urls.length).toBeGreaterThan(0);
    for (const u of urls) {
      expect(u).toMatch(/^\/api\/v1\/activity\?/);
      expect(new URL(u, 'http://x').searchParams.getAll('replicas'), 'replicas=all, once').toEqual(['all']);
    }
  });
});

test.describe('the Reference and the query', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('is shown as sent: skill:x, agent:x, or a bare name from an older replica', async ({ page }) => {
    await open(page, [
      ev(1, { kind: 'get', query: 'skill:review' }),
      ev(2, { kind: 'get', query: 'agent:planner' }),
      ev(3, { kind: 'get', query: 'builder' }),
    ]);
    const skill = await text(cell(rowOf(page, 'skill:review'), col.query));
    expect(skill).toContain('skill:review');
    expect(skill).not.toContain('agent:');
    const agent = await text(cell(rowOf(page, 'agent:planner'), col.query));
    expect(agent).toContain('agent:planner');
    expect(agent).not.toContain('skill:');
    const bare = await text(cell(rowOf(page, 'builder'), col.query));
    expect(bare).toContain('builder');
    expect(bare, 'a bare name stays bare').not.toMatch(/(skill|agent|article):builder/);
  });

  test('renders query text as text, never as HTML', async ({ page, baseURL }) => {
    const seen = await problems(page, baseURL ?? '');
    const hostile = '<img src=x onerror="window.__pwned=1"><script>window.__pwned=2</script><b>bold</b>';
    const rtl = 'deploy ‮evil‬ ​zero';
    await open(page, [ev(1, { query: hostile }), ev(2, { query: rtl })]);
    await expect(rows(page)).toHaveCount(2);
    await expect(cell(rowOf(page, '<img'), col.query)).toContainText(hostile);
    await expect(table(page).locator('img, script, b, iframe')).toHaveCount(0);
    expect(await page.evaluate(() => (window as unknown as Win).__pwned), 'the markup ran').toBeUndefined();
    expect(seen.errors, 'page errors and CSP violations').toEqual([]);
    expect(seen.external).toEqual([]);
  });
});

test.describe('Misses only, Pause and Resume, and the cap', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('Misses only keeps the miss rows and turning it off brings the rest back', async ({ page }) => {
    await open(page, sample);
    await expect(rows(page)).toHaveCount(8);
    await missesOnly(page).click();
    await expect(rows(page)).toHaveCount(4);
    for (const q of ['quantum', 'skill:ghost', 'agent:ghost', 'platform/ghost']) {
      await expect(rowOf(page, q)).toHaveCount(1);
    }
    await expect(rowOf(page, 'database')).toHaveCount(0);
    await send(page, frame(ev(9, { query: 'new hit' })) + frame(ev(10, { query: 'new miss', miss: true })));
    await expect(rowOf(page, 'new miss')).toHaveCount(1);
    await expect(rowOf(page, 'new hit'), 'a hit does not show under Misses only').toHaveCount(0);
    await missesOnly(page).click();
    await expect(rows(page)).toHaveCount(10);
  });

  test('Pause stops new rows and Resume continues', async ({ page }) => {
    await open(page, [ev(1, { query: 'before' })]);
    await expect(rows(page)).toHaveCount(1);
    await pause(page).click();
    await expect(resume(page)).toBeVisible();
    await expect(pause(page)).toHaveCount(0);
    await send(page, frame(ev(2, { query: 'while paused' })));
    await page.waitForTimeout(400);
    await expect(rowOf(page, 'while paused'), 'no new row while paused').toHaveCount(0);
    await expect(rows(page)).toHaveCount(1);
    await resume(page).click();
    await expect(pause(page)).toBeVisible();
    await send(page, frame(ev(3, { query: 'after resume' })));
    await expect(rowOf(page, 'after resume')).toHaveCount(1);
  });

  test('caps the rows kept, below what a replica history holds, and keeps the newest', async ({ page }) => {
    test.setTimeout(120_000);
    await open(page);
    await page.evaluate(() => {
      const w = window as unknown as Win;
      const now = new Date().toISOString();
      let out = '';
      for (let id = 1; id <= 10_500; id++) {
        const e = { id, time: now, kind: 'search', query: `bulk ${id}`, project: '', token: 'ci', client_version: '1.5.0', trace_id: '', miss: false, replica: 'goldie-0', late: false };
        out += `data: ${JSON.stringify(e)}\n\n`;
      }
      w.__send(out);
    });
    await expect(rowOf(page, 'bulk 10500')).toHaveCount(1, { timeout: 60_000 });
    const n = await rows(page).count();
    expect(n, 'some rows are kept').toBeGreaterThan(0);
    expect(n, 'the rows kept are capped').toBeLessThan(10_000);
    await expect(
      rows(page).filter({ has: page.getByText('bulk 1', { exact: true }) }),
      'the oldest rows are the ones dropped',
    ).toHaveCount(0);
  });
});

test.describe('the dropped notice', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('says the page fell behind when the server reports dropped events, and not before', async ({ page }) => {
    await open(page, sample);
    await expect(main(page)).not.toContainText(droppedCopy);
    await send(page, droppedFrame(3) + frame(ev(9, { query: 'after the gap' })));
    await expect(main(page)).toContainText(droppedCopy);
    await expect(rowOf(page, 'after the gap'), 'the stream carries on').toHaveCount(1);
  });
});

test.describe('the empty states', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('an empty stream says how to make a request, with no table rows', async ({ page }) => {
    await open(page);
    await expect(main(page)).toContainText(emptyCopy);
    await expect(rows(page)).toHaveCount(0);
    await expect(main(page)).not.toContainText(filteredCopy);
  });

  test('an empty result after filtering has its own copy, and the empty copy is not shown', async ({ page }) => {
    await open(page, [ev(1, { query: 'database' }), ev(2, { query: 'logging' })]);
    await missesOnly(page).click();
    await expect(rows(page)).toHaveCount(0);
    await expect(main(page)).toContainText(filteredCopy);
    await expect(main(page)).not.toContainText(emptyCopy);
    await missesOnly(page).click();
    await expect(rows(page)).toHaveCount(2);
    await expect(main(page)).not.toContainText(filteredCopy);
  });

  test('a row arriving after the empty copy replaces it', async ({ page }) => {
    await open(page);
    await expect(main(page)).toContainText(emptyCopy);
    await send(page, frame(ev(1, { query: 'first' })));
    await expect(rowOf(page, 'first')).toHaveCount(1);
    await expect(main(page)).not.toContainText(emptyCopy);
  });
});

test.describe('a miss row (#576)', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  // The detail's text without the Reference the person typed, so only the copy the page adds is
  // judged. The row's Result cell says "No Item matched" (#810); what was asked for is said here.
  const copyOf = async (page: Page, query: string) => {
    await rowButton(page, query).click();
    return (await text(detail(page))).split(query).join('');
  };

  test('speaks of the kind that was asked for: a skill, an agent or an article', async ({ page }) => {
    await open(page, sample);
    const skill = await copyOf(page, 'skill:ghost');
    expect(skill, 'a skill miss names a skill').toMatch(/skill/i);
    expect(skill, 'a skill miss is not about an article').not.toMatch(/article/i);
    const agent = await copyOf(page, 'agent:ghost');
    expect(agent, 'an agent miss names an agent').toMatch(/agent/i);
    expect(agent, 'an agent miss is not about an article').not.toMatch(/article/i);
    const article = await copyOf(page, 'platform/ghost');
    expect(article, 'an article lookup miss names an article').toMatch(/article/i);
    const search = await copyOf(page, 'quantum');
    expect(search, 'a search miss is not about a skill or an agent').not.toMatch(/skill|agent/i);
  });

  test('offers no article action for a skill or an agent miss, and no link to an Item', async ({ page }) => {
    await open(page, sample);
    for (const q of ['skill:ghost', 'agent:ghost']) {
      expect(await text(rowOf(page, q)), q).not.toMatch(/start this article|source repo/i);
      await expect(rowOf(page, q).getByRole('link')).toHaveCount(0);
    }
  });
});

test.describe('who may see it', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('a reader (403) sees that the page is for operators, and no table or raw envelope', async ({ page }) => {
    await page.route('**/api/v1/activity*', (route) =>
      json(route, 403, envelope('forbidden', 'this token cannot do that', 'Use an operator token.')),
    );
    await page.goto('/ops/activity');
    await expect(h1(page)).toHaveText('Activity');
    await expect(main(page)).toContainText('This page is for operators.');
    await expect(table(page)).toHaveCount(0);
    expect(await text(main(page))).not.toMatch(/forbidden|\{|"error"/);
  });

  test('a 401 ends the session visibly and the page stays', async ({ page }) => {
    await operator(page);
    await page.route('**/api/v1/activity*', (route) =>
      json(route, 401, envelope('unauthorized', 'missing or invalid API token', 'Send a configured API token.')),
    );
    await page.goto('/ops/activity');
    await expect(page.getByText('Your session ended.')).toBeVisible();
    await expect(page.getByRole('link', { name: 'Sign in again' })).toBeVisible();
    await expect(page).toHaveURL((u) => u.pathname === '/ops/activity');
    await expect(h1(page)).toHaveText('Activity');
  });

  test('a 503 too_many_streams shows its hint, not the raw envelope or the code', async ({ page }) => {
    await operator(page);
    const hint = 'Close another stream, or retry in a minute.';
    await page.route('**/api/v1/activity*', (route) =>
      json(route, 503, envelope('too_many_streams', 'too many activity streams are open', hint)),
    );
    await page.goto('/ops/activity');
    await expect(main(page)).toContainText(hint);
    expect(await text(main(page))).not.toMatch(/too_many_streams|\{|"error"/);
    await expect(table(page)).toHaveCount(0);
  });
});

test.describe('live updates and focus', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('the miss count is a polite status that changes at most every 30 seconds', async ({ page }) => {
    await page.clock.install();
    await open(page);
    const status = missStatus(page);
    await expect(status).toHaveCount(1);
    await expect(status).toHaveAttribute('role', 'status');
    expect(await status.getAttribute('aria-live'), 'polite, never assertive').not.toBe('assertive');
    await send(page, frame(ev(1, { query: 'm1', miss: true })));
    await expect(rowOf(page, 'm1')).toHaveCount(1);
    await send(page, frame(ev(2, { query: 'm2', miss: true })));
    await send(page, frame(ev(3, { query: 'm3', miss: true })));
    await expect(rowOf(page, 'm3')).toHaveCount(1);
    await page.clock.fastForward(10_000);
    expect(await text(status), 'the count is not re-announced inside 30 s').not.toMatch(/\b3\b/);
    await page.clock.fastForward(21_000);
    await expect(status).toContainText(/\b3\b/);
  });

  test('new rows never take focus, not from the h1 after navigation and not from a control', async ({ page }) => {
    await operator(page);
    await stubStream(page);
    await page.goto('/');
    await sideNav(page).getByRole('link', { name: 'Activity', exact: true }).click();
    await expect(h1(page)).toHaveText('Activity');
    await expect(h1(page), 'the h1 takes focus after navigation').toBeFocused();
    await expect(h1(page)).toHaveAttribute('tabindex', '-1');
    await page.waitForFunction(() => (window as unknown as Win).__activity.controllers.length > 0);
    await send(page, frame(ev(1, { query: 'first' })) + liveMarker);
    await expect(rowOf(page, 'first')).toHaveCount(1);
    await expect(h1(page), 'rows arriving do not move focus off the h1').toBeFocused();

    await pause(page).focus();
    await send(page, frame(ev(2, { query: 'second' })));
    await page.waitForTimeout(300);
    await expect(pause(page), 'a row arriving does not take focus from Pause').toBeFocused();
    await expect(rowOf(page, 'second')).toHaveCount(1);
    await missesOnly(page).focus();
    await send(page, frame(ev(3, { query: 'third' })));
    await expect(rowOf(page, 'third')).toHaveCount(1);
    await expect(missesOnly(page), 'a row arriving does not take focus from Misses only').toBeFocused();
  });

  test('keeps the selected row by event id while newer rows arrive', async ({ page }) => {
    const a = 'github.com/acme/a';
    const b = 'github.com/acme/b';
    await open(page, [ev(1, { query: 'same words', project: a }), ev(2, { query: 'same words', project: b })]);
    const pressed = (project: string) =>
      rows(page)
        .filter({ hasText: project })
        .locator('button[aria-pressed]');
    await pressed(a).click();
    await expect(pressed(a)).toHaveAttribute('aria-pressed', 'true');
    await expect(pressed(b)).toHaveAttribute('aria-pressed', 'false');
    await send(page, [3, 4, 5].map((id) => frame(ev(id, { query: `newer ${id}`, project: 'github.com/acme/c' }))).join(''));
    await expect(rowOf(page, 'newer 5')).toHaveCount(1);
    await expect(rows(page)).toHaveCount(5);
    await expect(pressed(a), 'the selection stays on event 1, not on the row at its old position').toHaveAttribute('aria-pressed', 'true');
    await expect(pressed(b)).toHaveAttribute('aria-pressed', 'false');
    await expect(rows(page).locator('button[aria-pressed="true"]')).toHaveCount(1);
  });
});

test.describe('keyboard and focus ring', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('the controls and a row button show the focus ring when reached with the keyboard', async ({ page }) => {
    await open(page, [ev(1, { query: 'database' })]);
    for (const target of [pause(page), missesOnly(page), rows(page).first().locator('button')]) {
      await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
      let reached = false;
      for (let i = 0; i < 40 && !reached; i++) {
        await page.keyboard.press('Tab');
        reached = await target.evaluate((el) => el === document.activeElement);
      }
      expect(reached, 'the control is in the tab order').toBe(true);
      await expectFocusRing(target);
    }
  });
});

test.describe('touch targets at 375 px', () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test('Pause, Misses only and a row button are at least 44 px', async ({ page }) => {
    await open(page, sample);
    for (const l of [pause(page), missesOnly(page), rows(page).first().locator('button')]) {
      const box = await l.boundingBox();
      expect(box, 'the control is on screen').not.toBeNull();
      expect(box!.width).toBeGreaterThanOrEqual(44);
      expect(box!.height).toBeGreaterThanOrEqual(44);
    }
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow, 'the table scrolls inside the page, which does not').toBeLessThanOrEqual(0);
  });
});

// The replica switcher and the partial-view banner (#631, slice 3 of #22). The stream
// is `replicas=all`; a peer going down or coming back arrives as an SSE event of type
// `replica_status` (the contract's ActivityReplicaStatus).
//
// Contract these specs pin down (the issue leaves them open):
// - the switcher is buttons with aria-pressed outside the table; "All replicas" is
//   one, each replica seen is another whose text holds its name, its status in words
//   (up or down) and its event count as a number;
// - with no peers there is no "All replicas" and no switcher button: the header is just "Live" (#809);
// - the banner is text in main that starts "Partial view." and names the replica;
// - a late row carries the word LATE in its row.
const status = (replica: string, up: boolean, ago = 120_000) =>
  `event: replica_status\ndata: ${JSON.stringify({ replica, up, last_seen: new Date(Date.now() - ago).toISOString() })}\n\n`;

const switcherButtons = (page: Page) => main(page).locator('button[aria-pressed]:not(table button)');
const allReplicas = (page: Page) => switcherButtons(page).filter({ hasText: /all replicas/i });
const replicaButton = (page: Page, name: string) => switcherButtons(page).filter({ hasText: name });
const banner = (page: Page) => main(page).getByText(/Partial view\./);

const fleet: Ev[] = [
  ev(1, { query: 'from zero a', replica: 'goldie-0' }),
  ev(2, { query: 'from zero b', replica: 'goldie-0' }),
  ev(3, { query: 'from one a', replica: 'goldie-1' }),
];

eachViewport((width, theme) => {
  test('the switcher and the partial-view banner are captured and axe-clean', async ({ page }) => {
    await open(page, fleet);
    await send(page, status('goldie-1', false));
    await expect(banner(page)).toBeVisible();
    await expect(switcherButtons(page)).toHaveCount(3);
    await evidence(page, width, theme, '-replicas', 'activity');
  });
});

test.describe('the replica switcher', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('has All replicas and one button per replica, with status and count, and filters the rows', async ({ page }) => {
    await open(page, fleet);
    await expect(switcherButtons(page)).toHaveCount(3);
    await expect(allReplicas(page)).toHaveAttribute('aria-pressed', 'true');
    await expect(allReplicas(page)).toContainText(/\b3\b/);
    const zero = replicaButton(page, 'goldie-0');
    const one = replicaButton(page, 'goldie-1');
    await expect(zero).toHaveAttribute('aria-pressed', 'false');
    await expect(zero).toContainText(/\bup\b/i);
    await expect(zero).toContainText(/\b2\b/);
    await expect(one).toContainText(/\bup\b/i);
    await expect(one).toContainText(/\b1\b/);
    await expect(rows(page)).toHaveCount(3);

    await one.click();
    await expect(one).toHaveAttribute('aria-pressed', 'true');
    await expect(allReplicas(page)).toHaveAttribute('aria-pressed', 'false');
    await expect(zero).toHaveAttribute('aria-pressed', 'false');
    await expect(rows(page)).toHaveCount(1);
    await expect(rowOf(page, 'from one a')).toHaveCount(1);

    await send(
      page,
      frame(ev(4, { query: 'new from zero', replica: 'goldie-0' })) + frame(ev(5, { query: 'new from one', replica: 'goldie-1' })),
    );
    await expect(rowOf(page, 'new from one')).toHaveCount(1);
    await expect(rowOf(page, 'new from zero'), 'the other replica stays filtered out').toHaveCount(0);
    await expect(one).toContainText(/\b2\b/);
    await expect(zero).toContainText(/\b3\b/);

    await allReplicas(page).click();
    await expect(allReplicas(page)).toHaveAttribute('aria-pressed', 'true');
    await expect(rows(page)).toHaveCount(5);
  });

  test('shows a replica that is down as down, in words, and up again when it is back', async ({ page }) => {
    await open(page, fleet);
    await send(page, status('goldie-1', false));
    await expect(replicaButton(page, 'goldie-1')).toContainText(/\bdown\b/i);
    await expect(replicaButton(page, 'goldie-0')).toContainText(/\bup\b/i);
    await send(page, status('goldie-1', true, 0));
    await expect(replicaButton(page, 'goldie-1')).toContainText(/\bup\b/i);
    await expect(replicaButton(page, 'goldie-1')).not.toContainText(/\bdown\b/i);
  });

  test('with no peers collapses to a header that reads just Live', async ({ page }) => {
    await open(page, [ev(1, { query: 'alone' }), ev(2, { query: 'still alone' })]);
    await expect(rows(page)).toHaveCount(2);
    await expect(switcherButtons(page)).toHaveCount(0);
    await expect(main(page).getByText(/all replicas/i)).toHaveCount(0);
    await expect(main(page).getByText('Live', { exact: true })).toBeVisible();
    expect(await mainTextOutsideTable(page)).not.toContain('goldie-0');
  });

  test('renders a replica name as text, never as HTML', async ({ page }) => {
    const hostile = '<img src=x onerror="window.__pwned=1">';
    await open(page, [ev(1, { query: 'a', replica: 'goldie-0' }), ev(2, { query: 'b', replica: hostile })]);
    await expect(rows(page)).toHaveCount(2);
    await expect(main(page).locator('img')).toHaveCount(0);
    expect(await page.evaluate(() => (window as unknown as Win).__pwned), 'the markup ran').toBeUndefined();
  });

  test('its buttons show the focus ring with the keyboard', async ({ page }) => {
    await open(page, fleet);
    const target = replicaButton(page, 'goldie-0');
    let reached = false;
    for (let i = 0; i < 40 && !reached; i++) {
      await page.keyboard.press('Tab');
      reached = await target.evaluate((el) => el === document.activeElement);
    }
    expect(reached, 'the replica button is in the tab order').toBe(true);
    await expectFocusRing(target);
  });
});

test.describe('the partial-view banner', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('appears while a replica is down, names it and how long, and goes away when it is back', async ({ page }) => {
    await open(page, fleet);
    await expect(banner(page), 'no banner while every replica answers').toHaveCount(0);
    await send(page, status('goldie-1', false, 120_000));
    await expect(banner(page)).toBeVisible();
    const copy = await mainTextOutsideTable(page);
    expect(copy).toMatch(/Partial view\.\s*goldie-1 stopped answering \S.*\bago\b\s*[—-]\s*its newer events are missing until it'?s back\./);
    expect(copy, 'the time is a number of units').toMatch(/stopped answering [^.]*\d/);
    await send(page, status('goldie-1', true, 0));
    await expect(banner(page)).toHaveCount(0);
  });

  test('shows for a replica that was already down when the stream opened', async ({ page }) => {
    await operator(page);
    await stubStream(page);
    await page.goto('/ops/activity');
    await page.waitForFunction(() => (window as unknown as Win).__activity.controllers.length > 0);
    await send(page, status('goldie-1', false) + frame(ev(1, { query: 'only zero' })) + liveMarker);
    await expect(banner(page)).toBeVisible();
    await expect(main(page)).toContainText('goldie-1 stopped answering');
    await expect(rowOf(page, 'only zero')).toHaveCount(1);
  });

  test('names every replica that is down', async ({ page }) => {
    await open(page, [...fleet, ev(4, { query: 'from two', replica: 'goldie-2' })]);
    await send(page, status('goldie-1', false) + status('goldie-2', false));
    await expect(main(page)).toContainText('goldie-1 stopped answering');
    await expect(main(page)).toContainText('goldie-2 stopped answering');
    await send(page, status('goldie-1', true, 0));
    await expect(main(page)).not.toContainText('goldie-1 stopped answering');
    await expect(main(page)).toContainText('goldie-2 stopped answering');
  });
});

test.describe('late events and equal ids', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('a late event carries a LATE marker in words and an on-time one does not', async ({ page }) => {
    await open(page, [ev(1, { query: 'on time', replica: 'goldie-0' }), ev(2, { query: 'behind', replica: 'goldie-1', late: true })]);
    expect(await text(rowOf(page, 'behind'))).toMatch(/\bLATE\b/i);
    expect(await text(rowOf(page, 'on time'))).not.toMatch(/\bLATE\b/i);
  });

  test('equal ids from two replicas both show', async ({ page }) => {
    await open(page, [ev(7, { query: 'zero seven', replica: 'goldie-0' }), ev(7, { query: 'one seven', replica: 'goldie-1' })]);
    await expect(rows(page)).toHaveCount(2);
    await expect(rowOf(page, 'zero seven')).toHaveCount(1);
    await expect(rowOf(page, 'one seven')).toHaveCount(1);
    await send(page, frame(ev(8, { query: 'zero eight', replica: 'goldie-0' })) + frame(ev(8, { query: 'one eight', replica: 'goldie-1' })));
    await expect(rows(page)).toHaveCount(4);
  });

  test('selecting a row whose id another replica shares selects only that row', async ({ page }) => {
    await open(page, [ev(7, { query: 'zero seven', replica: 'goldie-0' }), ev(7, { query: 'one seven', replica: 'goldie-1' })]);
    const pressed = (q: string) => rowOf(page, q).locator('button[aria-pressed]');
    await pressed('zero seven').click();
    await expect(pressed('zero seven')).toHaveAttribute('aria-pressed', 'true');
    await expect(pressed('one seven')).toHaveAttribute('aria-pressed', 'false');
  });
});

test.describe('the switcher at 375 px', () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test('its buttons are at least 44 px and the page does not scroll sideways', async ({ page }) => {
    await open(page, fleet);
    for (const l of [allReplicas(page), replicaButton(page, 'goldie-0'), replicaButton(page, 'goldie-1')]) {
      const box = await l.boundingBox();
      expect(box, 'the button is on screen').not.toBeNull();
      expect(box!.width).toBeGreaterThanOrEqual(44);
      expect(box!.height).toBeGreaterThanOrEqual(44);
    }
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });
});

// The Miss board and the event detail panel (#632, slice 4 of #22; handoff §3.6 items 5
// and 6, §8). Both read the feed's events, so there is no new request.
//
// Contract these specs pin down (the issue leaves them open):
// - the Miss board is a region named "What people looked for and didn't find" in main,
//   outside the table; each card is a plain button (no aria-pressed: the replica switcher
//   of #631 is the buttons with aria-pressed outside the table, and its specs count them)
//   whose text holds the query and the count as "×<n>"; misses are grouped by query;
// - selecting a card selects the latest event of its group, so the row of that event
//   becomes the pressed row button;
// - the detail panel is a region named "Event detail" in main, outside the table; absent
//   until a row or card is selected; its fields are labelled in words (Project, Token,
//   Replica) and a field the event does not have is left out, label and all;
// - "Try this search" is a link or a button; it goes to the search at `/?q=<query>`;
// - the Source repo copy is text in the panel (its target is not pinned).
const board = (page: Page) => main(page).getByRole('region', { name: "What people looked for and didn't find" });
const cards = (page: Page) => board(page).getByRole('button');
const cardOf = (page: Page, q: string) => cards(page).filter({ hasText: q });
const detail = (page: Page) => main(page).getByRole('region', { name: /event detail/i });
const trySearch = (page: Page) =>
  detail(page)
    .getByRole('link', { name: /try this search/i })
    .or(detail(page).getByRole('button', { name: /try this search/i }));
const rowButton = (page: Page, text: string | RegExp) => rowOf(page, text).locator('button').first();

const at = (id: number) => new Date(Date.now() - (100 - id) * 1000).toISOString();
const missing = (id: number, q: string, o: Partial<Ev> = {}) => ev(id, { query: q, miss: true, time: at(id), ...o });

// five misses with counts 5, 4, 3, 2, 1 and a query that was served ten times
const crowd: Ev[] = (() => {
  const out: Ev[] = [];
  let id = 0;
  for (const [q, n] of [['alpha', 5], ['bravo', 4], ['charlie', 3], ['delta', 2], ['echo', 1]] as const) {
    for (let i = 0; i < n; i++) out.push(missing(++id, q));
  }
  for (let i = 0; i < 10; i++) out.push(ev(++id, { query: 'served-often', time: at(id) }));
  return out;
})();

eachViewport((width, theme) => {
  test('the Miss board and the open detail panel are captured and axe-clean', async ({ page }) => {
    await open(page, [
      ...sample,
      missing(20, 'quantum', { project: 'github.com/acme/api', token: 'ci' }),
      missing(21, '<b>bold</b> & more', { kind: 'lookup' }),
    ]);
    await expect(board(page)).toBeVisible();
    await cardOf(page, 'quantum').click();
    await expect(detail(page)).toBeVisible();
    await evidence(page, width, theme, '-miss-board', 'activity');
  });
});

test.describe('the Miss board', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('shows up to four cards for the most frequent misses, most frequent first, with their count', async ({ page }) => {
    await open(page, crowd);
    await expect(board(page).getByRole('heading', { name: "What people looked for and didn't find" })).toBeVisible();
    await expect(cards(page)).toHaveCount(4);
    const names = (await cards(page).allInnerTexts()).map((t) => t.replace(/\s+/g, ' ').trim());
    expect(names[0]).toContain('alpha');
    expect(names[0]).toMatch(/×\s*5/);
    expect(names[1]).toContain('bravo');
    expect(names[1]).toMatch(/×\s*4/);
    expect(names[2]).toContain('charlie');
    expect(names[3]).toContain('delta');
    await expect(cardOf(page, 'echo'), 'the fifth miss does not fit').toHaveCount(0);
    await expect(cardOf(page, 'served-often'), 'a served query is not a miss').toHaveCount(0);
    await expect(cards(page).locator('xpath=self::*[@aria-pressed]'), 'cards are not switcher buttons').toHaveCount(0);
    await expect(board(page).locator('table')).toHaveCount(0);
  });

  test('groups the misses of one query into one card and follows the feed as misses arrive', async ({ page }) => {
    await open(page, [missing(1, 'quantum'), missing(2, 'quantum', { project: 'github.com/acme/b' })]);
    await expect(cards(page)).toHaveCount(1);
    await expect(cards(page).first()).toContainText(/×\s*2/);
    await send(page, frame(missing(3, 'quantum')) + frame(missing(4, 'other')));
    await expect(cards(page)).toHaveCount(2);
    await expect(cardOf(page, 'quantum')).toContainText(/×\s*3/);
  });

  test('selecting a card selects the latest event of its group and opens the detail panel', async ({ page }) => {
    await open(page, [
      missing(1, 'quantum', { project: 'github.com/acme/first' }),
      missing(2, 'quantum', { project: 'github.com/acme/second' }),
      missing(3, 'quantum', { project: 'github.com/acme/third' }),
      ev(4, { query: 'database', time: at(4) }),
    ]);
    await expect(detail(page), 'nothing is selected yet').toHaveCount(0);
    await cardOf(page, 'quantum').click();
    await expect(detail(page)).toContainText('github.com/acme/third');
    await expect(detail(page)).not.toContainText('github.com/acme/first');
    await expect(rowButton(page, 'github.com/acme/third')).toHaveAttribute('aria-pressed', 'true');
    await expect(rows(page).locator('button[aria-pressed="true"]')).toHaveCount(1);
  });

  test('renders a hostile query as text, never as HTML', async ({ page, baseURL }) => {
    const seen = await problems(page, baseURL ?? '');
    const hostile = '<img src=x onerror="window.__pwned=1"><script>window.__pwned=2</script><b>bold</b>';
    await open(page, [missing(1, hostile)]);
    await expect(cards(page)).toHaveCount(1);
    await expect(cards(page).first()).toContainText(hostile);
    await cards(page).first().click();
    await expect(detail(page)).toContainText(hostile);
    await expect(main(page).locator('img, script, b, iframe')).toHaveCount(0);
    expect(await page.evaluate(() => (window as unknown as Win).__pwned), 'the markup ran').toBeUndefined();
    expect(seen.errors, 'page errors and CSP violations').toEqual([]);
    expect(seen.external).toEqual([]);
  });
});

test.describe('the event detail panel', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('selecting a row opens it, shows the event and moves with the selection', async ({ page }) => {
    await open(page, [
      ev(1, { query: 'database', project: 'github.com/acme/shop', token: 'ci-token', replica: 'goldie-0', time: at(1) }),
      missing(2, 'quantum', { project: 'github.com/acme/api', token: 'ops-token', replica: 'goldie-1' }),
    ]);
    await expect(detail(page)).toHaveCount(0);
    await rowButton(page, 'database').click();
    await expect(detail(page)).toBeVisible();
    await expect(detail(page)).toContainText('database');
    await expect(detail(page)).toContainText('github.com/acme/shop');
    await expect(detail(page)).toContainText('ci-token');
    await expect(detail(page)).toContainText('goldie-0');
    expect(await text(detail(page)), 'a served event is not a miss').not.toMatch(/miss|source repo/i);
    await expect(table(page).getByRole('region')).toHaveCount(0);
    await rowButton(page, 'quantum').click();
    await expect(detail(page)).toContainText('quantum');
    await expect(detail(page)).toContainText('ops-token');
    await expect(detail(page)).toContainText('goldie-1');
    await expect(detail(page)).not.toContainText('ci-token');
    await expect(detail(page)).toContainText(/miss/i);
  });

  test('shows only the fields the event has', async ({ page }) => {
    await open(page, [
      ev(1, { query: 'full', project: 'github.com/acme/shop', token: 'ci-token', time: at(1) }),
      ev(2, { query: 'bare', project: '', token: '', trace_id: '', time: at(2) }),
    ]);
    await rowButton(page, 'full').click();
    const full = await text(detail(page));
    expect(full).toMatch(/\bProject\b/);
    expect(full).toMatch(/\bToken\b/);
    expect(full).toMatch(/\bReplica\b/);
    await rowButton(page, 'bare').click();
    await expect(detail(page)).toContainText('bare');
    const bare = await text(detail(page));
    expect(bare, 'no Project label for an event without a project').not.toMatch(/\bProject\b/);
    expect(bare, 'no Token label for an event without a token').not.toMatch(/\bToken\b/);
    expect(bare).toMatch(/\bReplica\b/);
    expect(bare, 'no placeholder for what is missing').not.toMatch(/\b(undefined|null|none|n\/a)\b/i);
  });

  test('says per kind what to add: a skill, an agent or an article', async ({ page }) => {
    await open(page, [
      missing(1, 'skill:ghost', { kind: 'get' }),
      missing(2, 'agent:ghost', { kind: 'get' }),
      missing(3, 'platform/ghost', { kind: 'lookup' }),
    ]);
    await rowButton(page, 'skill:ghost').click();
    await expect(detail(page)).toContainText('Add this skill to the Source repo ↗');
    expect(await text(detail(page))).not.toMatch(/article|agent to the/i);
    await rowButton(page, 'agent:ghost').click();
    await expect(detail(page)).toContainText('Add this agent to the Source repo ↗');
    expect(await text(detail(page))).not.toMatch(/article|skill to the/i);
    await rowButton(page, 'platform/ghost').click();
    await expect(detail(page)).toContainText('Start this article in the Source repo ↗');
    expect(await text(detail(page))).not.toMatch(/Add this (skill|agent)/);
  });

  test('"Try this search" runs the query in search for a search or a lookup, and is hidden for get events', async ({ page }) => {
    await open(page, [
      missing(1, 'quantum computing', { time: at(1) }),
      missing(2, 'platform/ghost', { kind: 'lookup', time: at(2) }),
      missing(3, 'skill:ghost', { kind: 'get', time: at(3) }),
      ev(4, { kind: 'get', query: 'agent:review', time: at(4) }),
      ev(5, { kind: 'get', query: 'builder', time: at(5) }),
    ]);
    for (const q of ['skill:ghost', 'agent:review', 'builder']) {
      await rowButton(page, q).click();
      await expect(detail(page)).toContainText(q);
      await expect(trySearch(page), `no search to run for a get of ${q}`).toHaveCount(0);
    }
    await rowButton(page, 'platform/ghost').click();
    await expect(trySearch(page)).toHaveCount(1);
    await rowButton(page, 'quantum computing').click();
    await expect(trySearch(page)).toHaveCount(1);
    await trySearch(page).click();
    await page.waitForURL((u) => u.pathname === '/' && u.searchParams.get('q') === 'quantum computing');
    await expect(h1(page)).not.toHaveText('Activity');
  });

  test('renders a hostile query as text in the panel, and the search keeps it as one value', async ({ page }) => {
    const hostile = 'a&b=c <i>x</i> #frag';
    await open(page, [ev(1, { query: hostile })]);
    await rowButton(page, 'a&b=c').click();
    await expect(detail(page)).toContainText(hostile);
    await expect(detail(page).locator('i')).toHaveCount(0);
    await trySearch(page).click();
    await page.waitForURL((u) => u.pathname === '/' && u.searchParams.get('q') === hostile);
  });
});

test.describe('the Miss board and the panel with the keyboard', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('a card and the panel actions show the focus ring and work with Enter', async ({ page }) => {
    await open(page, [missing(1, 'quantum')]);
    const reach = async (target: Locator) => {
      await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
      let reached = false;
      for (let i = 0; i < 60 && !reached; i++) {
        await page.keyboard.press('Tab');
        reached = await target.evaluate((el) => el === document.activeElement);
      }
      expect(reached, 'the control is in the tab order').toBe(true);
      await expectFocusRing(target);
    };
    await reach(cards(page).first());
    await page.keyboard.press('Enter');
    await expect(detail(page)).toBeVisible();
    await reach(trySearch(page));
  });
});

test.describe('the Miss board and the panel at 375 px', () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test('cards and panel actions are at least 44 px and the page does not scroll sideways', async ({ page }) => {
    await open(page, [...crowd, missing(40, 'platform/ghost', { kind: 'lookup' })]);
    for (const l of [cards(page).first(), cards(page).nth(3)]) {
      const box = await l.boundingBox();
      expect(box, 'the card is on screen').not.toBeNull();
      expect(box!.width).toBeGreaterThanOrEqual(44);
      expect(box!.height).toBeGreaterThanOrEqual(44);
    }
    await rowButton(page, 'platform/ghost').click();
    for (const l of [trySearch(page), detail(page).getByRole('link', { name: /source repo/i })]) {
      const box = await l.boundingBox();
      expect(box, 'the action is on screen').not.toBeNull();
      expect(box!.height).toBeGreaterThanOrEqual(44);
    }
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });
});

// The stopgap target of the Source repo link and the panel on a narrow screen (#632 fix round).
test.describe('the Source repo link and the panel at 375 px', () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test('a miss links to /ops/sources and a served event has no such link', async ({ page }) => {
    await open(page, [ev(1, { query: 'served' }), missing(2, 'skill:ghost', { kind: 'get' })]);
    await rowButton(page, 'skill:ghost').click();
    await expect(detail(page).getByRole('link', { name: /source repo/i })).toHaveAttribute('href', '/ops/sources');
    await rowButton(page, 'served').click();
    await expect(detail(page).getByRole('link', { name: /source repo/i })).toHaveCount(0);
  });

  test('a card tap brings the panel into the viewport without moving focus', async ({ page }) => {
    const feed = Array.from({ length: 30 }, (_, i) => ev(i + 1, { query: `served ${i}` }));
    await open(page, [...feed, missing(40, 'quantum')]);
    await cardOf(page, 'quantum').click();
    await expect(detail(page)).toBeVisible();
    await expect
      .poll(() =>
        detail(page).evaluate((el) => {
          const r = el.getBoundingClientRect();
          return r.top < window.innerHeight && r.bottom > 0;
        }),
      )
      .toBe(true);
    expect(await page.evaluate(() => document.activeElement?.tagName)).toBe('BUTTON');
  });
});

test.describe('the live header', () => {
  test.use({ viewport: { width: 375, height: 800 } });

  test('reads just Live with a single replica', async ({ page }) => {
    await open(page, [ev(1, { replica: 'goldie-0' })]);
    await expect(main(page).locator('p.label')).toHaveText('Live');
  });

  test('reads how many of the replicas seen answered in the last minute', async ({ page }) => {
    const ago = (ms: number) => new Date(Date.now() - ms).toISOString();
    await open(page, [ev(1, { replica: 'goldie-0' }), ev(2, { replica: 'goldie-1', time: ago(3 * 60_000) })]);
    await expect(main(page).locator('p.label')).toHaveText('Live · merged from all replicas (1 of 2 answering)');
    await send(page, frame(ev(3, { replica: 'goldie-1' })));
    await expect(main(page).locator('p.label')).toHaveText('Live · merged from all replicas (2 of 2 answering)');
  });
});

// The last five minutes and the Project chip (#809, handoff §3.6). Contract these specs pin down:
// - a region in main named /last five minutes/i holds four list items, the tiles, each text
//   starting with its label: Requests (a whole number, with the sub-line "per minute"), Misses (a percent,
//   with "of requests"), Projects asking (a count), Busiest surface and (#810) Latency;
// - the Project filter is the URL's `project`: only that Project's events are rows, and a button in
//   main named /remove.*<project>/i is its chip; pressing it drops `project` from the URL.
const tiles = (page: Page) => main(page).getByRole('region', { name: /last five minutes/i });
const tile = (page: Page, label: string | RegExp) => tiles(page).getByRole('listitem').filter({ hasText: label });

test.describe('the last five minutes', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('shows Requests, Misses and Projects asking, five minutes wide', async ({ page }) => {
    const at = (ms: number) => new Date(Date.now() + ms).toISOString();
    const batch = Array.from({ length: 600 }, (_, i) =>
      ev(i + 1, { query: `bulk ${i}`, miss: i % 10 === 0, project: `github.com/acme/p${i % 3}` }),
    );
    await open(page, [
      ...batch,
      ev(1000, { query: 'replayed', project: 'github.com/acme/old', time: at(-10 * 60_000) }),
      ev(1001, { query: 'skewed', project: 'github.com/acme/p0', time: at(60 * 60_000) }),
    ]);
    await expect(tiles(page)).toBeVisible();
    await expect(tiles(page).getByRole('listitem')).toHaveCount(5);
    // 601 requests in five minutes is 120 a minute, though the feed keeps 500 rows; the replayed
    // one is older than five minutes and the skewed one counts as now.
    // The Misses sub-line reads "of requests", so the Requests tile is found by its leading label.
    await expect(tile(page, /^\s*Requests\b/)).toContainText(/\b120\b/);
    await expect(tile(page, /^\s*Requests\b/)).toContainText(/per minute/i);
    await expect(tile(page, 'Misses')).toContainText(/\b10\s*%/);
    await expect(tile(page, 'Misses')).toContainText(/of requests/i);
    await expect(tile(page, 'Projects asking')).toContainText(/\b3\b/);
  });

  test('does not render a link to Grafana in this slice', async ({ page }) => {
    await open(page, sample);
    await expect(main(page).getByText(/Trends in Grafana/i)).toHaveCount(0);
    await expect(main(page).getByRole('link', { name: /grafana/i })).toHaveCount(0);
  });
});

test.describe('the Project filter chip', () => {
  test.use({ viewport: { width: 1280, height: 800 } });
  const shop = 'github.com/acme/shop';

  test('shows the Project as a removable chip, and removing it shows all Projects', async ({ page }) => {
    await open(page, sample, `/ops/activity?project=${encodeURIComponent(shop)}`);
    await expect(rows(page)).toHaveCount(2);
    const chip = main(page).getByRole('button', { name: /remove.*github\.com\/acme\/shop/i });
    await expect(chip).toBeVisible();
    await chip.click();
    await expect(rows(page)).toHaveCount(sample.length);
    await expect(chip).toHaveCount(0);
    expect(new URL(page.url()).searchParams.has('project')).toBe(false);
  });
});

test.describe('the Live stream stage (#811)', () => {
  // The config turns motion off for every spec; the stage draws a particle only when it is on.
  test.use({ viewport: { width: 1280, height: 800 }, reducedMotion: 'no-preference' });
  const shop = 'github.com/acme/shop';
  const particles = (page: Page) => main(page).locator('svg[role="img"] circle.particle');
  const lane = (page: Page) => main(page).getByRole('button', { name: new RegExp(`^${shop.replace(/\./g, '\\.')}`) });
  const live = (id: number, o: Partial<Ev> & { via?: string } = {}) => frame({ ...ev(id, { project: shop }), via: 'mcp', ...o } as Ev);

  test('draws no particle for the history replayed on connect, and one for a live request, named by its surface', async ({ page }) => {
    await open(page, sample);
    await expect(rows(page)).toHaveCount(8);
    await expect(particles(page)).toHaveCount(0);
    await send(page, live(20));
    await expect(particles(page)).toHaveCount(1);
    await expect(particles(page).first().locator('title')).toHaveText(/^mcp · /);
    await expect(main(page).getByText('Showing every request')).toBeVisible();
  });

  test('a lane filters the feed to its Project, and pressing it again clears the filter', async ({ page }) => {
    await open(page);
    await send(page, live(1, { query: 'one' }) + live(2, { query: 'two', project: 'github.com/acme/api' }));
    await expect(lane(page)).toBeVisible();
    await lane(page).click();
    await expect(lane(page)).toHaveAttribute('aria-pressed', 'true');
    expect(new URL(page.url()).searchParams.get('project')).toBe(shop);
    await expect(rows(page)).toHaveCount(1);
    await lane(page).click();
    await expect(page).not.toHaveURL(/[?&]project=/);
    await expect(rows(page)).toHaveCount(2);
  });

  test('draws nothing while the tab is hidden, and still shows the request in the feed', async ({ page }) => {
    await open(page);
    await page.evaluate(() => {
      Object.defineProperty(document, 'hidden', { configurable: true, get: () => true });
      document.dispatchEvent(new Event('visibilitychange'));
    });
    await send(page, live(30));
    await expect(rows(page)).toHaveCount(1);
    await expect(particles(page)).toHaveCount(0);
  });

  test('has no horizontal scroll at 375, 768 and 1280, and the SVG is not a sliver when narrow', async ({ page }) => {
    await open(page);
    await send(page, live(40));
    for (const width of [375, 768, 1280]) {
      await page.setViewportSize({ width, height: 800 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    }
    await page.setViewportSize({ width: 375, height: 800 });
    const box = await main(page).locator('svg[role="img"]').boundingBox();
    expect(box?.width ?? 0).toBeGreaterThan(250);
  });
});

// The surface pills, the Via and Result columns, Query → Item and the Latency tile (#810, handoff
// §3.6). Contract these specs pin down:
// - above the table in main, a group named /surface/i holds four checkboxes (role=checkbox, so the
//   switcher's buttons with aria-pressed stay the only such buttons), named MCP, CLI, UI and Other,
//   each with the number of events of that surface in the feed in its name; checking one shows only
//   that surface's rows, unchecking it shows all again; it combines with the Project chip and the
//   other filters;
// - the Via cell holds the surface's name (MCP, CLI, UI, Other, in any case);
// - the Result cell of a served row holds the served Item (the Reference as sent, as text), and of a
//   miss the words "No Item matched" besides Miss; the detail still says what was asked for;
// - the detail adds "Query → Item" as `<query> → <served>`, Latency (milliseconds, one decimal),
//   Via and Trace id; a field the event lacks (no served, a latency_us of 0 or none, no via, no
//   trace id: a peer on an older version) is left out with its label;
// - the Latency tile's headline is the p50 over five minutes and "p95 <ms>" sits under it,
//   in milliseconds with one decimal,
//   and Busiest surface holds the surface's name;
// - event text (served, trace id) is text without invisible or bidirectional controls, and a long
//   served is cut by code point, never in the middle of a character.
const surfaces = (page: Page) => main(page).getByRole('group', { name: /surface/i });
const pill = (page: Page, label: string) => surfaces(page).getByRole('checkbox', { name: new RegExp(`^${label}\\b`, 'i') });

const surfaced: Ev[] = [
  ev(1, { query: 'm one', via: 'mcp', project: 'github.com/acme/shop', served: 'article:platform/deploy' }),
  ev(2, { query: 'm two', via: 'mcp', project: 'github.com/acme/api' }),
  ev(3, { query: 'c one', via: 'cli', project: 'github.com/acme/shop' }),
  ev(4, { query: 'u one', via: 'ui' }),
  ev(5, { query: 'u two', via: 'ui' }),
  ev(6, { query: 'u three', via: 'ui', project: 'github.com/acme/shop' }),
  ev(7, { query: 'o one', via: 'other' }),
];

test.describe('the surface pills', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('are MCP, CLI, UI and Other with their counts, and filter the feed', async ({ page }) => {
    await open(page, surfaced);
    await expect(surfaces(page).getByRole('checkbox')).toHaveCount(4);
    await expect(pill(page, 'MCP')).toContainText(/\b2\b/);
    await expect(pill(page, 'CLI')).toContainText(/\b1\b/);
    await expect(pill(page, 'UI')).toContainText(/\b3\b/);
    await expect(pill(page, 'Other')).toContainText(/\b1\b/);
    await expect(rows(page)).toHaveCount(7);

    await pill(page, 'UI').click();
    await expect(pill(page, 'UI')).toBeChecked();
    await expect(rows(page)).toHaveCount(3);
    await expect(rowOf(page, 'u two')).toHaveCount(1);
    await expect(rowOf(page, 'm one')).toHaveCount(0);
    await expect(pill(page, 'MCP'), 'the counts are of the feed, not of the filtered rows').toContainText(/\b2\b/);

    await send(page, frame(ev(8, { query: 'u four', via: 'ui' })) + frame(ev(9, { query: 'm three', via: 'mcp' })));
    await expect(rowOf(page, 'u four')).toHaveCount(1);
    await expect(rowOf(page, 'm three'), 'a new row of another surface stays filtered out').toHaveCount(0);
    await expect(pill(page, 'UI')).toContainText(/\b4\b/);
    await expect(pill(page, 'MCP')).toContainText(/\b3\b/);

    await pill(page, 'UI').click();
    await expect(pill(page, 'UI')).not.toBeChecked();
    await expect(rows(page)).toHaveCount(9);
  });

  test('work alongside the Project chip', async ({ page }) => {
    const shop = 'github.com/acme/shop';
    await open(page, surfaced, `/ops/activity?project=${encodeURIComponent(shop)}`);
    await expect(rows(page)).toHaveCount(3);
    await pill(page, 'MCP').click();
    await expect(rows(page), 'MCP of the shop Project only').toHaveCount(1);
    await expect(rowOf(page, 'm one')).toHaveCount(1);
    await main(page)
      .getByRole('button', { name: /remove.*github\.com\/acme\/shop/i })
      .click();
    await expect(rows(page), 'the pill filter stays when the Project is dropped').toHaveCount(2);
    await expect(pill(page, 'MCP')).toBeChecked();
    expect(new URL(page.url()).searchParams.has('project')).toBe(false);
  });
});

test.describe('the Via and Result columns', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('Via shows the surface, and Result the served Item or No Item matched', async ({ page }) => {
    await open(page, [...surfaced, ev(10, { query: 'nothing here', via: 'cli', miss: true })]);
    await expect(cell(rowOf(page, 'm one'), col.via)).toContainText(/mcp/i);
    await expect(cell(rowOf(page, 'c one'), col.via)).toContainText(/cli/i);
    await expect(cell(rowOf(page, 'u one'), col.via)).toContainText(/\bui\b/i);
    await expect(cell(rowOf(page, 'o one'), col.via)).toContainText(/other/i);
    const served = cell(rowOf(page, 'm one'), col.result);
    await expect(served).toContainText('article:platform/deploy');
    expect(await text(served), 'a served row is not a Miss').not.toMatch(/miss|no item matched|\bServed\b/i);
    const miss = cell(rowOf(page, 'nothing here'), col.result);
    await expect(miss).toContainText('No Item matched');
    await expect(miss, 'colour is not the only sign of a Miss').toContainText(/miss/i);
  });

  test('render the served Item as text, without invisible controls, cut by code point', async ({ page, baseURL }) => {
    const seen = await problems(page, baseURL ?? '');
    const hostile = 'article:<img src=x onerror="window.__pwned=1"><b>bold</b>';
    const hidden = 'article:a‮evil‬​z⁦q';
    const longOdd = `skill:a${'\u{1F600}'.repeat(50_000)}`;
    const longEven = `skill:ab${'\u{1F600}'.repeat(50_000)}`;
    await open(page, [
      ev(1, { query: 'hostile', served: hostile }),
      ev(2, { query: 'hidden', served: hidden }),
      ev(3, { query: 'long odd', served: longOdd }),
      ev(4, { query: 'long even', served: longEven }),
    ]);
    await expect(rows(page)).toHaveCount(4);
    await expect(cell(rowOf(page, 'hostile'), col.result)).toContainText(hostile);
    await expect(table(page).locator('img, script, b, iframe')).toHaveCount(0);
    expect(await page.evaluate(() => (window as unknown as Win).__pwned), 'the markup ran').toBeUndefined();
    const shown = await cell(rowOf(page, 'hidden'), col.result).innerText();
    expect(shown).toContain('article:aevilzq');
    expect(shown).not.toMatch(/[​-‏‪-‮⁠-⁩﻿]/);
    for (const q of ['long odd', 'long even']) {
      const cut = await cell(rowOf(page, q), col.result).innerText();
      expect(cut.length, `${q}: some of it is shown`).toBeGreaterThan(0);
      expect(cut.length, `${q}: 100,000 characters are not`).toBeLessThan(10_000);
      expect(cut.isWellFormed(), `${q}: not cut inside a character`).toBe(true);
    }
    await rowButton(page, 'long odd').click();
    const d = await detail(page).innerText();
    expect(d.length, 'the detail is cut too').toBeLessThan(10_000);
    expect(d.isWellFormed()).toBe(true);
    expect(seen.errors, 'page errors and CSP violations').toEqual([]);
    expect(seen.external).toEqual([]);
  });
});

test.describe('the detail of a served event', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('shows Query → Item, Latency, Via, Replica and Trace id', async ({ page }) => {
    await open(page, [
      ev(1, {
        query: 'database',
        via: 'cli',
        served: 'article:platform/deploy',
        latency_us: 850,
        trace_id: '4bf92f3577b34da6a3ce929d0e0e4736',
        replica: 'goldie-1',
      }),
    ]);
    await rowButton(page, 'database').click();
    const d = await text(detail(page));
    expect(d).toMatch(/database\s*→\s*article:platform\/deploy/);
    expect(d).toMatch(/\bLatency\b\s*0\.9\s*ms/);
    expect(d).toMatch(/\bVia\b\s*CLI/i);
    expect(d).toMatch(/\bReplica\b\s*goldie-1/);
    expect(d).toMatch(/Trace id\s*4bf92f3577b34da6a3ce929d0e0e4736/i);
  });

  test('leaves out what a peer on an older version does not send', async ({ page }) => {
    await open(page, [ev(1, { query: 'old peer', latency_us: 0, trace_id: '', replica: 'goldie-1' })]);
    await rowButton(page, 'old peer').click();
    await expect(detail(page)).toContainText('old peer');
    const d = await text(detail(page));
    expect(d, 'no Via without a surface').not.toMatch(/\bVia\b/);
    expect(d, 'no Latency for 0 or none').not.toMatch(/\bLatency\b/);
    expect(d, 'no Trace id without one').not.toMatch(/Trace id/i);
    expect(d, 'no Query → Item without a served Item').not.toContain('→');
    expect(d).toMatch(/\bReplica\b/);
    expect(d, 'no placeholder for what is missing').not.toMatch(/\b(undefined|null|none|n\/a|0\.0)\b|—/i);
  });

  test('shows a trace id as text', async ({ page }) => {
    await open(page, [ev(1, { query: 'traced', trace_id: '<b>t</b>‮1', via: 'mcp' })]);
    await rowButton(page, 'traced').click();
    await expect(detail(page)).toContainText('<b>t</b>1');
    await expect(detail(page).locator('b')).toHaveCount(0);
  });
});

test.describe('the Busiest surface and Latency tiles', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('name the busiest surface and the p50 and p95 of the last five minutes, in ms with one decimal', async ({ page }) => {
    const old = new Date(Date.now() - 10 * 60_000).toISOString();
    const batch = Array.from({ length: 101 }, (_, i) =>
      ev(i + 1, { query: `timed ${i}`, via: i < 60 ? 'mcp' : i < 90 ? 'cli' : 'ui', latency_us: (i + 1) * 1000 }),
    );
    await open(page, [...batch, ev(500, { query: 'replayed', via: 'ui', latency_us: 9_000_000, time: old })]);
    await expect(tile(page, 'Busiest surface')).toContainText(/mcp/i);
    expect(await text(tile(page, 'Busiest surface'))).not.toContain('—');
    const l = await text(tile(page, 'Latency'));
    // The p50 is the tile's value, with no label; the p95 sits under it (#879).
    expect(l, 'the p50 value has no label').not.toMatch(/p50/i);
    expect(l).toMatch(/p95\s*96\.0/i);
    expect(l.indexOf('51.0'), 'p50 is 51.0 ms').toBeGreaterThan(-1);
    expect(l.indexOf('96.0'), 'p95 is 96.0 ms').toBeGreaterThan(l.indexOf('51.0'));
    expect(l).toMatch(/\bms\b/);
    expect(l, 'the replayed 9,000 ms is outside the window').not.toMatch(/9000|9,000/);
  });
});

// The links out of the activity view (#808, handoff §3.6). The Session (#807) carries grafana_url and
// trace_url_template for an operator. Contract these specs pin down:
// - "Trends in Grafana ↗" is a link in the live header (the bar above the feed) whose href is
//   grafana_url; with no grafana_url there is no such link;
// - "Open trace ↗" is a link in the event detail, only with a trace_url_template and an event that
//   has a trace_id; its href is the template with the trace_id percent-encoded into {trace_id};
// - both open in a new tab with rel "noopener noreferrer", and neither URL is ever markup.
test.describe('the Grafana and trace links', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  const grafana = 'https://grafana.example.com/d/goldie';
  const template = 'https://traces.example.com/trace/{trace_id}?from=goldie';
  const trendsLink = (page: Page) => main(page).getByRole('link', { name: /trends in grafana/i });
  const traceLink = (page: Page) => detail(page).getByRole('link', { name: /open trace/i });
  const rels = async (l: Locator) => ((await l.getAttribute('rel')) ?? '').split(/\s+/).sort();

  test('shows "Trends in Grafana" in the live header with a grafana_url, in a new tab', async ({ page }) => {
    await open(page, [ev(1)], '/ops/activity', { grafana_url: grafana });
    await expect(trendsLink(page)).toHaveCount(1);
    await expect(trendsLink(page)).toHaveText(/^\s*Trends in Grafana\s*↗\s*$/);
    await expect(trendsLink(page)).toHaveAttribute('href', grafana);
    await expect(trendsLink(page)).toHaveAttribute('target', '_blank');
    expect(await rels(trendsLink(page))).toEqual(['noopener', 'noreferrer']);
    await expect(main(page).locator('.bar').getByRole('link', { name: /trends in grafana/i })).toHaveCount(1);
  });

  test('has no Grafana link without a grafana_url', async ({ page }) => {
    await open(page, [ev(1)]);
    await expect(main(page).getByText('Live', { exact: true })).toBeVisible();
    await expect(trendsLink(page)).toHaveCount(0);
    await expect(main(page).getByText(/grafana/i)).toHaveCount(0);
  });

  test('shows "Open trace" in the detail with the template and a trace_id, percent-encoded, in a new tab', async ({ page }) => {
    await open(page, [ev(1, { query: 'traced', trace_id: '4bf92f35/77b3?x=1&y#z é' })], '/ops/activity', {
      trace_url_template: template,
    });
    await rowButton(page, 'traced').click();
    await expect(traceLink(page)).toHaveCount(1);
    await expect(traceLink(page)).toHaveText(/^\s*Open trace\s*↗\s*$/);
    await expect(traceLink(page)).toHaveAttribute(
      'href',
      'https://traces.example.com/trace/4bf92f35%2F77b3%3Fx%3D1%26y%23z%20%C3%A9?from=goldie',
    );
    await expect(traceLink(page)).toHaveAttribute('target', '_blank');
    expect(await rels(traceLink(page))).toEqual(['noopener', 'noreferrer']);
  });

  test('lists "Try this search" before "Open trace" in the detail when both are shown (#860)', async ({ page }) => {
    await open(page, [ev(1, { query: 'both actions', trace_id: '4bf92f3577b34da6a3ce929d0e0e4736' })], '/ops/activity', {
      trace_url_template: template,
    });
    await rowButton(page, 'both actions').click();
    await expect(traceLink(page)).toHaveCount(1);
    await expect(trySearch(page)).toHaveCount(1);
    // DOCUMENT_POSITION_FOLLOWING (4): the trace link follows the search link in the document.
    const follows = await trySearch(page).evaluate(
      (search, trace) => Boolean(search.compareDocumentPosition(trace) & Node.DOCUMENT_POSITION_FOLLOWING),
      await traceLink(page).elementHandle(),
    );
    expect(follows, '"Open trace" comes after "Try this search"').toBe(true);
  });

  test('has no "Open trace" without a template', async ({ page }) => {
    await open(page, [ev(1, { query: 'plain id', trace_id: '4bf92f3577b34da6a3ce929d0e0e4736' })]);
    await rowButton(page, 'plain id').click();
    await expect(detail(page)).toContainText('plain id');
    await expect(traceLink(page)).toHaveCount(0);
  });

  test('has no "Open trace" for an event without a trace_id, even with a template', async ({ page }) => {
    await open(page, [ev(1, { query: 'untraced', trace_id: '' })], '/ops/activity', { trace_url_template: template });
    await rowButton(page, 'untraced').click();
    await expect(detail(page)).toContainText('untraced');
    await expect(traceLink(page)).toHaveCount(0);
  });

  test('binds a hostile URL and trace_id as an href only, never as markup', async ({ page }) => {
    const hostile = 'https://g.example.com/d?x="><img src=x onerror="window.__pwned=1"><b>bold</b>';
    await open(page, [ev(1, { query: 'hostile', trace_id: '"><img src=x onerror="window.__pwned=2">' })], '/ops/activity', {
      grafana_url: hostile,
      trace_url_template: 'https://t.example.com/{trace_id}',
    });
    await rowButton(page, 'hostile').click();
    await expect(trendsLink(page)).toHaveAttribute('href', hostile);
    await expect(traceLink(page)).toHaveCount(1);
    const href = (await traceLink(page).getAttribute('href')) ?? '';
    expect(href.startsWith('https://t.example.com/')).toBe(true);
    expect(href).not.toMatch(/[<>"]/);
    await expect(main(page).locator('img, b')).toHaveCount(0);
    expect(await page.evaluate(() => (window as unknown as Win).__pwned), 'the markup ran').toBeUndefined();
  });
});

// The links out of the activity view, captured at every width and theme (#808): the live header
// with "Trends in Grafana ↗", and an event detail with "Open trace ↗".
eachViewport((width, theme) => {
  const links = { grafana_url: 'https://grafana.example.com/d/goldie', trace_url_template: 'https://traces.example.com/trace/{trace_id}' };

  test('the live header with the Grafana link is captured and axe-clean', async ({ page }) => {
    await open(page, sample, '/ops/activity', links);
    await expect(main(page).getByRole('link', { name: /trends in grafana/i })).toBeVisible();
    await evidence(page, width, theme, '-grafana', 'activity');
  });

  test('the event detail with the trace link is captured and axe-clean', async ({ page }) => {
    await open(page, [ev(1, { query: 'traced', trace_id: '4bf92f3577b34da6a3ce929d0e0e4736' })], '/ops/activity', links);
    await rowButton(page, 'traced').click();
    await expect(detail(page).getByRole('link', { name: /open trace/i })).toBeVisible();
    await evidence(page, width, theme, '-trace', 'activity');
  });
});

// A dropped stream (#873, handoff §5.1 "Offline / SSE dropped"): the page stays, says "Reconnecting…"
// in a polite status, reconnects with a growing, capped backoff and replaces the view with the
// replayed history. A reconnect counts as successful once the live marker has arrived.
test.describe('a dropped stream', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  const requests = (page: Page) => page.evaluate(() => (window as unknown as Win).__activity.urls.length);
  // drop ends the newest stream the way a closed connection does, or fails it the way a reset does.
  const drop = (page: Page, how: 'end' | 'error' = 'end') =>
    page.evaluate((h) => {
      const hook = (window as unknown as Win).__activity;
      const c = hook.controllers[hook.controllers.length - 1];
      if (h === 'end') c.close();
      else c.error(new TypeError('network error'));
    }, how);
  const reconnecting = (page: Page) => main(page).getByRole('status').filter({ hasText: 'Reconnecting…' });
  // answerNext makes the page's activity requests come back with a plain HTTP answer.
  const answerNext = (page: Page, status: number, body: unknown) =>
    page.evaluate(
      ([s, b]) => {
        const real = window.fetch;
        window.fetch = (input, init) => {
          const raw = input instanceof Request ? input.url : String(input);
          if (new URL(raw, location.href).pathname !== '/api/v1/activity') return real(input, init);
          (window as unknown as Win).__activity.urls.push('refused');
          return Promise.resolve(
            new Response(JSON.stringify(b), { status: s as number, headers: { 'Content-Type': 'application/json' } }),
          );
        };
      },
      [status, body] as const,
    );

  for (const how of ['end', 'error'] as const) {
    test(`keeps the feed, tiles, Miss board and selection when the stream ${how === 'end' ? 'ends' : 'errors'}`, async ({ page }) => {
      await page.clock.install();
      await open(page, [ev(1, { query: 'database' }), ev(2, { query: 'quantum', miss: true })]);
      await rowOf(page, 'database').getByRole('button').click();
      await expect(rows(page)).toHaveCount(2);
      await drop(page, how);
      await expect(reconnecting(page)).toBeVisible();
      await expect(rows(page)).toHaveCount(2);
      await expect(main(page).getByRole('region', { name: 'Last five minutes' })).toBeVisible();
      await expect(main(page).getByRole('heading', { name: "What people looked for and didn't find" })).toBeVisible();
      await expect(main(page).getByRole('region', { name: 'Event detail' })).toBeVisible();
      await expect(rowOf(page, 'database').getByRole('button')).toHaveAttribute('aria-pressed', 'true');
      await expect(main(page).getByRole('alert')).toHaveCount(0);
      await expect(main(page).getByRole('button', { name: 'Retry' })).toHaveCount(0);
    });
  }

  test('says "Reconnecting…" in a polite status instead of Live, until the stream is live again', async ({ page }) => {
    await page.clock.install();
    await open(page, [ev(1, { query: 'database' })]);
    await expect(main(page).getByText(/^Live(?! stream)\b/)).toHaveCount(1);
    await drop(page);
    const status = reconnecting(page);
    await expect(status).toHaveCount(1);
    expect(await status.getAttribute('aria-live'), 'polite, never assertive').not.toBe('assertive');
    await expect(main(page).getByText(/^Live(?! stream)\b/)).toHaveCount(0);
    await page.clock.fastForward(2_000);
    await page.waitForFunction(() => (window as unknown as Win).__activity.controllers.length > 1);
    await send(page, frame(ev(1, { query: 'database' })) + liveMarker);
    await expect(reconnecting(page)).toHaveCount(0);
    await expect(main(page).getByText(/^Live(?! stream)\b/)).toHaveCount(1);
  });

  test('reconnects after a pause, not at once and not in a tight loop', async ({ page }) => {
    await page.clock.install();
    await open(page, [ev(1)]);
    expect(await requests(page)).toBe(1);
    await drop(page);
    await expect(reconnecting(page)).toBeVisible();
    expect(await requests(page), 'no request before the backoff has passed').toBe(1);
    await page.clock.fastForward(2_000);
    await expect.poll(() => requests(page)).toBe(2);
  });

  test('doubles the pause on each failed attempt and caps it at 30 seconds with jitter', async ({ page }) => {
    await page.clock.install();
    await open(page, [ev(1)]);
    // Attempts that end before the live marker: the pauses grow 1 s, 2 s, 4 s, 8 s, ...
    for (let n = 1; n <= 3; n++) {
      await drop(page);
      await page.clock.fastForward(45_000);
      await expect.poll(() => requests(page), { message: `attempt ${n + 1}` }).toBe(n + 1);
    }
    await drop(page);
    await page.clock.fastForward(3_000);
    expect(await requests(page), 'the fourth pause (about 8 s) has not passed after 3 s').toBe(4);
    await page.clock.fastForward(45_000);
    await expect.poll(() => requests(page)).toBe(5);
    // Many failures later the pause stays within the cap, with room for jitter.
    for (let n = 5; n < 12; n++) {
      await drop(page);
      await page.clock.fastForward(45_000);
      await expect.poll(() => requests(page), { message: `attempt ${n + 1}` }).toBe(n + 1);
    }
    await drop(page);
    await page.clock.fastForward(5_000);
    expect(await requests(page), 'the capped pause (about 30 s) has not passed after 5 s').toBe(12);
  });

  test('a successful reconnect resets the backoff', async ({ page }) => {
    await page.clock.install();
    await open(page, [ev(1)]);
    for (let n = 1; n <= 3; n++) {
      await drop(page);
      await page.clock.fastForward(45_000);
      await expect.poll(() => requests(page)).toBe(n + 1);
    }
    await send(page, frame(ev(1)) + liveMarker);
    await expect(reconnecting(page)).toHaveCount(0);
    await drop(page);
    await expect(reconnecting(page)).toBeVisible();
    await page.clock.fastForward(2_000);
    await expect.poll(() => requests(page), { message: 'first pause again after a success' }).toBe(5);
  });

  test('replaces the view with the replayed history, without duplicate rows', async ({ page }) => {
    await page.clock.install();
    const history = [ev(1, { query: 'database' }), ev(2, { query: 'quantum', miss: true })];
    await open(page, history);
    await drop(page);
    await expect(reconnecting(page)).toBeVisible();
    await page.clock.fastForward(2_000);
    await page.waitForFunction(() => (window as unknown as Win).__activity.controllers.length > 1);
    await send(page, [...history, ev(3, { query: 'newcomer' })].map(frame).join('') + liveMarker);
    await expect(rowOf(page, 'newcomer')).toHaveCount(1);
    await expect(rows(page)).toHaveCount(3);
    await expect(rowOf(page, 'database')).toHaveCount(1);
    await expect(rowOf(page, 'quantum')).toHaveCount(1);
  });

  const refusals = [
    { status: 401, code: 'unauthorized', shows: 'Your session ended.' },
    { status: 403, code: 'forbidden', shows: 'This page is for operators.' },
    { status: 503, code: 'too_many_streams', shows: 'Close another stream, or retry in a minute.' },
  ];
  for (const r of refusals) {
    test(`a ${r.status} on reconnect shows its state and does not loop`, async ({ page }) => {
      await page.clock.install();
      await open(page, [ev(1, { query: 'database' })]);
      await answerNext(page, r.status, envelope(r.code, 'refused', 'Close another stream, or retry in a minute.'));
      await drop(page);
      await page.clock.fastForward(2_000);
      await expect(page.getByText(r.shows)).toBeVisible();
      const seen = await requests(page);
      await page.clock.fastForward(120_000);
      expect(await requests(page), 'no further attempts').toBe(seen);
      await expect(reconnecting(page)).toHaveCount(0);
    });
  }
});

// Pressed toggles (#875, handoff §6.2 to §6.4, finding A11): a pressed or checked replica button,
// surface pill and Project lane use the brand pressed style (the Decisions status filter's, brand and
// not gold); gold is for Miss, so only "Misses only" is gold when checked. Compared as computed
// colours against the tokens, so it holds in both themes.
const pressedMix: Ev[] = [
  ev(1, { query: 'm one', via: 'mcp', project: 'github.com/acme/shop', replica: 'goldie-0' }),
  ev(2, { query: 'c one', via: 'cli', project: 'github.com/acme/shop', replica: 'goldie-1' }),
  ev(3, { query: 'u one', via: 'ui', project: 'github.com/acme/api', replica: 'goldie-1', miss: true }),
];

const tokenColour = (page: Page, token: string) =>
  page.evaluate((t) => {
    const probe = document.createElement('span');
    probe.style.background = `var(${t})`;
    document.body.appendChild(probe);
    const c = getComputedStyle(probe).backgroundColor;
    probe.remove();
    return c;
  }, token);
const backgroundOf = (l: Locator) => l.evaluate((el) => getComputedStyle(el).backgroundColor);

async function pressEverything(page: Page) {
  await replicaButton(page, 'goldie-1').click();
  await pill(page, 'MCP').click();
  await main(page)
    .getByRole('button', { name: /^github\.com\/acme\/shop/ })
    .click();
  await missesOnly(page).click();
}

eachViewport((width, theme) => {
  test('the pressed toggles are captured and axe-clean', async ({ page }) => {
    await open(page);
    // Lanes come from live events only, not the history replayed on connect (#811).
    await send(page, pressedMix.map(frame).join(''));
    await expect(rows(page)).toHaveCount(3);
    await pressEverything(page);
    await expect(pill(page, 'MCP')).toBeChecked();
    await evidence(page, width, theme, '-pressed', 'activity');
  });
});

test.describe('the pressed toggles use the brand style, and gold is for Miss', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  for (const t of ['light', 'dark'] as const) {
    test(`a pressed replica button, surface pill and Project lane are brand, not gold, in ${t}`, async ({ page }) => {
      await page.emulateMedia({ colorScheme: t });
      await open(page);
      // Lanes come from live events only, not the history replayed on connect (#811).
      await send(page, pressedMix.map(frame).join(''));
      await expect(rows(page)).toHaveCount(3);
      const gold = await tokenColour(page, '--miss-solid');
      const brand = [await tokenColour(page, '--brand'), await tokenColour(page, '--brand-tint')];
      const replica = replicaButton(page, 'goldie-1');
      const mcp = pill(page, 'MCP');
      const shop = main(page).getByRole('button', { name: /^github\.com\/acme\/shop/ });
      const idle = [await backgroundOf(replica), await backgroundOf(mcp), await backgroundOf(shop)];
      await pressEverything(page);
      await expect(replica).toHaveAttribute('aria-pressed', 'true');
      await expect(shop).toHaveAttribute('aria-pressed', 'true');
      for (const [i, el] of [replica, mcp, shop].entries()) {
        const bg = await backgroundOf(el);
        expect(bg, 'not the Miss gold').not.toBe(gold);
        expect(brand, 'a brand pressed colour').toContain(bg);
        expect(bg, 'differs from unpressed').not.toBe(idle[i]);
      }
    });
  }

  test('"Misses only" keeps the gold when checked, and loses it when unchecked', async ({ page }) => {
    await open(page);
    // Lanes come from live events only, not the history replayed on connect (#811).
    await send(page, pressedMix.map(frame).join(''));
    const gold = await tokenColour(page, '--miss-solid');
    await expect(missesOnly(page)).not.toHaveCSS('background-color', gold);
    await missesOnly(page).click();
    await expect(missesOnly(page)).toHaveCSS('background-color', gold);
    await missesOnly(page).click();
    await expect(missesOnly(page)).not.toHaveCSS('background-color', gold);
  });
});

// The feed layout (#876, handoff §3.6 item 6, §7, §9.1 2.4.6). Contract these specs pin down:
// - the surface pills, the Project chip and "Misses only" share one container (the toolbar) that is
//   below the stage, the tiles and the Miss board and directly above the feed table, in one row at
//   1280 px;
// - Live stream, Last five minutes, the Miss board and the feed each have one visible h2 (the feed's
//   is named "Feed");
// - with the Event detail open every column is visible with no horizontal scroll at 1280 px; the feed
//   is at least 800 px wide beside the detail, and where that does not fit the detail stacks under
//   the feed. At 1024 and 375 px the detail stacks under the feed and the page does not scroll sideways.
test.describe('the feed layout', () => {
  test.use({ viewport: { width: 1280, height: 800 } });
  const shop = 'github.com/acme/shop';
  const chip = (page: Page) => main(page).getByRole('button', { name: /remove.*github\.com\/acme\/shop/i });
  const box = async (l: Locator) => {
    const b = await l.boundingBox();
    if (!b) throw new Error('not rendered');
    return b;
  };
  const feedRegion = (page: Page) => main(page).getByRole('region', { name: 'Activity feed' });
  const detail = (page: Page) => main(page).getByRole('region', { name: 'Event detail' });
  const h2 = (page: Page, name: string) => main(page).getByRole('heading', { level: 2, name, exact: true });

  test('the surface pills, the Project chip and Misses only form one toolbar directly above the table', async ({ page }) => {
    await open(page, sample, `/ops/activity?project=${encodeURIComponent(shop)}`);
    await expect(chip(page)).toBeVisible();
    const tilesRegion = main(page).getByRole('region', { name: 'Last five minutes' });
    await expect(tilesRegion).toBeVisible();
    const boxes = [await box(surfaces(page)), await box(chip(page)), await box(missesOnly(page))];

    const shared = await surfaces(page).evaluate((el) => {
      const m = el.closest('main')!;
      const others = [...m.querySelectorAll('button')].filter(
        (b) => /misses only/i.test(b.textContent ?? '') || /^Remove Project filter/.test(b.getAttribute('aria-label') ?? ''),
      );
      let a: HTMLElement | null = el as HTMLElement;
      while (a && a !== m && !others.every((o) => a!.contains(o))) a = a.parentElement;
      return { isMain: a === m, hasTable: !!a && !!a.querySelector('table'), others: others.length };
    });
    expect(shared.others).toBe(2);
    expect(shared.isMain, 'the three controls share a container smaller than main').toBe(false);
    expect(shared.hasTable, 'the toolbar does not contain the table').toBe(false);

    const tiles = await box(tilesRegion);
    const stage = await box(main(page).getByRole('region', { name: 'Live stream' }));
    const feed = await box(feedRegion(page));
    for (const b of boxes) {
      expect(b.y, 'below the tiles').toBeGreaterThanOrEqual(tiles.y + tiles.height - 1);
      expect(b.y, 'below the stage').toBeGreaterThanOrEqual(stage.y + stage.height - 1);
      expect(b.y + b.height, 'above the table').toBeLessThanOrEqual(feed.y + 1);
    }
    const lowest = Math.max(...boxes.map((b) => b.y + b.height));
    expect(feed.y - lowest, 'the toolbar sits directly above the table').toBeLessThanOrEqual(64);
    const tops = boxes.map((b) => b.y);
    expect(Math.max(...tops) - Math.min(...tops), 'one row at 1280 px').toBeLessThan(24);
  });

  test('Live stream, Last five minutes, the Miss board and Feed each have one visible h2', async ({ page }) => {
    await open(page, sample);
    await expect(rows(page)).toHaveCount(sample.length);
    for (const name of ['Live stream', 'Last five minutes', 'Feed']) {
      const h = h2(page, name);
      await expect(h, name).toHaveCount(1);
      await expect(h, name).toBeVisible();
      const b = await box(h);
      expect(b.width, `${name} is not a hidden heading`).toBeGreaterThan(24);
      expect(b.height, `${name} is not a hidden heading`).toBeGreaterThan(10);
    }
    const board = main(page)
      .getByRole('region', { name: /didn't find/i })
      .getByRole('heading', { level: 2 });
    await expect(board).toHaveCount(1);
    await expect(board).toBeVisible();
    expect((await box(board)).height).toBeGreaterThan(10);
    const ys = [(await box(h2(page, 'Live stream'))).y, (await box(h2(page, 'Last five minutes'))).y, (await box(board)).y, (await box(h2(page, 'Feed'))).y];
    expect([...ys].sort((a, b) => a - b), 'in reading order: stage, tiles, board, feed').toEqual(ys);
  });

  test('at 1280 px with the detail open every column is visible and nothing scrolls sideways', async ({ page }) => {
    await open(page, sample);
    await rowOf(page, 'database').getByRole('button').click();
    await expect(detail(page)).toBeVisible();
    const region = await box(feedRegion(page));
    for (const name of ['Time', 'Event', 'Query → Item', 'Result', 'Via', 'Project', 'Replica']) {
      const th = table(page).getByRole('columnheader', { name, exact: true });
      await expect(th, name).toBeVisible();
      const b = await box(th);
      expect(b.x, `${name} starts inside the feed`).toBeGreaterThanOrEqual(region.x - 1);
      expect(b.x + b.width, `${name} ends inside the feed`).toBeLessThanOrEqual(region.x + region.width + 1);
    }
    expect(await feedRegion(page).evaluate((el) => el.scrollWidth <= el.clientWidth), 'the feed has no horizontal scroll').toBe(true);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

    const d = await box(detail(page));
    const beside = d.x >= region.x + region.width - 1 && d.y < region.y + region.height;
    if (beside) expect(region.width, 'beside the detail the feed is 800 px').toBeGreaterThanOrEqual(799);
    else expect(d.y, 'otherwise the detail stacks under the feed').toBeGreaterThanOrEqual(region.y + region.height - 1);
  });

  for (const width of [1024, 375]) {
    test(`at ${width} px with the detail open the detail stacks under the feed and the page does not scroll sideways`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 });
      await open(page, sample);
      await rowOf(page, 'database').getByRole('button').click();
      await expect(detail(page)).toBeVisible();
      const region = await box(feedRegion(page));
      const d = await box(detail(page));
      expect(d.y, 'under the feed').toBeGreaterThanOrEqual(region.y + region.height - 1);
      expect(d.x + d.width, 'inside the viewport').toBeLessThanOrEqual(width + 1);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    });
  }
});
