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
// - the stream is the local replica's: GET /api/v1/activity without `replicas`;
// - a row's action is a button in its Time cell with aria-pressed; the pressed one
//   is the selected event;
// - "Misses only" is a checkbox or a button named /misses only/i; Pause is a button
//   named Pause that becomes one named Resume;
// - the miss count is a role=status in main whose text holds the word miss or
//   misses (not "missed") and the count as a number;
// - the replica label is text outside the table that holds the replica's name;
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
    await page.route('**/api/v1/activity', (route) =>
      json(route, 403, envelope('forbidden', 'this token cannot do that', 'Use an operator token.')),
    );
    await page.goto('/ops/activity');
    await expect(main(page)).toContainText('This page is for operators.');
    await evidence(page, width, theme, '-reader', 'activity');
  });

  test('the too-many-streams state is captured and axe-clean', async ({ page }) => {
    await operator(page);
    await page.route('**/api/v1/activity', (route) =>
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

  test('is fed by the local replica stream and a label names the replica shown', async ({ page }) => {
    await open(page, sample);
    const urls = await page.evaluate(() => (window as unknown as Win).__activity.urls);
    expect(urls.length).toBeGreaterThan(0);
    for (const u of urls) {
      expect(u).toMatch(/^\/api\/v1\/activity(\?|$)/);
      expect(new URL(u, 'http://x').searchParams.has('replicas'), 'the local stream, not replicas=all').toBe(false);
    }
    expect(await mainTextOutsideTable(page), 'a label outside the table names the replica').toContain('goldie-0');
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
    await page.route('**/api/v1/activity', (route) =>
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
    await page.route('**/api/v1/activity', (route) =>
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
    await page.route('**/api/v1/activity', (route) =>
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
