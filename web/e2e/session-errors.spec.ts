// The first session check when the server answers with something other than a
// session (#673). A signed-in person who reloads must never be sent to /signin
// because the server was busy (429) or unavailable (503): their cookie is still
// good. They see a retryable notice on the page they asked for, and Retry
// brings the page back. A real 401 still means "not signed in" and leads to
// /signin?next=. The browser is the seam; the first GET /api/v1/session is
// stubbed on the load after a reload, the default storageState supplies a valid
// session, and no error copy is pinned here beyond the Retry control.
import { expect, test, type Page } from '@playwright/test';

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
