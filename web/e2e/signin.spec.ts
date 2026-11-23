// Sign in, the auth guard and sign out (#495, handoff §2.3, §5, §8). The
// browser is the seam: what a person sees and does at /signin, the redirect
// every other route makes without a session, the role chip and its Sign out,
// and the banner a later 401 shows. Specs that are not about signing in run
// signed in through the storageState setup, so every block here that needs an
// anonymous browser says so. ./run ux passes the throwaway token as
// GOLDIE_UX_TOKEN.
import { expect, test, type Page } from '@playwright/test';
import { eachViewport, evidence, problems } from './ux';

const anonymous = { cookies: [], origins: [] };
const token = process.env.GOLDIE_UX_TOKEN ?? '';
const wrongToken = 'not-a-configured-token-0123456789abcdef0123456789abcdef';

const wrongCopy = "That token wasn't accepted. Check it and try again, or ask your platform team for a new one.";
const endedCopy = /Your session ended\.\s*Sign in again to keep going\./;

const tokenField = (page: Page) => page.getByLabel(/token/i);
const submit = (page: Page) => page.getByRole('button', { name: /sign in/i });
// The rendered text with typographic apostrophes folded, so the copy table's
// plain quote matches either.
const plain = (s: string | null) => (s ?? '').replace(/\s+/g, ' ').replace(/[’‘]/g, "'").trim();

async function signIn(page: Page, value = token) {
  await tokenField(page).fill(value);
  await submit(page).click();
}

function sessionBody(role: 'reader' | 'operator') {
  return JSON.stringify({ name: 'ux', role });
}

const envelope = (code: string, message: string, hint: string) => JSON.stringify({ error: { code, message, details: { hint } } });

test.describe('the test setup', () => {
  test('the throwaway token reaches the specs and the default browser is signed in by storageState', async ({ request }) => {
    expect(token, 'GOLDIE_UX_TOKEN is set by ./run ux').not.toBe('');
    const res = await request.get('/api/v1/session');
    expect(res.status(), 'the default context carries a session cookie').toBe(200);
    expect(await res.json()).toMatchObject({ role: expect.stringMatching(/^(reader|operator)$/) });
  });
});

