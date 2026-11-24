// The App shell at / (#166, handoff §3.1, §7, §9): the frame every screen
// renders into. Per width and theme: the shot, axe (with best-practice), the
// landmarks, the empty state and the top bar. Plus the nav, the routes and
// the 1024 px change of the shell.
import { expect, test, type Page, type Route } from '@playwright/test';
import { eachViewport, evidence, expectNavCollapsed, menuButton, missingBrandFonts, problems, themeToggle, withNav } from './ux';

const emptyCopy = /Nothing to show yet\. Content appears here as goldie[’']s pages come online\./;

eachViewport((width, theme) => {
  test('the shell at / is captured, axe-clean, self-hosted and error-free', async ({ page, baseURL }) => {
    const seen = await problems(page, baseURL ?? '');
    const res = await page.goto('/');
    expect(res?.status()).toBe(200);
    await expect(page.locator('html')).toHaveAttribute('lang', 'en');
    expect(await missingBrandFonts(page), 'brand fonts not loaded').toEqual([]);
    await evidence(page, width, theme);
    expect(seen.external, 'requests to other origins').toEqual([]);
    expect(seen.failed, 'same-origin 4xx/5xx or failed requests').toEqual([]);
    expect(seen.errors, 'page errors and CSP violations').toEqual([]);
  });

  test('the shell has header, nav and main landmarks and one h1; Home has no empty-state placeholder (#721) and /items/skill/x keeps it', async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('header')).toHaveCount(1);
    await expect(page.getByRole('banner')).toBeVisible();
    await expect(page.getByRole('main')).toBeVisible();
    await expect(page.locator('nav[aria-label]')).toHaveCount(1);
    await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('What are you deciding today?');
    await expect(page.getByRole('main')).not.toContainText(emptyCopy);
    await page.goto('/items/skill/x');
    await expect(page.getByRole('main').getByRole('status')).toHaveText(emptyCopy);
  });

  test('the empty-state panel is neutral, never gold', async ({ page }) => {
    await page.goto('/items/skill/x');
    const colours = await page.getByRole('main').getByRole('status').evaluate((el) => {
      // Normalise any CSS colour (oklch included) to sRGB through a canvas.
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
      return [s.backgroundColor, s.borderTopColor, s.borderLeftColor].map(rgb);
    });
    for (const { r, g, b, a } of colours) {
      if (a === 0) continue;
      const max = Math.max(r, g, b);
      const min = Math.min(r, g, b);
      const sat = max === 0 ? 0 : (max - min) / max;
      let hue = 0;
      if (max !== min) {
        if (max === r) hue = (60 * ((g - b) / (max - min)) + 360) % 360;
        else if (max === g) hue = 60 * ((b - r) / (max - min)) + 120;
        else hue = 60 * ((r - g) / (max - min)) + 240;
      }
      const gold = sat > 0.25 && hue >= 30 && hue <= 60;
      expect(gold, `rgb(${r},${g},${b}) is gold`).toBe(false);
    }
  });

  test('the top bar is 64 px: the goldie mark, then the theme toggle, then the menu button below 1024 px', async ({ page }) => {
    await page.goto('/');
    const header = await page.locator('header').boundingBox();
    expect(Math.round(header!.height)).toBe(64);
    const mark = page.locator('header').getByRole('img', { name: /goldie/i }).or(page.locator('header').getByRole('link', { name: /goldie/i }));
    await expect(mark.first()).toBeVisible();
    await expect(page.locator('header')).not.toContainText(/phippy/i);
    const m = (await mark.first().boundingBox())!;
    const t = (await themeToggle(page).boundingBox())!;
    expect(m.x + m.width, 'mark before the toggle').toBeLessThanOrEqual(t.x);
    if (width < 1024) {
      const b = (await menuButton(page).boundingBox())!;
      expect(t.x + t.width, 'toggle before the menu button').toBeLessThanOrEqual(b.x);
    } else {
      await expect(menuButton(page)).toBeHidden();
    }
  });

  test('the nav collapses below 1024 px and the page has no skip-link-first violation', async ({ page }) => {
    await page.goto('/');
    await expectNavCollapsed(page, width);
    const first = await page.evaluate(
      () => document.querySelector<HTMLElement>('a[href], button, input, [tabindex]:not([tabindex="-1"])')?.textContent?.trim(),
    );
    expect(first).toBe('Skip to content');
  });

  test('the side nav lists Home, Decisions, the Agent assets group, then the Reader nav note, and no Operate group', async ({ page }) => {
    await page.goto('/');
    const nav = await withNav(page);
    await expect(nav).toHaveAttribute('aria-label', /.+/);
    const names = await nav.getByRole('link').allTextContents();
    expect(names.map((n) => n.trim())).toEqual(['Home', 'Decisions', 'Skills', 'Agents', 'Loops', 'Instructions']);
    const text = ((await nav.innerText()) ?? '').replace(/\s+/g, ' ').replace(/’/g, "'");
    const at = ['Home', 'Decisions', 'Agent assets', 'Skills', 'Agents', 'Loops', 'Instructions', 'Reader view: Activity and Sources are for operators and aren\'t shown.'].map(
      (s) => text.indexOf(s),
    );
    expect(at.every((i) => i >= 0), `all entries present in: ${text}`).toBe(true);
    expect([...at].sort((a, b) => a - b), 'entries in order').toEqual(at);
    await expect(nav.getByRole('link', { name: /activity|sources/i })).toHaveCount(0);
    await expect(nav).not.toContainText('Operate');
  });
});

