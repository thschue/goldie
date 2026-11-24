// The Sources page /ops/sources (#633, handoff §3, §5.3): a row per Source
// from GET /api/v1/sync, failing ones first, and the nav count of failing
// Sources. Read-only. The API is stubbed per test (page.route) so every state
// is deterministic. The session role is stubbed too: the page is for
// operators, a reader gets the 403 page. The Resync button (#634) calls
// POST /api/v1/sync with the session's CSRF token (#490).
import { expect, test, type Page, type Request, type Route } from '@playwright/test';
import { eachViewport, evidence, expectFocusRing, sideNav, withNav } from './ux';

const apiSync = /\/api\/v1\/sync$/;

type Source = { revision?: string; last_poll?: string; last_success?: string; last_error?: string };

const json = (route: Route, status: number, body: unknown) =>
  route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

const envelope = (code: string, message: string, hint: string) => ({ error: { code, message, details: { hint } } });

const asRole = (page: Page, role: 'reader' | 'operator') =>
  page.route('**/api/v1/session', (route) =>
    route.request().method() === 'GET' ? json(route, 200, { name: 'ux', role }) : route.continue(),
  );

const stubSync = (page: Page, sources: Source[]) =>
  page.route(apiSync, (route) => json(route, 200, { sources, warnings: [] }));

const csrf = 'csrf-token-for-the-session';

// An operator whose session answers with a CSRF token, as GET /api/v1/session does (ADR 0027).
const asOperatorWithCsrf = (page: Page) =>
  page.route('**/api/v1/session', (route) =>
    route.request().method() === 'GET'
      ? route.fulfill({
          status: 200,
          contentType: 'application/json',
          headers: { 'X-Goldie-CSRF': csrf },
          body: JSON.stringify({ name: 'ux', role: 'operator' }),
        })
      : route.continue(),
  );

// stubResync answers GET with `before` until a POST has been answered, then with `after`. The POST
// waits for release(), so a test can look at the page while the sync runs. Posts holds the requests.
function stubResync(page: Page, before: Source[], after: Source[], answer: (route: Route) => unknown) {
  const posts: Request[] = [];
  let release!: () => void;
  const gate = new Promise<void>((resolve) => (release = resolve));
  let done = false;
  void page.route(apiSync, async (route) => {
    if (route.request().method() === 'POST') {
      posts.push(route.request());
      await gate;
      done = true;
      return answer(route);
    }
    return json(route, 200, { sources: done ? after : before, warnings: [] });
  });
  return { posts, release };
}

const resync = (page: Page) => main(page).getByRole('button', { name: /^(resync|syncing)/i });

const main = (page: Page) => page.getByRole('main');
const rows = (page: Page) => main(page).getByRole('listitem');
const h1 = (page: Page) => page.getByRole('heading', { level: 1 });
const sourcesLink = (page: Page) => sideNav(page).getByRole('link', { name: /^Sources/ });

const healthy: Source = {
  revision: '1111111111111111111111111111111111111111',
  last_poll: '2026-11-23T10:00:00Z',
  last_success: '2026-11-23T10:00:00Z',
};
const failing: Source = {
  revision: '2222222222222222222222222222222222222222',
  last_poll: '2026-11-23T10:05:00Z',
  last_success: '2026-11-23T09:00:00Z',
  last_error: 'global/articles/sub/bad.md: frontmatter not closed with ---',
};
const unpolled: Source = { revision: '3333333333333333333333333333333333333333' };

