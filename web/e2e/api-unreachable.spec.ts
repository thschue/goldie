// The "API unreachable" state (#638, handoff §5.1, §9). In mode `ui` the server
// proxies /api to another goldie; when that API stops answering the proxy
// returns a 502 envelope (code upstream_unavailable), or the fetch fails. The
// page then shows a full-page state with a retry, never a raw failure.
// The API is stubbed per test (page.route) so every state is deterministic.
//
// Contract these specs pin down for the implementation:
// - the state is inside main, announced as role=alert or role=status, and its
//   text is the handoff copy "API unreachable. goldie's API isn't answering
//   right now. We'll keep trying." with one button named "Retry now";
// - it replaces the view: no hit, row or earlier content stays on the page;
// - it appears for the 502 envelope and for a failed fetch, never for another
//   error (a 500 keeps its own alert), and never shows the address, the
//   environment variable or any text of the envelope;
// - the retry runs the failed call again; the state goes when it succeeds.
import { expect, test, type Page, type Route } from '@playwright/test';
import { eachViewport, evidence, expectFocusRing, problems } from './ux';

const apiAny = /\/api\/v1\//;
const apiSearch = /\/api\/v1\/search(\?|$)/;

const json = (route: Route, status: number, body: unknown) =>
  route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

// The envelope the UI-mode proxy writes, with a hostile hint: the address and
// the variable must never reach the page.
const upstreamDown = {
  error: {
    code: 'upstream_unavailable',
    message: 'the upstream is not reachable',
    details: { hint: 'Retry shortly; check GOLDIE_UPSTREAM_ADDR=http://10.0.0.7:8080 is running.' },
  },
};

const hits = [
  { id: 'platform/deploy', name: 'Deploying to Kubernetes', kind: 'article', scope: 'global', keywords: [], score: 4, matched: ['database'] },
  { id: 'platform/logging', name: 'Structured logging', kind: 'article', scope: 'org', keywords: [], score: 3, matched: ['database'] },
];

type Mode = 'envelope' | 'network';

// serve answers the API while `down.on` is false, the session always as an
// operator, and fails every API call (the session too, when `session` is set)
// the way `down.mode` says while it is true.
async function serve(page: Page, opts: { session?: boolean } = {}) {
  const down = { on: false, mode: 'envelope' as Mode, calls: 0 };
  await page.route(apiAny, (route) => {
    const url = new URL(route.request().url());
    const isSession = url.pathname === '/api/v1/session';
    if (down.on && (!isSession || opts.session)) {
      down.calls++;
      return down.mode === 'network' ? route.abort('connectionrefused') : json(route, 502, upstreamDown);
    }
    if (isSession) return json(route, 200, { name: 'ux', role: 'operator' });
    if (apiSearch.test(url.pathname + url.search)) {
      const q = url.searchParams.get('q') ?? '';
      return json(route, 200, { query: q, results: hits });
    }
    return json(route, 404, { error: { code: 'not_found', message: 'no such endpoint', details: {} } });
  });
  return down;
}

const main = (page: Page) => page.getByRole('main');
const field = (page: Page) => main(page).locator('input[type="search"]');
const rows = (page: Page) => main(page).getByRole('list').getByRole('listitem');
const retryButton = (page: Page) => main(page).getByRole('button', { name: /retry now/i });
// The announced region that holds the copy.
const unreachable = (page: Page) =>
  main(page)
    .getByRole('alert')
    .or(main(page).getByRole('status'))
    .filter({ hasText: /API unreachable/ });