test.describe('nav width', () => {
  for (const [width, min, max] of [
    [1280, 240, 260],
    [1440, 240, 260],
    [1279, 220, 220],
    [1024, 220, 220],
  ] as const) {
    test(`the nav is ${min}-${max} px at ${width} px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 });
      await page.goto('/');
      const box = (await page.getByRole('navigation').boundingBox())!;
      expect(box.width).toBeGreaterThanOrEqual(min);
      expect(box.width).toBeLessThanOrEqual(max);
    });
  }
});

test.describe('the shell changes at 1024 px (§7)', () => {
  test.use({ viewport: { width: 1024, height: 800 } });
  test('at 1024 px the nav is inline and axe-clean, with no menu button', async ({ page, baseURL }) => {
    const seen = await problems(page, baseURL ?? '');
    expect((await page.goto('/'))?.status()).toBe(200);
    await expectNavCollapsed(page, 1024);
    await evidence(page, 1024, 'light', '', 'shell');
    expect(seen.external).toEqual([]);
    expect(seen.failed).toEqual([]);
  });
});

test.describe('just below 1024 px', () => {
  test.use({ viewport: { width: 1023, height: 800 } });
  test('at 1023 px the nav is behind the menu button', async ({ page }) => {
    await page.goto('/');
    await expectNavCollapsed(page, 1023);
  });
});

test.describe('routes and the current page', () => {
  const routes: [string, string][] = [
    ['Home', '/'],
    ['Decisions', '/decisions'],
    ['Skills', '/assets/skills'],
    ['Agents', '/assets/agents'],
    ['Instructions', '/assets/instructions'],
  ];

  for (const [name, path] of routes) {
    test(`${name} links to ${path}, which renders the shell with the empty state and marks the link current`, async ({ page }) => {
      const res = await page.goto('/');
      expect(res?.status()).toBe(200);
      let nav = await withNav(page);
      const link = nav.getByRole('link', { name, exact: true });
      await expect(link).toHaveAttribute('href', path);
      await link.click();
      await expect(page).toHaveURL((u) => u.pathname === path);
      // The assets pages show the Agent assets frame (#584), not the empty state.
      if (path.startsWith('/assets/')) await expect(page.getByRole('heading', { level: 1 })).toHaveText('Agent assets');
      else if (path === '/') await expect(page.getByRole('main')).not.toContainText(emptyCopy);
      else if (path === '/decisions') await expect(page.getByRole('heading', { level: 1 })).toHaveText('Decisions');
      else await expect(page.getByRole('main').getByRole('status')).toHaveText(emptyCopy);
      nav = await withNav(page);
      const current = nav.getByRole('link', { name, exact: true });
      await expect(current).toHaveAttribute('aria-current', 'page');
      await expect(nav.locator('[aria-current="page"]')).toHaveCount(1);

      // A cue other than colour: some non-colour style differs from a link that is not current.
      const other = nav.getByRole('link', { name: name === 'Home' ? 'Decisions' : 'Home', exact: true });
      const cue = (l: typeof current) =>
        l.evaluate((el) => {
          const s = getComputedStyle(el);
          const b = getComputedStyle(el, '::before');
          return [s.fontWeight, s.textDecorationLine, s.borderLeftWidth, s.borderBottomWidth, s.boxShadow, b.content, s.fontStyle].join('|');
        });
      expect(await cue(current), 'non-colour cue of the current page').not.toBe(await cue(other));
    });
  }

  for (const path of ['/', '/decisions', '/assets/skills', '/assets/agents', '/assets/instructions', '/signin', '/ops/activity', '/ops/sources', '/items/skill/x', '/decisions?status=accepted&scope=org']) {
    test(`GET ${path} is 200 and renders the shell, not a 404`, async ({ page }) => {
      const res = await page.goto(path);
      expect(res?.status()).toBe(200);
      await expect(page.getByRole('banner')).toBeVisible();
      await expect(page.getByRole('main')).toBeVisible();
      await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
    });
  }

  test('every route has its own h1 and only Home asks the Home question', async ({ page }) => {
    const seen = new Map<string, string>();
    for (const path of ['/', '/decisions', '/assets/skills', '/assets/agents', '/assets/instructions', '/signin', '/ops/activity', '/ops/sources', '/items/skill/x', '/no/such/route']) {
      await page.goto(path);
      const h1 = page.getByRole('heading', { level: 1 });
      await expect(h1, path).toHaveCount(1);
      const text = ((await h1.textContent()) ?? '').trim();
      expect(text, `h1 of ${path}`).not.toBe('');
      if (path === '/') expect(text).toBe('What are you deciding today?');
      else expect(text, `h1 of ${path} is not the Home question`).not.toBe('What are you deciding today?');
      seen.set(path, text);
    }
    // The three Agent assets tabs share one h1 (#584).
    const nav = ['/decisions', '/assets/skills'].map((p) => seen.get(p));
    expect(new Set(nav).size, `h1s of the nav routes are distinct: ${nav}`).toBe(nav.length);
  });

  test('a route outside §2.2 shows the shell with a not-found message and a link Home', async ({ page, baseURL }) => {
    const seen = await problems(page, baseURL ?? '');
    const res = await page.goto('/no/such/route');
    expect(res?.status(), 'the SPA index is served with 200').toBe(200);
    await expect(page.getByRole('banner')).toBeVisible();
    const main = page.getByRole('main');
    await expect(main).toContainText(/not found/i);
    await expect(main).not.toContainText(emptyCopy);
    await expect(main.getByRole('link', { name: /home/i })).toHaveAttribute('href', '/');
    await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
    expect(seen.errors).toEqual([]);
    await main.getByRole('link', { name: /home/i }).click();
    await expect(page).toHaveURL((u) => u.pathname === '/');
  });

  test('a missing file with an extension is a real 404, never the shell', async ({ request }) => {
    const res = await request.get('/assets/missing-file.js');
    expect(res.status()).toBe(404);
    expect(res.headers()['content-type'] ?? '').not.toContain('text/html');
  });
});

test.describe('page hygiene', () => {
  test('there is no inline script and the pre-paint theme init is an external classic script in head', async ({ page, request }) => {
    const res = await request.get('/');
    const csp = res.headers()['content-security-policy'] ?? '';
    expect(csp).toContain("script-src 'self'");
    expect(csp).not.toContain('unsafe-inline');
    await page.goto('/');
    const scripts = await page.evaluate(() =>
      [...document.querySelectorAll('script')].map((s) => ({
        src: s.getAttribute('src'),
        inline: !s.src && (s.textContent ?? '').trim() !== '',
        module: s.type === 'module',
        deferred: s.defer || s.async,
        inHead: !!s.closest('head'),
      })),
    );
    expect(scripts.filter((s) => s.inline), 'inline scripts').toEqual([]);
    const init = scripts.filter((s) => s.src && !s.module && !s.deferred && s.inHead);
    expect(init.length, 'a blocking external script in head sets data-theme before first paint').toBeGreaterThanOrEqual(1);
    for (const s of init) {
      const js = await request.get(s.src!);
      expect(js.status()).toBe(200);
      expect(js.headers()['content-type']).toContain('text/javascript');
    }
  });

  test('the rendered shell has no inline event-handler attributes', async ({ page }) => {
    await page.goto('/');
    const inline = await page.evaluate(() =>
      [...document.querySelectorAll('*')].flatMap((el) => el.getAttributeNames().filter((a) => /^on/i.test(a)).map((a) => `${el.tagName}.${a}`)),
    );
    expect(inline).toEqual([]);
  });
});

// The selected Project follows the user (#729, handoff §2.2, §3.1): the query
// string carries `project`, so every link and every page keeps it. The API is
// stubbed per test; the throwaway ux token is a reader, so the nav specs stub
// GET /api/v1/session as an operator to see every link.
test.describe('the selected Project follows the user across pages', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  const shop = 'github.com/acme/shop';
  const billing = 'github.com/acme/billing';
  const origin = 'http://goldie.test';

  const json = (route: Route, status: number, body: unknown) =>
    route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

  const asOperator = (page: Page) =>
    page.route('**/api/v1/session', (route) =>
      route.request().method() === 'GET' ? json(route, 200, { name: 'ops', role: 'operator' }) : route.continue(),
    );

  const stubProjects = (page: Page) =>
    page.route(/\/api\/v1\/projects$/, (route) => json(route, 200, { projects: [shop, billing] }));

  const selector = (page: Page) => page.locator('header').getByRole('combobox', { name: /project/i });
  const chooseProject = async (page: Page, id: string) => {
    await selector(page).click();
    await page.getByRole('option').filter({ hasText: id }).click();
  };

  // navHrefs reads every link of the side nav and the brand link as [name, href].
  async function navHrefs(page: Page): Promise<[string, string][]> {
    const nav = await withNav(page);
    const read = (els: Element[], name: string) =>
      els.map((el) => [name || (el.textContent ?? '').trim(), el.getAttribute('href') ?? ''] as [string, string]);
    const links = [
      ...(await nav.getByRole('link').evaluateAll(read, '')),
      ...(await page.locator('header').getByRole('link', { name: /goldie/i }).evaluateAll(read, 'brand')),
    ];
    return links.map(([name, href]) => [name.replace(/\s*\d+ failing$/, ''), href]);
  }

  const parsed = (href: string) => new URL(href, origin);

  const paths: Record<string, string> = {
    Home: '/',
    Decisions: '/decisions',
    Skills: '/assets/skills',
    Agents: '/assets/agents',
    Loops: '/assets/loops',
    Instructions: '/assets/instructions',
    Activity: '/ops/activity',
    Sources: '/ops/sources',
    brand: '/',
  };

  test('with a Project selected every side-nav link and the brand link keep ?project=<id>', async ({ page }) => {
    await asOperator(page);
    await stubProjects(page);
    await page.goto(`/decisions?project=${encodeURIComponent(shop)}`);
    const links = await navHrefs(page);
    expect(links.map(([n]) => n).sort(), 'every nav entry and the brand link').toEqual(Object.keys(paths).sort());
    for (const [name, href] of links) {
      const u = parsed(href);
      expect(u.pathname, `${name} still goes to its page`).toBe(paths[name]);
      expect(u.searchParams.get('project'), `${name} keeps the Project`).toBe(shop);
    }
  });

  test('a nav link click lands on its page with the Project in the URL and in the selector', async ({ page }) => {
    await asOperator(page);
    await stubProjects(page);
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    const nav = await withNav(page);
    await nav.getByRole('link', { name: 'Decisions', exact: true }).click();
    await expect(page).toHaveURL((u) => u.pathname === '/decisions' && u.searchParams.get('project') === shop);
    await expect(selector(page)).toContainText(shop);
    await page.locator('header').getByRole('link', { name: /goldie/i }).click();
    await expect(page).toHaveURL((u) => u.pathname === '/' && u.searchParams.get('project') === shop);
    await expect(selector(page)).toContainText(shop);
  });

  test('with no Project selected the links carry no project', async ({ page }) => {
    await asOperator(page);
    await stubProjects(page);
    await page.goto('/decisions');
    const links = await navHrefs(page);
    expect(links.length).toBe(Object.keys(paths).length);
    for (const [name, href] of links) {
      expect(parsed(href).searchParams.has('project'), `${name} (${href}) carries no project`).toBe(false);
      expect(href, `${name} has no empty project`).not.toContain('project');
    }
  });

  for (const path of ['/decisions', '/assets/skills', '/assets/skills?tab=x']) {
    test(`choosing a Project at ${path} keeps the user on the page and sets its project query`, async ({ page }) => {
      await stubProjects(page);
      await page.goto(path);
      const before = parsed(page.url());
      await chooseProject(page, billing);
      await expect(page).toHaveURL((u) => u.pathname === before.pathname && u.searchParams.get('project') === billing);
      for (const [k, v] of before.searchParams) expect(parsed(page.url()).searchParams.get(k), `${k} is kept`).toBe(v);
      await expect(selector(page)).toContainText(billing);
      await chooseProject(page, shop);
      await expect(page).toHaveURL((u) => u.pathname === before.pathname && u.searchParams.get('project') === shop);
      expect(parsed(page.url()).searchParams.getAll('project'), 'one project value').toEqual([shop]);
    });
  }

  test('choosing a Project on an Item page stays on that Item', async ({ page }) => {
    await stubProjects(page);
    await page.route(/\/api\/v1\/articles\//, (route) =>
      json(route, 200, { id: 'platform/deploy', name: 'Deploying to Kubernetes', keywords: [], content: 'Body.' }),
    );
    await page.goto('/items/article/platform%2Fdeploy');
    await chooseProject(page, shop);
    await expect(page).toHaveURL((u) => u.pathname === '/items/article/platform%2Fdeploy' && u.searchParams.get('project') === shop);
  });

  for (const path of ['/ops/activity', '/assets/skills', '/assets/agents', '/decisions', '/']) {
    test(`opening ${path}?project=x shows x in the selector`, async ({ page }) => {
      await asOperator(page);
      await stubProjects(page);
      await page.goto(`${path}?project=${encodeURIComponent(shop)}`);
      await expect(selector(page)).toContainText(shop);
    });
  }

  for (const hostile of ['../x', 'github.com/acme/..%2Fx', '..%2F..%2Fetc', 'a/b?c=d#e', 'x&kind=skill']) {
    test(`the Project id ${hostile} is encoded in every link and never becomes a path`, async ({ page }) => {
      await asOperator(page);
      await stubProjects(page);
      await page.goto(`/decisions?project=${encodeURIComponent(hostile)}`);
      const links = await navHrefs(page);
      expect(links.length).toBe(Object.keys(paths).length);
      for (const [name, href] of links) {
        const u = parsed(href);
        expect(u.pathname, `${name} keeps its own path`).toBe(paths[name]);
        expect(u.hash, `${name} has no fragment`).toBe('');
        expect(u.searchParams.get('project'), `${name} round-trips the id`).toBe(hostile);
        expect([...u.searchParams.keys()], `${name} gains no other parameter`).toEqual(['project']);
      }
      // Following a link ends on the page the link names, not on the id's path.
      const nav = await withNav(page);
      await nav.getByRole('link', { name: 'Skills', exact: true }).click();
      await expect(page).toHaveURL((u) => u.pathname === '/assets/skills' && u.searchParams.get('project') === hostile);
      await expect(page.getByRole('banner')).toBeVisible();
    });
  }

  // The Item's back link (criterion 4).
  const hit = { id: 'platform/deploy', name: 'Deploying to Kubernetes', kind: 'article', scope: 'global', keywords: [], score: 4, matched: ['deploy'], enforced: false, overrides: [] };
  const stubItemApis = async (page: Page) => {
    await stubProjects(page);
    await page.route(/\/api\/v1\/search(\?|$)/, (route) => json(route, 200, { query: 'deploy', results: [hit] }));
    await page.route(/\/api\/v1\/articles\//, (route) =>
      json(route, 200, { id: 'platform/deploy', name: 'Deploying to Kubernetes', keywords: [], content: 'Body.' }),
    );
  };
  const back = (page: Page) => page.getByRole('main').getByRole('link', { name: /Back to search/ });
  const openHit = async (page: Page) => {
    await page.goto('/?q=deploy&kind=article');
    await page.getByRole('main').getByRole('listitem').getByRole('link').first().click();
    await expect(page).toHaveURL((u) => u.pathname === '/items/article/platform%2Fdeploy');
  };

  test("an Item's back link returns to the search it came from with q and kind", async ({ page }) => {
    await stubItemApis(page);
    await openHit(page);
    await expect(back(page)).toHaveCount(1);
    const u = parsed((await back(page).getAttribute('href'))!);
    expect(u.pathname).toBe('/');
    expect(u.searchParams.get('q')).toBe('deploy');
    expect(u.searchParams.get('kind')).toBe('article');
    expect(u.searchParams.has('project')).toBe(false);
    await back(page).click();
    await expect(page).toHaveURL((l) => l.pathname === '/' && l.searchParams.get('q') === 'deploy' && l.searchParams.get('kind') === 'article');
  });

  test("an Item's back link keeps the Project chosen on the way", async ({ page }) => {
    await stubItemApis(page);
    await openHit(page);
    await chooseProject(page, shop);
    await expect(page).toHaveURL((u) => u.pathname === '/items/article/platform%2Fdeploy' && u.searchParams.get('project') === shop);
    const u = parsed((await back(page).getAttribute('href'))!);
    expect(u.pathname).toBe('/');
    expect(u.searchParams.get('q')).toBe('deploy');
    expect(u.searchParams.get('kind')).toBe('article');
    expect(u.searchParams.get('project')).toBe(shop);
  });

  test("an Item's back link without a previous search goes to Home with the Project", async ({ page }) => {
    await stubItemApis(page);
    await page.goto(`/items/article/platform%2Fdeploy?project=${encodeURIComponent(shop)}`);
    await expect(back(page)).toHaveCount(1);
    const u = parsed((await back(page).getAttribute('href'))!);
    expect(u.pathname).toBe('/');
    expect(u.searchParams.get('project')).toBe(shop);
    expect(u.searchParams.has('q')).toBe(false);
    expect(u.searchParams.has('kind')).toBe(false);
    await back(page).click();
    await expect(page).toHaveURL((l) => l.pathname === '/' && l.searchParams.get('project') === shop);
  });

  test("an Item's back link without a search or a Project goes to plain Home", async ({ page }) => {
    await stubItemApis(page);
    await page.goto('/items/article/platform%2Fdeploy');
    await expect(back(page)).toHaveCount(1);
    const u = parsed((await back(page).getAttribute('href'))!);
    expect(u.pathname).toBe('/');
    expect([...u.searchParams.keys()]).toEqual([]);
  });

  test("an Item's back link encodes a hostile Project id and never becomes a path", async ({ page }) => {
    await stubItemApis(page);
    await page.goto(`/items/article/platform%2Fdeploy?project=${encodeURIComponent('../x')}`);
    await expect(back(page)).toHaveCount(1);
    const u = parsed((await back(page).getAttribute('href'))!);
    expect(u.pathname).toBe('/');
    expect(u.searchParams.get('project')).toBe('../x');
  });
});