eachViewport((width, theme) => {
  test('the Sources page with a healthy, a failing and an unpolled Source is captured and axe-clean', async ({
    page,
  }) => {
    await asRole(page, 'operator');
    await stubSync(page, [healthy, failing, unpolled]);
    await page.goto('/ops/sources');
    await expect(rows(page)).toHaveCount(3);
    await evidence(page, width, theme, '', 'sources');
  });

  test('the Sources page empty state is captured and axe-clean', async ({ page }) => {
    await asRole(page, 'operator');
    await stubSync(page, []);
    await page.goto('/ops/sources');
    await expect(main(page).getByText(/no source is configured/i)).toBeVisible();
    await evidence(page, width, theme, '-empty', 'sources');
  });

  test('the Sources page error state is captured and axe-clean', async ({ page }) => {
    await asRole(page, 'operator');
    await page.route(apiSync, (route) =>
      json(route, 500, envelope('internal', 'Something went wrong.', 'Try again in a moment.')),
    );
    await page.goto('/ops/sources');
    await expect(main(page).getByRole('alert')).toBeVisible();
    await evidence(page, width, theme, '-error', 'sources');
  });

  test('the Sources page while a Resync runs is captured and axe-clean', async ({ page }) => {
    await asOperatorWithCsrf(page);
    const sync = stubResync(page, [failing], [healthy], (route) => json(route, 202, {}));
    await page.goto('/ops/sources');
    await resync(page).click();
    await expect(resync(page)).toHaveText(/syncing/i);
    await evidence(page, width, theme, '-syncing', 'sources');
    sync.release();
  });

  test('the Sources page with a refused Resync is captured and axe-clean', async ({ page }) => {
    await asOperatorWithCsrf(page);
    const sync = stubResync(page, [failing], [failing], (route) =>
      json(route, 429, envelope('sync_running', 'A sync is already running.', 'Wait for the running sync to end.')),
    );
    await page.goto('/ops/sources');
    sync.release();
    await resync(page).click();
    await expect(main(page).getByRole('alert')).toContainText('Wait for the running sync to end.');
    await evidence(page, width, theme, '-resync-error', 'sources');
  });

  test('the Sources page for a reader is captured and axe-clean', async ({ page }) => {
    await asRole(page, 'reader');
    await page.goto('/ops/sources');
    await expect(main(page)).toContainText('This page is for operators.');
    await evidence(page, width, theme, '-reader', 'sources');
  });
});