const copy = /API unreachable\.?\s+goldie[’']s API isn[’']t answering right now\.\s+We[’']ll keep trying\./;

async function expectState(page: Page) {
  const state = unreachable(page);
  await expect(state).toBeVisible();
  await expect(state).toHaveText(new RegExp(copy.source + '\\s*Retry now'));
  await expect(retryButton(page)).toBeVisible();
  await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
}

eachViewport((width, theme) => {
  test('the API unreachable state is captured and axe-clean', async ({ page }) => {
    const down = await serve(page);
    down.on = true;
    await page.goto('/?q=database');
    await expectState(page);
    await evidence(page, width, theme, '', 'api-unreachable');
  });

  test('the retry button is at least 44 px and shows a focus ring when reached by keyboard', async ({ page }) => {
    const down = await serve(page);
    down.on = true;
    await page.goto('/?q=database');
    await expectState(page);
    const box = (await retryButton(page).boundingBox())!;
    expect(box.height, 'retry height').toBeGreaterThanOrEqual(44);
    expect(box.width, 'retry width').toBeGreaterThanOrEqual(44);
    for (let i = 0; i < 20; i++) {
      await page.keyboard.press('Tab');
      if (await retryButton(page).evaluate((el) => el === document.activeElement)) break;
    }
    await expectFocusRing(retryButton(page));
  });
});

test.describe('when the API stops answering', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('the 502 envelope shows the state with the handoff copy and nothing of the envelope', async ({ page, baseURL }) => {
    const seen = await problems(page, baseURL ?? '');
    const down = await serve(page);
    down.on = true;
    await page.goto('/?q=database');
    await expectState(page);
    await expect(page.locator('body')).not.toContainText(/10\.0\.0\.7|GOLDIE_UPSTREAM_ADDR|upstream|Something went wrong|Couldn[’']t load/i);
    await expect(rows(page)).toHaveCount(0);
    expect(seen.errors, 'page errors and CSP violations').toEqual([]);
    expect(seen.external).toEqual([]);
  });

  test('a failed fetch shows the same state', async ({ page }) => {
    const down = await serve(page);
    down.on = true;
    down.mode = 'network';
    await page.goto('/?q=database');
    await expectState(page);
    await expect(main(page).getByText('No golden path')).toHaveCount(0);
  });

  test('a down API on first load shows the state, not the sign-in page', async ({ page }) => {
    const down = await serve(page, { session: true });
    down.on = true;
    await page.goto('/');
    await expectState(page);
    expect(new URL(page.url()).pathname, 'no redirect to sign-in').toBe('/');
    await page.unroute(apiAny);
    await page.route(apiAny, (route) => route.abort('connectionrefused'));
    await page.goto('/decisions');
    await expectState(page);
    expect(new URL(page.url()).pathname).toBe('/decisions');
  });

  test('loaded content is not shown as live while the state shows, and retry recovers when the API is back', async ({ page }) => {
    const down = await serve(page);
    await page.goto('/?q=database');
    await expect(rows(page)).toHaveCount(2);
    await expect(main(page)).toContainText('Deploying to Kubernetes');

    down.on = true;
    await field(page).fill('logging');
    await field(page).press('Enter');
    await expectState(page);
    await expect(rows(page)).toHaveCount(0);
    await expect(page.locator('body')).not.toContainText(/Deploying to Kubernetes|Structured logging/);

    // Retry while the API is still down keeps the state.
    const before = down.calls;
    await retryButton(page).click();
    await expect.poll(() => down.calls, 'retry calls the API again').toBeGreaterThan(before);
    await expectState(page);

    down.on = false;
    await retryButton(page).click();
    await expect(rows(page)).toHaveCount(2);
    await expect(main(page)).toContainText('Deploying to Kubernetes');
    await expect(unreachable(page)).toHaveCount(0);
    await expect(retryButton(page)).toHaveCount(0);
  });

  test('another API error keeps its own alert and never shows the unreachable state', async ({ page }) => {
    await serve(page);
    await page.route(apiSearch, (route) =>
      json(route, 500, { error: { code: 'internal', message: 'Something went wrong.', details: { hint: 'Try again in a moment.' } } }),
    );
    await page.goto('/?q=database');
    const alert = main(page).getByRole('alert');
    await expect(alert).toContainText('Something went wrong.');
    await expect(page.locator('body')).not.toContainText(/API unreachable/);
  });

  test('the state is not shown while the API answers', async ({ page }) => {
    await serve(page);
    await page.goto('/?q=database');
    await expect(rows(page)).toHaveCount(2);
    await expect(page.locator('body')).not.toContainText(/API unreachable/);
  });
});