test.describe('anonymous', () => {
  test.use({ storageState: anonymous });

  test.describe('the auth guard', () => {
    const routes: [string, string][] = [
      ['/', '/'],
      ['/decisions', '/decisions'],
      ['/assets/skills', '/assets/skills'],
      ['/assets/agents', '/assets/agents'],
      ['/assets/loops', '/assets/loops'],
      ['/assets/instructions', '/assets/instructions'],
      ['/items/skill/x', '/items/skill/x'],
      ['/ops/activity', '/ops/activity'],
      ['/ops/sources', '/ops/sources'],
      ['/decisions?status=accepted&scope=org', '/decisions?status=accepted&scope=org'],
      ['/no/such/route', '/no/such/route'],
    ];
    for (const [route, next] of routes) {
      test(`an unauthenticated visit to ${route} redirects to /signin?next=<route>`, async ({ page }) => {
        await page.goto(route);
        await expect(page).toHaveURL((u) => u.pathname === '/signin');
        const url = new URL(page.url());
        expect(url.searchParams.get('next'), 'next carries the route and its query').toBe(next);
        await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in');
      });
    }

    test('/signin itself does not redirect to itself', async ({ page }) => {
      await page.goto('/signin');
      await expect(page).toHaveURL((u) => u.pathname === '/signin' && !u.searchParams.has('next'));
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in');
    });

    test('after sign-in the user returns to the route they asked for, query included', async ({ page }) => {
      await page.goto('/decisions?status=accepted');
      await signIn(page);
      await expect(page).toHaveURL((u) => u.pathname === '/decisions' && u.search === '?status=accepted');
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('Decisions');
    });

    test('with no next, sign-in goes to /', async ({ page }) => {
      await page.goto('/signin');
      await signIn(page);
      await expect(page).toHaveURL((u) => u.pathname === '/' && u.search === '');
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('What are you deciding today?');
    });

    // next is honoured only as a same-origin path. Each value is put in the
    // address bar as an attacker would write it.
    const hostile = ['//evil.example', '/\\evil', 'https://evil.example', 'javascript:alert(1)', '/%0d%0a', '/%2f%2fevil'];
    for (const next of hostile) {
      test(`next=${next} falls back to / and leaves nothing for another origin`, async ({ page, baseURL }) => {
        const seen = await problems(page, baseURL ?? '');
        await page.goto(`/signin?next=${next}`);
        await signIn(page);
        await expect(page.getByRole('heading', { level: 1 })).toHaveText('What are you deciding today?');
        const url = new URL(page.url());
        expect(url.origin).toBe(new URL(baseURL ?? '').origin);
        expect(url.pathname).toBe('/');
        expect(seen.external, 'no request to another origin').toEqual([]);
        expect(seen.errors, 'no script ran or CSP violation').toEqual([]);
      });
    }
  });

  test.describe('the form', () => {
    test('the heading, the lead and the token help follow §8 and the role line and footer links are left out', async ({ page }) => {
      await page.goto('/signin');
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in');
      const main = page.getByRole('main');
      const text = plain(await main.innerText());
      expect(text).toMatch(/The golden path for \S.* — the same guidance your coding agents get\./);
      expect(text).toContain('Exchanged once for a secure session. The token is never stored in your browser.');
      expect(text, 'the lead names an org, not the placeholder').not.toContain('<org>');
      // The org is set in bold (§8: **`<org>`**).
      await expect(main.locator('strong, b').first()).toBeVisible();
      expect(text, 'no role line').not.toMatch(/\b(reader|operator)\b/i);
      await expect(main.locator('footer')).toHaveCount(0);
      await expect(page.getByRole('contentinfo')).toHaveCount(0);
      await expect(main.getByRole('link')).toHaveCount(0);
    });

    test('the token field has a visible label, type=password and autocomplete=off', async ({ page }) => {
      await page.goto('/signin');
      const field = tokenField(page);
      await expect(field).toBeVisible();
      await expect(field).toHaveAttribute('type', 'password');
      await expect(field).toHaveAttribute('autocomplete', 'off');
      const id = await field.getAttribute('id');
      expect(id, 'the field has an id for its label').toBeTruthy();
      const label = page.locator(`label[for="${id}"]`);
      await expect(label, 'a <label> is visible, not just an aria-label').toBeVisible();
      expect(((await label.textContent()) ?? '').trim()).not.toBe('');
    });

    test('the form is submitted in JavaScript as a JSON POST, never as a native form post', async ({ page }) => {
      const navPosts: string[] = [];
      page.on('request', (req) => {
        if (req.isNavigationRequest() && req.method() !== 'GET') navPosts.push(`${req.method()} ${req.url()}`);
      });
      await page.goto('/signin');
      const posted = page.waitForRequest((r) => new URL(r.url()).pathname === '/api/v1/session' && r.method() === 'POST');
      await tokenField(page).fill(wrongToken);
      await tokenField(page).press('Enter');
      const req = await posted;
      expect(req.headers()['content-type'] ?? '').toContain('application/json');
      expect(req.postDataJSON()).toEqual({ token: wrongToken });
      expect(navPosts, 'no native form navigation').toEqual([]);
      // form-action 'none' blocks a native post silently, so the page must answer.
      await expect(page.getByRole('alert')).toBeVisible();
      await expect(page).toHaveURL((u) => u.pathname === '/signin');
    });

    test('the token never reaches the URL, localStorage, sessionStorage or document.cookie, and the field is cleared after submit', async ({ page }) => {
      await page.goto('/signin');
      await signIn(page, wrongToken);
      await expect(page.getByRole('alert')).toBeVisible();
      await expect(tokenField(page)).toHaveValue('');
      expect(page.url()).not.toContain(wrongToken);
      const held = await page.evaluate(
        (t) => ({
          local: JSON.stringify({ ...localStorage }).includes(t),
          session: JSON.stringify({ ...sessionStorage }).includes(t),
          cookie: document.cookie.includes(t),
          history: JSON.stringify(history.state ?? null).includes(t),
        }),
        wrongToken,
      );
      expect(held).toEqual({ local: false, session: false, cookie: false, history: false });
    });

    test('a good token is cleared, kept out of storage, and the session cookie is HttpOnly', async ({ page, context }) => {
      await page.goto('/signin');
      await signIn(page);
      await expect(page).toHaveURL((u) => u.pathname === '/');
      expect(page.url()).not.toContain(token);
      const held = await page.evaluate(
        (t) => ({
          local: JSON.stringify({ ...localStorage }).includes(t),
          session: JSON.stringify({ ...sessionStorage }).includes(t),
          cookie: document.cookie.includes(t),
        }),
        token,
      );
      expect(held).toEqual({ local: false, session: false, cookie: false });
      const cookies = await context.cookies();
      expect(cookies.length, 'the server set a session cookie').toBeGreaterThan(0);
      for (const c of cookies) {
        expect(c.httpOnly, `${c.name} is HttpOnly`).toBe(true);
        expect(c.value, `${c.name} is not the token`).not.toContain(token);
      }
    });
  });

  test.describe('errors', () => {
    test('a wrong token shows a danger Alert with the copy and the hint, tied to the field with aria-describedby', async ({ page }) => {
      await page.route('**/api/v1/session', (route) =>
        route.request().method() === 'POST'
          ? route.fulfill({ status: 401, contentType: 'application/json', body: envelope('unauthorized', 'missing or invalid API token', 'Send a configured API token.') })
          : route.continue(),
      );
      await page.goto('/signin');
      await signIn(page, wrongToken);
      const alert = page.getByRole('alert');
      await expect(alert).toHaveCount(1);
      expect(plain(await alert.innerText())).toContain(wrongCopy);
      expect(plain(await alert.innerText()), 'the envelope hint follows the copy').toContain('Send a configured API token.');
      expect(plain(await alert.innerText()), 'never the raw envelope').not.toMatch(/unauthorized|\{|"error"/);
      const id = await alert.getAttribute('id');
      expect(id, 'the Alert has an id').toBeTruthy();
      const describedby = ((await tokenField(page).getAttribute('aria-describedby')) ?? '').split(/\s+/);
      expect(describedby, 'the field is described by the Alert').toContain(id!);
      await expect(page).toHaveURL((u) => u.pathname === '/signin');
      // danger, not info or warning: red-ish, and not the gold of a warning.
      const colour = await alert.evaluate((el) => {
        const ctx = document.createElement('canvas').getContext('2d')!;
        const s = getComputedStyle(el);
        const rgb = (c: string) => {
          ctx.clearRect(0, 0, 1, 1);
          ctx.fillStyle = '#000';
          ctx.fillStyle = c;
          ctx.fillRect(0, 0, 1, 1);
          const [r, g, b, a] = ctx.getImageData(0, 0, 1, 1).data;
          return { r, g, b, a };
        };
        return [s.borderLeftColor, s.borderTopColor, s.backgroundColor].map(rgb).filter((c) => c.a > 0);
      });
      expect(colour.some(({ r, g, b }) => r > g + 40 && r > b + 40), 'a danger (red) Alert').toBe(true);
    });

    test('the real server refuses a wrong token the same way, and a second wrong token replaces the Alert rather than stacking it', async ({ page }) => {
      await page.goto('/signin');
      await signIn(page, wrongToken);
      await expect(page.getByRole('alert')).toHaveCount(1);
      expect(plain(await page.getByRole('alert').innerText())).toContain(wrongCopy);
      await signIn(page, wrongToken + 'x');
      await expect(page.getByRole('alert')).toHaveCount(1);
      await expect(page).toHaveURL((u) => u.pathname === '/signin');
    });

    test('a 401 during sign-in is not a session ending', async ({ page }) => {
      await page.goto('/signin');
      await signIn(page, wrongToken);
      await expect(page.getByRole('alert')).toBeVisible();
      await expect(page.getByText(endedCopy)).toHaveCount(0);
      await expect(page.getByText('Your session ended')).toHaveCount(0);
    });

    test('a 429 shows the wait in a status, disables the submit button and counts down', async ({ page }) => {
      await page.route('**/api/v1/session', (route) =>
        route.request().method() === 'POST'
          ? route.fulfill({
              status: 429,
              contentType: 'application/json',
              headers: { 'Retry-After': '4' },
              body: envelope('rate_limited', 'too many requests', 'Wait and retry.'),
            })
          : route.continue(),
      );
      await page.goto('/signin');
      await signIn(page, wrongToken);
      const status = page.getByRole('main').getByRole('status').filter({ hasText: 'Too many requests' });
      await expect(status).toHaveCount(1);
      await expect(status).toHaveText(/Too many requests — try again in [1-4] s\./);
      const first = Number(/in (\d+) s/.exec((await status.textContent()) ?? '')?.[1]);
      await expect(submit(page)).toBeDisabled();
      await expect.poll(async () => Number(/in (\d+) s/.exec((await status.textContent()) ?? '')?.[1]), { timeout: 4000 }).toBeLessThan(first);
      await expect(submit(page), 'enabled again once the wait is over').toBeEnabled({ timeout: 8000 });
      await expect(status).toHaveCount(0);
    });
  });

  test.describe('the session ends after sign-in', () => {
    test('a 401 on a later call shows the non-blocking banner and the view stays', async ({ page }) => {
      let signedIn = true;
      await page.route('**/api/v1/**', (route) => {
        const req = route.request();
        if (signedIn && req.method() === 'GET' && new URL(req.url()).pathname === '/api/v1/session') {
          return route.fulfill({ status: 200, contentType: 'application/json', body: sessionBody('reader') });
        }
        return route.fulfill({ status: 401, contentType: 'application/json', body: envelope('unauthorized', 'no valid session', 'Sign in again.') });
      });
      await page.goto('/decisions');
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('Decisions');
      signedIn = false;
      // The next client-side move makes a call, which now answers 401.
      const nav = page.getByRole('navigation').getByRole('link', { name: 'Home', exact: true });
      if (!(await nav.isVisible())) await page.getByRole('button', { name: /menu/i }).click();
      await nav.click();
      const banner = page.getByText(endedCopy);
      await expect(banner).toBeVisible();
      await expect(banner.locator('strong, b, a, button').filter({ hasText: 'Sign in again' }).first()).toBeVisible();
      await expect(page.getByRole('banner'), 'the shell stays').toBeVisible();
      await expect(page.getByRole('main')).toBeVisible();
      // Non-blocking: nothing is inert and no dialog covers the page.
      expect(await page.getByRole('main').evaluate((el) => !!el.closest('[inert]'))).toBe(false);
      await expect(page.getByRole('dialog')).toHaveCount(0);
      // Sign in again leads to /signin.
      await page.getByRole('link', { name: 'Sign in again' }).or(page.getByRole('button', { name: 'Sign in again' })).first().click();
      await expect(page).toHaveURL((u) => u.pathname === '/signin');
    });
  });
});

test.describe('signed in: the role chip and Sign out', () => {
  for (const role of ['reader', 'operator'] as const) {
    test(`the chip for GET /api/v1/session role=${role} reads "${role}"`, async ({ page }) => {
      await page.route('**/api/v1/session', (route) =>
        route.request().method() === 'GET' ? route.fulfill({ status: 200, contentType: 'application/json', body: sessionBody(role) }) : route.continue(),
      );
      await page.goto('/');
      const chip = page.getByRole('banner').getByRole('button', { name: new RegExp(role, 'i') });
      await expect(chip).toBeVisible();
      await expect(chip).toContainText(role);
      const other = role === 'reader' ? 'operator' : 'reader';
      await expect(page.getByRole('banner').getByText(other, { exact: true })).toHaveCount(0);
      // reader is an outline, operator is a blue fill.
      const look = await chip.evaluate((el) => {
        const ctx = document.createElement('canvas').getContext('2d')!;
        const rgb = (c: string) => {
          ctx.clearRect(0, 0, 1, 1);
          ctx.fillStyle = '#000';
          ctx.fillStyle = c;
          ctx.fillRect(0, 0, 1, 1);
          const [r, g, b, a] = ctx.getImageData(0, 0, 1, 1).data;
          return { r, g, b, a };
        };
        const s = getComputedStyle(el);
        return { bg: rgb(s.backgroundColor), borderWidth: s.borderTopWidth, borderStyle: s.borderTopStyle };
      });
      if (role === 'reader') {
        expect(look.bg.a, 'reader has no fill').toBe(0);
        expect(look.borderStyle).not.toBe('none');
        expect(parseFloat(look.borderWidth)).toBeGreaterThan(0);
      } else {
        expect(look.bg.a, 'operator is filled').toBeGreaterThan(0);
        expect(look.bg.b, 'the fill is blue').toBeGreaterThan(look.bg.r + 40);
        expect(look.bg.b).toBeGreaterThan(look.bg.g);
      }
    });
  }

  test('the chip shows the real session of the signed-in test user', async ({ page, request }) => {
    const res = await request.get('/api/v1/session');
    expect(res.status(), 'the default context is signed in').toBe(200);
    const role = (await res.json()).role as string;
    expect(['reader', 'operator']).toContain(role);
    await page.goto('/');
    await expect(page.getByRole('banner').getByRole('button', { name: new RegExp(role, 'i') })).toBeVisible();
  });

  test('the chip menu has Sign out, which ends the session and returns to /signin', async ({ page, context }) => {
    await page.goto('/');
    const chip = page.getByRole('banner').getByRole('button', { name: /reader|operator/i });
    await chip.click();
    await expect(chip).toHaveAttribute('aria-expanded', 'true');
    const item = page.getByRole('menuitem', { name: 'Sign out' }).or(page.getByRole('menu').getByRole('button', { name: 'Sign out' }));
    await expect(item.first()).toBeVisible();
    const deleted = page.waitForRequest((r) => new URL(r.url()).pathname === '/api/v1/session' && r.method() === 'DELETE');
    await item.first().click();
    await deleted;
    await expect(page).toHaveURL((u) => u.pathname === '/signin');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in');
    // The session is gone: the API refuses, and a protected route redirects.
    const res = await page.request.get('/api/v1/session');
    expect(res.status()).toBe(401);
    await page.goto('/decisions');
    await expect(page).toHaveURL((u) => u.pathname === '/signin' && u.searchParams.get('next') === '/decisions');
    expect((await context.cookies()).filter((c) => c.value !== '')).toEqual([]);
  });

  test('the chip menu is keyboard operable: Esc closes it and focus returns to the chip', async ({ page }) => {
    await page.goto('/');
    const chip = page.getByRole('banner').getByRole('button', { name: /reader|operator/i });
    await chip.focus();
    await page.keyboard.press('Enter');
    await expect(chip).toHaveAttribute('aria-expanded', 'true');
    await page.keyboard.press('Escape');
    await expect(chip).toHaveAttribute('aria-expanded', 'false');
    await expect(chip).toBeFocused();
  });

  test('the chip shows a caret that tells it opens a menu, and its name is still the role', async ({ page }) => {
    await page.goto('/');
    const chip = page.getByRole('banner').getByRole('button', { name: /^(reader|operator)$/i });
    await expect(chip, 'the caret is decorative: the accessible name is the role alone').toBeVisible();
    const caret = await chip.evaluate((el) => {
      const decorative = el.querySelector('svg, [aria-hidden="true"]');
      const after = getComputedStyle(el, '::after').content;
      const box = decorative?.getBoundingClientRect();
      return { element: !!box && box.width > 0 && box.height > 0, pseudo: after !== 'none' && after !== 'normal' && after !== '""' };
    });
    expect(caret.element || caret.pseudo, 'a visible caret inside the chip').toBe(true);
  });

  for (const failure of ['the server answers 500', 'the request cannot be sent'] as const) {
    test(`when ${failure}, Sign out keeps the user signed in with an error alert and a retry that then signs out`, async ({ page }) => {
      let failing = true;
      let deletes = 0;
      await page.route('**/api/v1/session', (route) => {
        if (route.request().method() !== 'DELETE') return route.continue();
        deletes++;
        if (!failing) return route.fulfill({ status: 204 });
        return failure === 'the server answers 500'
          ? route.fulfill({ status: 500, contentType: 'application/json', body: envelope('internal', 'something went wrong', '') })
          : route.abort();
      });
      await page.goto('/decisions');
      const chip = page.getByRole('banner').getByRole('button', { name: /reader|operator/i });
      await chip.click();
      await page.getByRole('menuitem', { name: 'Sign out' }).click();

      // The user is not told they are signed out.
      const alert = page.getByRole('alert');
      await expect(alert).toBeVisible();
      await expect(page).toHaveURL((u) => u.pathname === '/decisions');
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('Decisions');
      await expect(chip, 'the account chip is still there').toBeVisible();
      const retry = alert.getByRole('button', { name: /retry|try again/i }).or(page.getByRole('button', { name: /retry|try again/i }));
      await expect(retry.first()).toBeVisible();
      expect(deletes).toBe(1);
      // And the session really is still valid on the server.
      expect((await page.request.get('/api/v1/session')).status()).toBe(200);

      // Retry once the server answers: the user reaches the sign-in page.
      failing = false;
      await retry.first().click();
      await expect(page).toHaveURL((u) => u.pathname === '/signin');
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in');
      expect(deletes).toBe(2);
    });
  }

  test('a sign out that succeeds shows no error alert before reaching /signin', async ({ page }) => {
    await page.route('**/api/v1/session', (route) => (route.request().method() === 'DELETE' ? route.fulfill({ status: 204 }) : route.continue()));
    await page.goto('/');
    await page.getByRole('banner').getByRole('button', { name: /reader|operator/i }).click();
    await page.getByRole('menuitem', { name: 'Sign out' }).click();
    await expect(page).toHaveURL((u) => u.pathname === '/signin');
    await expect(page.getByRole('alert')).toHaveCount(0);
  });
});

test.describe('the first load', () => {
  // Holds GET /api/v1/session until released, as a slow server would.
  async function slowSession(page: Page) {
    let release!: () => void;
    const gate = new Promise<void>((resolve) => (release = resolve));
    await page.route('**/api/v1/session', async (route) => {
      if (route.request().method() !== 'GET') return route.continue();
      await gate;
      return route.continue();
    });
    return release;
  }
  const loadingState = (page: Page) => page.getByRole('status').filter({ hasText: /loading/i });

  test('shows a loading state while the first session check is in flight, then the page', async ({ page }) => {
    const release = await slowSession(page);
    try {
      await page.goto('/decisions', { waitUntil: 'domcontentloaded' });
      await expect(loadingState(page), 'not a blank page').toBeVisible();
      // The loading state carries the page's one h1 (axe best-practice page-has-heading-one), so the
      // check is that no view heading is there yet, and that any h1 is the loading state's.
      await expect(page.getByRole('heading', { name: 'Decisions' }), 'no view before the answer').toHaveCount(0);
      await expect(page.getByRole('heading', { level: 1 }).filter({ hasNotText: /loading/i }), 'no view heading before the answer').toHaveCount(0);
      await expect(page.getByRole('button', { name: /^(reader|operator)/i })).toHaveCount(0);
    } finally {
      release();
    }
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Decisions');
    await expect(loadingState(page), 'the loading state is gone').toHaveCount(0);
  });

  test('a visitor without a session sees the loading state, then the sign-in page', async ({ browser }) => {
    const context = await browser.newContext({ storageState: anonymous });
    const page = await context.newPage();
    const release = await slowSession(page);
    try {
      await page.goto('/decisions', { waitUntil: 'domcontentloaded' });
      await expect(loadingState(page)).toBeVisible();
    } finally {
      release();
    }
    await expect(page).toHaveURL((u) => u.pathname === '/signin');
    await expect(loadingState(page)).toHaveCount(0);
    await context.close();
  });
});

// The evidence of §12: the Sign in screen and its error states at 375, 768 and
// 1280 in light and dark, axe-clean, no horizontal scroll, targets of 44 px.
eachViewport((width, theme) => {
  test.describe('anonymous screens', () => {
    test.use({ storageState: anonymous });

    test('Sign in is captured, axe-clean, self-hosted and error-free', async ({ page, baseURL }) => {
      const seen = await problems(page, baseURL ?? '');
      const res = await page.goto('/signin');
      expect(res?.status()).toBe(200);
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in');
      await expect(tokenField(page)).toBeVisible();
      await evidence(page, width, theme, '', 'signin');
      expect(seen.external).toEqual([]);
      expect(seen.errors).toEqual([]);
    });

    test('Sign in with a wrong token is captured and axe-clean', async ({ page, baseURL }) => {
      const seen = await problems(page, baseURL ?? '');
      // Stubbed: six real failures here would trip the sign-in limiter (10 per address per minute)
      // together with the real failures of the other tests, and the 429 renders a status, not an alert.
      // The real 401 path is exercised by the tests above.
      await page.route('**/api/v1/session', (route) =>
        route.request().method() === 'POST'
          ? route.fulfill({ status: 401, contentType: 'application/json', body: envelope('unauthorized', 'missing or invalid API token', 'Tokens are case-sensitive, so copy yours exactly as issued.') })
          : route.continue(),
      );
      await page.goto('/signin');
      await signIn(page, wrongToken);
      await expect(page.getByRole('alert')).toBeVisible();
      await evidence(page, width, theme, '-error', 'signin');
      expect(seen.external).toEqual([]);
      expect(seen.errors).toEqual([]);
    });

    test('Sign in rate limited is captured and axe-clean', async ({ page, baseURL }) => {
      const seen = await problems(page, baseURL ?? '');
      await page.route('**/api/v1/session', (route) =>
        route.request().method() === 'POST'
          ? route.fulfill({ status: 429, contentType: 'application/json', headers: { 'Retry-After': '30' }, body: envelope('rate_limited', 'too many requests', 'Wait and retry.') })
          : route.continue(),
      );
      await page.goto('/signin');
      await signIn(page, wrongToken);
      await expect(page.getByRole('main').getByRole('status').filter({ hasText: 'Too many requests' })).toBeVisible();
      await evidence(page, width, theme, '-limited', 'signin');
      expect(seen.errors).toEqual([]);
    });
  });

  test('the signed-in shell with the role chip is captured and axe-clean', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByRole('banner').getByRole('button', { name: /reader|operator/i })).toBeVisible();
    await evidence(page, width, theme, '-chip', 'shell');
  });

  test('the account chip menu open with Sign out is captured and axe-clean', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('banner').getByRole('button', { name: /reader|operator/i }).click();
    await expect(page.getByRole('menuitem', { name: 'Sign out' })).toBeVisible();
    await evidence(page, width, theme, '-chip-menu', 'shell');
  });

  test('the operator chip is captured and axe-clean', async ({ page }) => {
    await page.route('**/api/v1/session', (route) =>
      route.request().method() === 'GET' ? route.fulfill({ status: 200, contentType: 'application/json', body: sessionBody('operator') }) : route.continue(),
    );
    await page.goto('/');
    await expect(page.getByRole('banner').getByRole('button', { name: /operator/i })).toBeVisible();
    await evidence(page, width, theme, '-chip-operator', 'shell');
  });

  test('the loading state is captured and axe-clean', async ({ page }) => {
    let release!: () => void;
    const gate = new Promise<void>((resolve) => (release = resolve));
    await page.route('**/api/v1/session', async (route) => {
      if (route.request().method() !== 'GET') return route.continue();
      await gate;
      return route.continue();
    });
    try {
      await page.goto('/', { waitUntil: 'domcontentloaded' });
      await expect(page.getByRole('status').filter({ hasText: /loading/i })).toBeVisible();
      await evidence(page, width, theme, '', 'loading');
    } finally {
      release();
    }
  });
});