test.describe('the Sources page', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('lists one row per Source with revision, last poll, last success and last error', async ({ page }) => {
    await asRole(page, 'operator');
    await stubSync(page, [failing]);
    await page.goto('/ops/sources');
    await expect(h1(page)).toHaveText('Sources');
    await expect(rows(page)).toHaveCount(1);
    const row = rows(page).first();
    await expect(row).toContainText('2222222');
    await expect(row).toContainText(/last poll/i);
    await expect(row).toContainText(/last success/i);
    await expect(row).toContainText(/last error/i);
    await expect(row).toContainText(failing.last_error!);
  });

  test('puts failing Sources before healthy ones, whatever order the API answers in', async ({ page }) => {
    await asRole(page, 'operator');
    await stubSync(page, [healthy, unpolled, failing]);
    await page.goto('/ops/sources');
    await expect(rows(page)).toHaveCount(3);
    await expect(rows(page).first()).toContainText(failing.last_error!);
    await expect(rows(page).first()).toContainText('2222222');
  });

  test('a failing Source says so in text, not by colour alone', async ({ page }) => {
    await asRole(page, 'operator');
    await stubSync(page, [healthy, failing]);
    await page.goto('/ops/sources');
    await expect(rows(page)).toHaveCount(2);
    await expect(rows(page).first()).toContainText(/failing|failed/i);
    await expect(rows(page).nth(1)).not.toContainText(/failing|failed/i);
  });

  test('a Source with no finished poll says so', async ({ page }) => {
    await asRole(page, 'operator');
    await stubSync(page, [unpolled]);
    await page.goto('/ops/sources');
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).toContainText(/no poll .*(finished|ended|completed|yet)/i);
  });

  test('an empty list says no Source is configured, as a neutral status', async ({ page }) => {
    await asRole(page, 'operator');
    await stubSync(page, []);
    await page.goto('/ops/sources');
    await expect(main(page).getByRole('status')).toContainText(/no source is configured/i);
    await expect(main(page).getByRole('alert')).toHaveCount(0);
    await expect(rows(page)).toHaveCount(0);
  });

  test('the nav reads "Sources <n> failing" with the count in text', async ({ page }) => {
    await asRole(page, 'operator');
    await stubSync(page, [healthy, failing, { ...failing, revision: '4444444444444444444444444444444444444444' }]);
    await page.goto('/');
    await withNav(page);
    await expect(sourcesLink(page)).toHaveText(/^Sources\s+2 failing$/);
  });

  test('the nav shows no failing count when every Source is healthy', async ({ page }) => {
    await asRole(page, 'operator');
    await stubSync(page, [healthy]);
    await page.goto('/');
    await withNav(page);
    await expect(sourcesLink(page)).toBeVisible();
    await expect(sourcesLink(page)).not.toContainText(/failing/i);
  });

  test('shows an API error as text with details.hint', async ({ page }) => {
    await asRole(page, 'operator');
    await page.route(apiSync, (route) =>
      json(route, 500, envelope('internal', 'Something went wrong.', 'Try again in a moment.')),
    );
    await page.goto('/ops/sources');
    await expect(main(page).getByRole('alert')).toContainText('Try again in a moment.');
    await expect(rows(page)).toHaveCount(0);
  });

  test('an error never shows more than the envelope: no stack, path or address', async ({ page }) => {
    await asRole(page, 'operator');
    await page.route(apiSync, (route) =>
      route.fulfill({
        status: 502,
        contentType: 'text/html',
        body: '<pre>panic at /srv/goldie/x.go:12 10.0.0.7:8080</pre>',
      }),
    );
    await page.goto('/ops/sources');
    const alert = main(page).getByRole('alert');
    await expect(alert).toBeVisible();
    await expect(alert).not.toContainText('panic');
    await expect(alert).not.toContainText('10.0.0.7');
    await expect(alert).not.toContainText('/srv/');
  });

  test('a reader sees "This page is for operators." and no Source data', async ({ page }) => {
    await asRole(page, 'reader');
    await page.route(apiSync, (route) =>
      json(route, 403, envelope('forbidden', 'Operators only.', 'Use an operator token.')),
    );
    await page.goto('/ops/sources');
    await expect(main(page)).toContainText('This page is for operators.');
    await expect(rows(page)).toHaveCount(0);
  });

  test('renders a hostile last_error as text, never as markup', async ({ page }) => {
    await asRole(page, 'operator');
    await stubSync(page, [
      { ...failing, last_error: '<img src=x onerror=window.__pwn=1><script>window.__pwn=2</script>' },
    ]);
    await page.goto('/ops/sources');
    await expect(rows(page).first()).toContainText('<img src=x onerror=window.__pwn=1>');
    expect(await main(page).locator('img, script').count()).toBe(0);
    expect(await page.evaluate(() => (window as unknown as { __pwn?: number }).__pwn)).toBeUndefined();
  });

  test('a long error wraps without horizontal scroll at 375 px', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 800 });
    await asRole(page, 'operator');
    await stubSync(page, [{ ...failing, last_error: 'global/' + 'a-very-long-path-segment-'.repeat(12) + '.md: bad' }]);
    await page.goto('/ops/sources');
    await expect(rows(page)).toHaveCount(1);
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow, 'no horizontal scroll').toBeLessThanOrEqual(0);
  });

  test('the nav link moves focus to the h1, which matches :focus-visible', async ({ page }) => {
    await asRole(page, 'operator');
    await stubSync(page, [healthy]);
    await page.goto('/');
    await sourcesLink(page).focus();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL((u) => u.pathname === '/ops/sources');
    await expect(h1(page)).toHaveText('Sources');
    await expect(h1(page)).toBeFocused();
    expect(await h1(page).evaluate((el) => el.matches(':focus-visible'))).toBe(true);
  });

  test('an operator Resync posts to the sync endpoint, shows "Syncing…" disabled until it ends, then refreshes the rows', async ({
    page,
  }) => {
    await asOperatorWithCsrf(page);
    const sync = stubResync(page, [failing], [healthy], (route) => json(route, 202, {}));
    await page.goto('/ops/sources');
    await expect(rows(page).first()).toContainText(failing.last_error!);
    await expect(resync(page)).toHaveText('Resync');
    await expect(resync(page)).toBeEnabled();

    await resync(page).click();
    await expect(resync(page)).toHaveText('Syncing…');
    await expect(resync(page)).toBeDisabled();
    expect(sync.posts, 'one POST so far').toHaveLength(1);
    expect(sync.posts[0].method()).toBe('POST');

    sync.release();
    await expect(resync(page)).toHaveText('Resync');
    await expect(resync(page)).toBeEnabled();
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).not.toContainText(failing.last_error!);
    await expect(rows(page).first()).toContainText('1111111');
    expect(sync.posts, 'still one POST').toHaveLength(1);
  });

  test('the Resync request carries the session CSRF token', async ({ page }) => {
    await asOperatorWithCsrf(page);
    const sync = stubResync(page, [healthy], [healthy], (route) => json(route, 202, {}));
    sync.release();
    await page.goto('/ops/sources');
    await resync(page).click();
    await expect.poll(() => sync.posts.length).toBe(1);
    expect(await sync.posts[0].headerValue('x-goldie-csrf')).toBe(csrf);
  });

  test('a 429 sync_running shows its details.hint and the button works again', async ({ page }) => {
    await asOperatorWithCsrf(page);
    const sync = stubResync(page, [failing], [failing], (route) =>
      json(route, 429, envelope('sync_running', 'A sync is already running.', 'Wait for the running sync to end.')),
    );
    sync.release();
    await page.goto('/ops/sources');
    await resync(page).click();
    await expect(main(page).getByRole('alert')).toContainText('Wait for the running sync to end.');
    await expect(resync(page)).toHaveText('Resync');
    await expect(resync(page)).toBeEnabled();
    await expect(rows(page)).toHaveCount(1);
  });

  test('any other Resync error shows as text, never more than the envelope', async ({ page }) => {
    await asOperatorWithCsrf(page);
    const sync = stubResync(page, [failing], [failing], (route) =>
      route.fulfill({
        status: 502,
        contentType: 'text/html',
        body: '<pre>panic at /srv/goldie/x.go:12 10.0.0.7:8080</pre>',
      }),
    );
    sync.release();
    await page.goto('/ops/sources');
    await resync(page).click();
    const alert = main(page).getByRole('alert');
    await expect(alert).toBeVisible();
    await expect(alert).not.toContainText('panic');
    await expect(alert).not.toContainText('10.0.0.7');
    await expect(alert).not.toContainText('/srv/');
    await expect(resync(page)).toBeEnabled();
  });

  test('a 5xx Resync error shows its details.hint', async ({ page }) => {
    await asOperatorWithCsrf(page);
    const sync = stubResync(page, [healthy], [healthy], (route) =>
      json(route, 500, envelope('internal', 'Something went wrong.', 'Try again in a moment.')),
    );
    sync.release();
    await page.goto('/ops/sources');
    await resync(page).click();
    await expect(main(page).getByRole('alert')).toContainText('Try again in a moment.');
  });

  test('a reader does not see the Resync button', async ({ page }) => {
    await asRole(page, 'reader');
    await page.goto('/ops/sources');
    await expect(main(page)).toContainText('This page is for operators.');
    await expect(page.getByRole('button', { name: /resync|syncing/i })).toHaveCount(0);
  });

  test('the Resync button shows a visible focus ring on keyboard focus', async ({ page }) => {
    await asOperatorWithCsrf(page);
    await stubSync(page, [healthy]);
    await page.goto('/ops/sources');
    await resync(page).focus();
    await page.keyboard.press('Shift+Tab');
    await page.keyboard.press('Tab');
    await expect(resync(page)).toBeFocused();
    expect(await resync(page).evaluate((el) => el.matches(':focus-visible'))).toBe(true);
    await expectFocusRing(resync(page));
  });
});

