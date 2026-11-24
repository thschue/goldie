// Sign in, the auth guard and sign out (#495, handoff §2.3, §5, §8). The
// browser is the seam: what a person sees and does at /signin, the redirect
// every other route makes without a session, the role chip and its Sign out,
// and the banner a later 401 shows. Specs that are not about signing in run
// signed in through the storageState setup, so every block here that needs an
// anonymous browser says so. ./run ux passes the throwaway token as
// GOLDIE_UX_TOKEN.
import { expect, test, type Page } from '@playwright/test';
import { eachViewport, evidence, menuButton, problems } from './ux';

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

// #728 (handoff §2.3 SignIn): the sign-in page is a bare page, not a screen in
// the app shell. Before sign-in the nav is of no use, since every link in it
// redirects back to /signin. Every other route keeps the shell.
eachViewport((width) => {
  test.describe('the sign-in page has no app shell', () => {
    test.use({ storageState: anonymous });

    test('there is no top bar, no navigation landmark and no menu button', async ({ page }) => {
      await page.goto('/signin');
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in');
      await expect(page.getByRole('banner'), 'no top bar').toHaveCount(0);
      await expect(page.locator('header'), 'no top bar element').toHaveCount(0);
      await expect(page.getByRole('navigation'), 'no side nav').toHaveCount(0);
      await expect(page.getByRole('button', { name: /menu/i }), 'no menu button').toHaveCount(0);
      await expect(menuButton(page)).toHaveCount(0);
      await expect(page.getByRole('link'), 'no link leads away from the page').toHaveCount(0);
    });

    test('the logo with the wordmark is centred above the centred card', async ({ page }) => {
      await page.goto('/signin');
      const heading = page.getByRole('heading', { level: 1 });
      const wordmark = page.getByText(/^goldie$/i);
      await expect(wordmark, 'the wordmark is visible, not screen-reader-only').toBeVisible();
      const logo = page.locator('svg').first();
      await expect(logo, 'the mark is drawn').toBeVisible();
      const vw = page.viewportSize()!.width;
      const mark = (await logo.boundingBox())!;
      const word = (await wordmark.boundingBox())!;
      const title = (await heading.boundingBox())!;
      const field = (await tokenField(page).boundingBox())!;
      expect(word.width, 'the wordmark has a real box').toBeGreaterThan(0);
      expect(mark.y + mark.height, 'the mark is above the card heading').toBeLessThanOrEqual(title.y);
      expect(word.y + word.height, 'the wordmark is above the card heading').toBeLessThanOrEqual(title.y);
      expect(title.y, 'the form is below the heading').toBeLessThan(field.y);
      // The logo group (mark and wordmark) and the card are each centred on the page.
      const left = Math.min(mark.x, word.x);
      const right = Math.max(mark.x + mark.width, word.x + word.width);
      expect(Math.abs((left + right) / 2 - vw / 2), 'the logo is centred').toBeLessThanOrEqual(8);
      const card = (await page.getByRole('main').locator('form').evaluate((f) => {
        const box = (f.closest('.card') ?? f.parentElement)!.getBoundingClientRect();
        return { x: box.x, width: box.width };
      }))!;
      expect(Math.abs(card.x + card.width / 2 - vw / 2), 'the card is centred').toBeLessThanOrEqual(8);
      expect(card.width, 'the card fits the width').toBeLessThanOrEqual(vw);
      if (width >= 768) expect(card.width, 'the card is a card, not full width').toBeLessThan(vw);
    });

    test('the page keeps one main landmark, one h1, and a skip link that targets main or is gone', async ({ page }) => {
      await page.goto('/signin');
      await expect(page.getByRole('main')).toHaveCount(1);
      await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in');
      await expect(page.getByRole('main').getByRole('heading', { level: 1 }), 'the h1 is inside main').toHaveCount(1);
      const skips = page.getByRole('link', { name: /skip to/i });
      if ((await skips.count()) > 0) {
        await expect(skips).toHaveAttribute('href', '#main');
        await expect(page.locator('main#main')).toHaveCount(1);
      }
    });

    test('the card keeps the existing content and focus order starts at the token field', async ({ page }) => {
      await page.goto('/signin');
      const main = page.getByRole('main');
      await expect(main.getByLabel(/token/i)).toBeVisible();
      await expect(main.getByRole('button', { name: /sign in/i })).toBeVisible();
      await expect(main).toContainText('Exchanged once for a secure session. The token is never stored in your browser.');
      // Nothing of a shell sits in the focus order: at most the skip link (which targets main) comes first.
      await page.keyboard.press('Tab');
      if (!(await tokenField(page).evaluate((el) => el === document.activeElement))) {
        await expect(page.getByRole('link', { name: /skip to/i }), 'the only thing before the field is the skip link').toBeFocused();
        await page.keyboard.press('Tab');
      }
      await expect(tokenField(page), 'the token field is next in focus order').toBeFocused();
    });

    test('a wrong token keeps the page shell-less and shows the alert in the card', async ({ page }) => {
      await page.goto('/signin');
      // Stubbed, as the other per-viewport specs do: a real failure per width and theme would trip the
      // sign-in limiter (10 per address per minute) and give a later sign-in a 429.
      await page.route('**/api/v1/session', (route) =>
        route.request().method() === 'POST'
          ? route.fulfill({ status: 401, contentType: 'application/json', body: envelope('unauthorized', 'missing or invalid API token', 'Send a configured API token.') })
          : route.continue(),
      );
      await signIn(page, wrongToken);
      await expect(page.getByRole('main').getByRole('alert')).toContainText(wrongCopy);
      await expect(page.getByRole('navigation')).toHaveCount(0);
      await expect(page.getByRole('banner')).toHaveCount(0);
    });

    test('signing in lands on next inside the shell', async ({ page }) => {
      await page.goto('/decisions');
      await expect(page).toHaveURL((u) => u.pathname === '/signin');
      await expect(page.getByRole('navigation')).toHaveCount(0);
      await signIn(page);
      await expect(page).toHaveURL((u) => u.pathname === '/decisions');
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('Decisions');
      await expect(page.getByRole('banner'), 'the top bar is back').toBeVisible();
      await expect(page.getByRole('banner').getByRole('button', { name: /reader|operator/i })).toBeVisible();
      if (width >= 1024) {
        await expect(page.getByRole('navigation')).toBeVisible();
        await expect(menuButton(page)).toBeHidden();
      } else {
        await expect(menuButton(page)).toBeVisible();
      }
      await expect(page.getByRole('main')).toHaveCount(1);
    });

    test('signing in from the bare page lands on / in the shell with a navigation landmark and the menu at drawer widths', async ({ page }) => {
      await page.goto('/signin');
      await expect(page.getByRole('navigation')).toHaveCount(0);
      await expect(menuButton(page)).toHaveCount(0);
      await signIn(page);
      await expect(page).toHaveURL((u) => u.pathname === '/');
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('What are you deciding today?');
      await expect(page.getByRole('navigation').or(menuButton(page)).first()).toBeVisible();
      if (width >= 1024) await expect(page.getByRole('navigation')).toBeVisible();
      else await expect(menuButton(page)).toBeVisible();
    });
  });

  test.describe('signed in, the shell stays on every other route', () => {
    for (const route of ['/', '/decisions', '/assets/skills', '/ops/activity', '/no/such/route']) {
      test(`${route} renders the top bar and, by width, the nav or the menu button`, async ({ page }) => {
        await page.goto(route);
        await expect(page.getByRole('banner')).toBeVisible();
        if (width >= 1024) {
          await expect(page.getByRole('navigation')).toBeVisible();
          await expect(menuButton(page)).toBeHidden();
        } else {
          await expect(menuButton(page)).toBeVisible();
        }
      });
    }

    test('signing out returns to the shell-less sign-in page', async ({ page }) => {
      await page.goto('/');
      await expect(page.getByRole('banner')).toBeVisible();
      await page.getByRole('banner').getByRole('button', { name: /reader|operator/i }).click();
      await page.getByRole('menuitem', { name: 'Sign out' }).click();
      await expect(page).toHaveURL((u) => u.pathname === '/signin');
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in');
      await expect(page.getByRole('banner')).toHaveCount(0);
      await expect(page.getByRole('navigation')).toHaveCount(0);
      await expect(menuButton(page)).toHaveCount(0);
    });
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

// Errors look like errors (#650): a failure uses the danger tokens, a "not
// found" or a wait keeps the miss tokens, and both read at WCAG AA contrast
// in both themes. Colours are read from the rendered element and compared
// with the resolved tokens, so a renamed class cannot hide a wrong token.
type Look = { ink: string; bg: string; border: string; ratio: number };

async function look(el: import('@playwright/test').Locator): Promise<{ look: Look; tokens: Record<'danger' | 'miss', Omit<Look, 'ratio'>> }> {
  return el.evaluate((node) => {
    const ctx = document.createElement('canvas').getContext('2d')!;
    const rgb = (c: string) => {
      ctx.clearRect(0, 0, 1, 1);
      ctx.fillStyle = '#000';
      ctx.fillStyle = c;
      ctx.fillRect(0, 0, 1, 1);
      const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data;
      return [r, g, b];
    };
    const hex = (c: number[]) => c.join(',');
    const lum = (c: number[]) => {
      const [r, g, b] = c.map((v) => {
        const s = v / 255;
        return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
      });
      return 0.2126 * r + 0.7152 * g + 0.0722 * b;
    };
    const root = getComputedStyle(document.documentElement);
    const token = (n: string) => rgb(root.getPropertyValue(n).trim());
    const s = getComputedStyle(node);
    const ink = rgb(s.color);
    const bg = rgb(s.backgroundColor);
    const [hi, lo] = [lum(ink), lum(bg)].sort((a, b) => b - a);
    return {
      look: { ink: hex(ink), bg: hex(bg), border: hex(rgb(s.borderTopColor)), ratio: (hi + 0.05) / (lo + 0.05) },
      tokens: {
        danger: { ink: hex(token('--danger-ink')), bg: hex(token('--danger-bg')), border: hex(token('--danger-border')) },
        miss: { ink: hex(token('--miss-ink')), bg: hex(token('--miss-bg')), border: hex(token('--miss-border')) },
      },
    };
  });
}

async function expectDanger(el: import('@playwright/test').Locator) {
  const { look: l, tokens } = await look(el);
  expect({ ink: l.ink, bg: l.bg, border: l.border }, 'uses --danger-ink, --danger-bg and --danger-border').toEqual(tokens.danger);
  expect(l.ratio, 'AA contrast of text on its background').toBeGreaterThanOrEqual(4.5);
}

for (const theme of ['light', 'dark'] as const) {
  test.describe(`error colours, ${theme}`, () => {
    test.use({ colorScheme: theme });

    test('the sign-out failure alert uses the danger tokens', async ({ page }) => {
      await page.route('**/api/v1/session', (route) =>
        route.request().method() === 'DELETE' ? route.fulfill({ status: 500, contentType: 'application/json', body: envelope('internal', 'x', '') }) : route.continue(),
      );
      await page.goto('/decisions');
      await page.getByRole('banner').getByRole('button', { name: /reader|operator/i }).click();
      await page.getByRole('menuitem', { name: 'Sign out' }).click();
      await expectDanger(page.getByRole('alert'));
    });

    test.describe('anonymous', () => {
      test.use({ storageState: anonymous });

      test('the wrong-token error on /signin uses the danger tokens', async ({ page }) => {
        await page.goto('/signin');
        await signIn(page, wrongToken);
        await expectDanger(page.getByRole('alert'));
      });

      test('the rate-limit wait on /signin keeps the miss tokens and differs from a failure', async ({ page }) => {
        await page.route('**/api/v1/session', (route) =>
          route.request().method() === 'POST'
            ? route.fulfill({ status: 429, contentType: 'application/json', headers: { 'Retry-After': '30' }, body: envelope('rate_limited', 'x', '') })
            : route.continue(),
        );
        await page.goto('/signin');
        await signIn(page, wrongToken);
        const { look: l, tokens } = await look(page.getByRole('main').getByRole('status').filter({ hasText: 'Too many requests' }));
        expect({ ink: l.ink, bg: l.bg, border: l.border }).toEqual(tokens.miss);
        expect(l.bg, 'a wait does not look like a failure').not.toBe(tokens.danger.bg);
      });
    });

    test('the session-ended banner uses the danger tokens', async ({ page }) => {
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
      await page.setViewportSize({ width: 1280, height: 800 });
      await page.getByRole('navigation').getByRole('link', { name: 'Home', exact: true }).click();
      await expect(page.getByText(endedCopy)).toBeVisible();
      await expectDanger(page.getByRole('status').filter({ hasText: 'Your session ended' }));
    });
  });
}

test.describe('focus after a failed sign-out', () => {
  test('moves to the alert Retry button, and the keyboard alone retries and signs out', async ({ page }) => {
    let failing = true;
    await page.route('**/api/v1/session', (route) => {
      if (route.request().method() !== 'DELETE') return route.continue();
      return failing ? route.fulfill({ status: 500, contentType: 'application/json', body: envelope('internal', 'x', '') }) : route.fulfill({ status: 204 });
    });
    await page.goto('/decisions');
    const chip = page.getByRole('banner').getByRole('button', { name: /reader|operator/i });
    await chip.focus();
    await page.keyboard.press('Enter');
    const item = page.getByRole('menuitem', { name: 'Sign out' });
    await expect(item).toBeVisible();
    for (let i = 0; i < 8 && !(await item.evaluate((el) => el === document.activeElement)); i++) await page.keyboard.press('ArrowDown');
    await expect(item, 'Sign out reached with the keyboard').toBeFocused();
    await page.keyboard.press('Enter');

    const alert = page.getByRole('alert');
    await expect(alert).toBeVisible();
    await expect(alert.getByRole('button', { name: 'Retry' }), 'focus is on Retry').toBeFocused();

    // A second failure puts focus back on Retry too.
    await page.keyboard.press('Enter');
    await expect(alert.getByRole('button', { name: 'Retry' })).toBeFocused();

    failing = false;
    await page.keyboard.press('Space');
    await expect(page).toHaveURL((u) => u.pathname === '/signin');
  });

  test('a sign-out that succeeds does not leave focus on a Retry button', async ({ page }) => {
    await page.route('**/api/v1/session', (route) => (route.request().method() === 'DELETE' ? route.fulfill({ status: 204 }) : route.continue()));
    await page.goto('/');
    await page.getByRole('banner').getByRole('button', { name: /reader|operator/i }).click();
    await page.getByRole('menuitem', { name: 'Sign out' }).click();
    await expect(page).toHaveURL((u) => u.pathname === '/signin');
    await expect(page.getByRole('button', { name: 'Retry' })).toHaveCount(0);
  });
});
