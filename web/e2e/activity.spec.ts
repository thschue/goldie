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
// history can hold, and that the newest rows are the ones kept), the Via cell (an
// event carries no `via`), the Item half of Query → Item (an event carries none),
// and the time format.
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

const operator = (page: Page) =>
  page.route('**/api/v1/session', (route) =>
    route.request().method() === 'GET' ? json(route, 200, { name: 'ops', role: 'operator' }) : route.continue(),
  );

const send = (page: Page, text: string) => page.evaluate((t) => (window as unknown as Win).__send(t), text);

// open starts the page as an operator, waits for the stream request, and sends the
// history and the live marker.
async function open(page: Page, history: Ev[] = [], path = '/ops/activity') {
  await operator(page);
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
const missStatus = (page: Page) => main(page).getByRole('status').filter({ hasText: /\bmiss(es)?\b/i });
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

  // The row's text without the Reference the person typed, so only the copy the page adds is judged.
  const copyOf = async (page: Page, query: string) => (await text(rowOf(page, query))).split(query).join('');

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
//   starting with its label: Requests (a whole number per minute), Misses (a percent), Projects asking
//   (a count) and Busiest surface ("—" until #805);
// - the Project filter is the URL's `project`: only that Project's events are rows, and a button in
//   main named /remove.*<project>/i is its chip; pressing it drops `project` from the URL.
const tiles = (page: Page) => main(page).getByRole('region', { name: /last five minutes/i });
const tile = (page: Page, label: string) => tiles(page).getByRole('listitem').filter({ hasText: label });

test.describe('the last five minutes', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('shows Requests, Misses, Projects asking and a dash for the busiest surface', async ({ page }) => {
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
    await expect(tiles(page).getByRole('listitem')).toHaveCount(4);
    // 601 requests in five minutes is 120 a minute, though the feed keeps 500 rows; the replayed
    // one is older than five minutes and the skewed one counts as now.
    await expect(tile(page, 'Requests')).toContainText(/\b120\b/);
    await expect(tile(page, 'Misses')).toContainText(/\b10\s*%/);
    await expect(tile(page, 'Projects asking')).toContainText(/\b3\b/);
    await expect(tile(page, 'Busiest surface')).toContainText('—');
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