// #783: the Resync error closes a message lacking . ? or ! before its hint (handoff §8), as the
// article page does (#759).
test.describe('the Resync error: message then hint (#783)', () => {
  const norm = (s: string) => s.replace(/\s+/g, ' ').trim();
  const refuse = async (page: Page, message: string, hint: string) => {
    await asOperatorWithCsrf(page);
    const sync = stubResync(page, [failing], [failing], (route) => json(route, 429, envelope('sync_running', message, hint)));
    await page.goto('/ops/sources');
    sync.release();
    await resync(page).click();
    return main(page).getByRole('alert');
  };

  test('a message without a closing period gets one before the hint', async ({ page }) => {
    const alert = await refuse(page, 'A sync is already running', 'Wait for the running sync to end.');
    await expect(alert).toBeVisible();
    expect(norm(await alert.innerText())).toBe('A sync is already running. Wait for the running sync to end.');
  });

  test('a message already ending in a question mark gets nothing extra before the hint', async ({ page }) => {
    const alert = await refuse(page, 'Is a sync already running?', 'Wait for the running sync to end.');
    await expect(alert).toBeVisible();
    expect(norm(await alert.innerText())).toBe('Is a sync already running? Wait for the running sync to end.');
  });

  test('a message without a period and without a hint is rendered unchanged', async ({ page }) => {
    const alert = await refuse(page, 'A sync is already running', '');
    await expect(alert).toBeVisible();
    expect(norm(await alert.innerText())).toBe('A sync is already running');
  });
});
