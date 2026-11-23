// The first session check when the server answers with something other than a
// session (#673). A signed-in person who reloads must never be sent to /signin
// because the server was busy (429) or unavailable (503): their cookie is still
// good. They see a retryable notice on the page they asked for, and Retry
// brings the page back. A real 401 still means "not signed in" and leads to
// /signin?next=. The browser is the seam; the first GET /api/v1/session is
// stubbed on the load after a reload, the default storageState supplies a valid
// session, and no error copy is pinned here beyond the Retry control.
import { expect, test, type Page, type Route } from '@playwright/test';
import { eachViewport, evidence } from './ux';

const envelope = (code: string, message: string) => JSON.stringify({ error: { code, message, details: { hint: 'Try again in a moment.' } } });

const retry = (page: Page) => page.getByRole('button', { name: /retry|try again/i });
const chip = (page: Page) => page.getByRole('banner').getByRole('button', { name: /reader|operator/i });

// Stubs GET /api/v1/session with the answer until `release` is called; other
// methods and paths pass through.
async function stubSession(page: Page, answer: { status: number; headers?: Record<string, string>; body: string }) {
  let stubbing = true;
  await page.route('**/api/v1/session', (route) => {
    if (!stubbing || route.request().method() !== 'GET') return route.continue();
    return route.fulfill({ status: answer.status, contentType: 'application/json', headers: answer.headers, body: answer.body });
  });
  return () => {
    stubbing = false;
  };
}

const failures = [
  { name: '429 Too Many Requests', status: 429, headers: { 'Retry-After': '1' }, body: envelope('rate_limited', 'too many requests') },
  { name: '503 Service Unavailable', status: 503, headers: { 'Retry-After': '1' }, body: envelope('unavailable', 'the server is not ready') },
] as const;

test.describe('a reload while the session check is refused or fails', () => {
  for (const f of failures) {
    test(`a ${f.name} on the session check keeps the page, shows a retry and no sign-in form`, async ({ page }) => {
      await page.goto('/decisions');
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('Decisions');
      const release = await stubSession(page, f);
      await page.reload();

      // The page settles on a retryable notice, not on the sign-in form.
      await expect(retry(page).first(), 'a retryable error or notice').toBeVisible();
      await expect(page).toHaveURL((u) => u.pathname === '/decisions' && !u.searchParams.has('next'));
      await expect(page.getByRole('heading', { level: 1, name: 'Sign in' })).toHaveCount(0);
      await expect(page.getByLabel(/token/i), 'no sign-in form').toHaveCount(0);
      await expect(page.getByText(/Your session ended/), 'the session did not end').toHaveCount(0);

      // The cookie is still valid, so Retry restores the page the person asked for.
      release();
      await retry(page).first().click();
      await expect(page).toHaveURL((u) => u.pathname === '/decisions');
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('Decisions');
      await expect(chip(page)).toBeVisible();
      await expect(retry(page)).toHaveCount(0);
    });
  }

  test('a 401 on the session check still leads to /signin?next=<route>', async ({ page }) => {
    await page.goto('/decisions');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Decisions');
    await stubSession(page, { status: 401, body: envelope('unauthorized', 'no valid session') });
    await page.reload();
    await expect(page).toHaveURL((u) => u.pathname === '/signin' && u.searchParams.get('next') === '/decisions');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in');
  });
});

test('a failed Retry keeps the alert up and focused, and the unconfirmed page never mounts', async ({ page }) => {
  await page.goto('/decisions');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Decisions');
  await page.route('**/api/v1/session', async (route) => {
    if (route.request().method() !== 'GET') return route.continue();
    await new Promise((resolve) => setTimeout(resolve, 400));
    return route.fulfill({ status: 503, contentType: 'application/json', body: envelope('unavailable', 'not ready') });
  });
  await page.reload();
  await expect(retry(page)).toBeVisible();
  await retry(page).click();
  // In flight: the alert is still up, Retry is busy and the page is not shown.
  await expect(retry(page)).toHaveAttribute('aria-disabled', 'true');
  await expect(page.getByRole('heading', { level: 1, name: 'Decisions' })).toHaveCount(0);
  // Answered with another 503: still the alert, and Retry holds focus.
  await expect(retry(page)).toHaveAttribute('aria-disabled', 'false');
  await expect(page.getByRole('alert')).toBeVisible();
  await expect(retry(page)).toBeFocused();
  await expect(page.getByRole('heading', { level: 1, name: 'Decisions' })).toHaveCount(0);
});

// Answers GET /api/v1/session from `answers` in order (the last one repeats);
// other methods and paths pass through. `served` counts the answers given.
async function stubSessionSequence(page: Page, answers: { status: number; body: string }[]) {
  const state = { served: 0 };
  await page.route('**/api/v1/session', (route) => {
    if (route.request().method() !== 'GET') return route.continue();
    const answer = answers[Math.min(state.served, answers.length - 1)];
    state.served++;
    return route.fulfill({ status: answer.status, contentType: 'application/json', body: answer.body });
  });
  return state;
}

const unavailable = { status: 503, body: envelope('unavailable', 'not ready') };

test.describe('the session check alert and its focus (#677)', () => {
  test('the alert moves focus to Retry when it first appears', async ({ page }) => {
    await page.goto('/decisions');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Decisions');
    await stubSessionSequence(page, [unavailable]);
    await page.reload();
    await expect(page.getByRole('alert')).toBeVisible();
    // No click and no key press: the person has not touched the page yet.
    await expect(retry(page)).toBeFocused();
  });

  test('each repeat failure of Retry mounts a new alert and returns focus to Retry', async ({ page }) => {
    await page.goto('/decisions');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Decisions');
    const stub = await stubSessionSequence(page, [unavailable]);
    await page.reload();
    await expect(page.getByRole('alert')).toBeVisible();

    for (let round = 1; round <= 2; round++) {
      const before = await page.getByRole('alert').elementHandle();
      expect(before, 'the alert is on the page').not.toBeNull();
      const served = stub.served;
      await retry(page).click();
      await expect.poll(() => stub.served, 'the retry reached the server').toBeGreaterThan(served);
      await expect(retry(page)).toHaveAttribute('aria-disabled', 'false');
      // A remounted alert is a new element, which a screen reader announces again.
      await expect
        .poll(() => before!.evaluate((el) => el.isConnected), `round ${round}: the old alert element was replaced`)
        .toBe(false);
      await expect(page.getByRole('alert')).toHaveCount(1);
      await expect(retry(page), `round ${round}: focus is back on Retry`).toBeFocused();
      await expect(page.getByRole('heading', { level: 1, name: 'Decisions' })).toHaveCount(0);
    }
  });

  test('a Retry that gets a 401 leads to /signin?next=<current route>', async ({ page }) => {
    await page.goto('/decisions');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Decisions');
    await stubSessionSequence(page, [unavailable, { status: 401, body: envelope('unauthorized', 'no valid session') }]);
    await page.reload();
    await expect(retry(page)).toBeVisible();
    await retry(page).click();
    await expect(page).toHaveURL((u) => u.pathname === '/signin' && u.searchParams.get('next') === '/decisions');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in');
    await expect(retry(page)).toHaveCount(0);
  });
});

eachViewport((width, theme) => {
  test('the session check failed state is captured and axe-clean', async ({ page }) => {
    await page.goto('/decisions');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Decisions');
    await stubSession(page, { status: 503, body: envelope('unavailable', 'not ready') });
    await page.reload();
    await expect(retry(page)).toBeVisible();
    await evidence(page, width, theme, '-check-failed', 'session-errors');
  });
});

// A 401 on a read that is not the session check (#685). A signed-in person
// whose session expired sees the session-ended banner with its sign-in link on
// a Project page and on a search, the same as on any other read. A signed-out
// visitor is never told a session ended.
const endedCopy = /Your session ended\./;
const unauthorized = (route: Route) =>
  route.fulfill({ status: 401, contentType: 'application/json', body: envelope('unauthorized', 'no valid session') });
const shop = 'github.com/acme/shop';
const apiProjects = /\/api\/v1\/projects$/;
const apiItems = /\/api\/v1\/projects\/[^/]+\/items$/;
const apiSearch = /\/api\/v1\/search(\?|$)/;

test.describe('a 401 on a read ends the session (#685)', () => {
  test('a Project page read that answers 401 shows the session-ended banner and its sign-in link', async ({ page }) => {
    await page.route(apiProjects, (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ projects: [shop] }) }),
    );
    await page.route(apiItems, unauthorized);
    await page.goto(`/?project=${encodeURIComponent(shop)}`);

    const banner = page.getByRole('status').filter({ hasText: 'Your session ended' });
    await expect(banner).toBeVisible();
    await expect(banner.getByRole('link', { name: 'Sign in again' })).toHaveAttribute('href', /^\/signin\?next=/);
  });

  test('a search that answers 401 shows the session-ended banner and its sign-in link', async ({ page }) => {
    await page.route(apiSearch, unauthorized);
    await page.goto('/?q=database');

    const banner = page.getByRole('status').filter({ hasText: 'Your session ended' });
    await expect(banner).toBeVisible();
    await expect(banner.getByRole('link', { name: 'Sign in again' })).toHaveAttribute('href', /^\/signin\?next=/);
  });

  test.describe('signed out', () => {
    test.use({ storageState: { cookies: [], origins: [] } });

    test('a 401 on a Project page read or a search does not show the banner', async ({ page }) => {
      await page.route(apiItems, unauthorized);
      await page.route(apiSearch, unauthorized);
      for (const path of [`/?project=${encodeURIComponent(shop)}`, '/?q=database']) {
        await page.goto(path);
        await expect(page).toHaveURL((u) => u.pathname === '/signin');
        await expect(page.getByText(endedCopy)).toHaveCount(0);
      }
    });
  });
});
