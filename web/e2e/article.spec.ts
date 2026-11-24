// The article view (#498, handoff §3.4, §5.2, §8, §9, ADR 0016). `/items/article/:name`
// shows one article. The API is stubbed per test (page.route) so every state is
// deterministic; the article's Markdown is untrusted and is hostile here.
//
// Contract these specs pin down for the implementation:
// - the title is the one h1 in main; the article's own headings start at h2;
// - the id is shown in a mono face (computed font-family names "mono");
// - the enforced chip is text or an img named /enforced/i, shown only for
//   `enforced: true`; the overrides line reads "Overrides article:<name|id> from
//   <scope>[, <scope>]", a single "from" before the Scopes (docs/design
//   ui-handoff §8), shown only for a non-empty `overrides`;
// - the lifecycle banner is a role=status in main, above the h1, with a dashed
//   border for a draft; the replaced_by link is inside it;
// - an error is a role=alert in main holding the envelope's message and hint
//   and a link that goes to `/`, named /Back to home/ when the article was opened
//   without a search (#754) and /Back to search/ when it came from one;
// - a nested id is requested as GET /api/v1/articles/<id with %2F>, one segment;
// - wide `pre` and `table` scroll inside their own box (an ancestor inside main
//   with overflow-x auto or scroll), never the page.
//
// Not covered here, and why: the Scope chip of the first criterion. The Article
// the API returns has no `scope` field (schema Article), so a chip for it has
// no observable source until the issue says where it comes from.
import { expect, test, type Page, type Route } from '@playwright/test';
import { eachViewport, evidence, problems } from './ux';

const apiArticle = /\/api\/v1\/articles\//;

// The shell's own "← Back to search" link (#729) is on a loaded article too; the
// link specs below are about links made from the article's Markdown, and match
// only that exact link, so any other extra anchor still fails them.
const shellBack = /^\s*←\s*Back to (search|home)\s*$/;

type Article = {
  id?: string;
  name?: string;
  keywords?: string[];
  content?: string;
  status?: string;
  replaced_by?: string;
  enforced?: boolean;
  overrides?: string[];
};

// An article as the API sends it. `enforced` and `overrides` are left out unless
// the test sets them (an older server).
const article = (a: Article = {}) => ({
  id: 'platform/deploy',
  name: 'Deploying to Kubernetes',
  keywords: ['kubernetes', 'deploy'],
  content: '## Steps\n\nUse the **platform chart**.',
  ...a,
});

const json = (route: Route, status: number, body: unknown) =>
  route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

const envelope = (code: string, message: string, hint?: string) => ({
  error: { code, message, details: hint ? { hint } : {} },
});

// stubArticle answers every article lookup and returns the request paths seen,
// as the browser sent them (percent-encoding intact).
async function stubArticle(page: Page, a: Article, status = 200) {
  const seen: string[] = [];
  await page.route(apiArticle, (route) => {
    const u = new URL(route.request().url());
    seen.push(u.pathname + u.search);
    return json(route, status, a);
  });
  return seen;
}

const open = (page: Page, id = 'platform%2Fdeploy') => page.goto(`/items/article/${id}`);

const main = (page: Page) => page.getByRole('main');
const h1 = (page: Page) => main(page).getByRole('heading', { level: 1 });
const alert = (page: Page) => main(page).getByRole('alert');
const banner = (page: Page) => main(page).getByRole('status');
const chip = (page: Page) => main(page).getByText(/enforced/i).or(main(page).getByRole('img', { name: /enforced/i }));
const norm = (s: string) => s.replace(/\s+/g, ' ').trim();
const mainText = async (page: Page) => norm(await main(page).innerText());

const hostileMd = [
  '# Top heading',
  '',
  'Before <script>window.__pwned = "script"; alert(1)</script> after.',
  '',
  '<img src=x onerror="window.__pwned = \'img\'; alert(2)">',
  '',
  '<iframe src="https://evil.example/frame"></iframe>',
  '',
  '<style>body { display: none !important }</style>',
  '',
  '<a href="javascript:window.__pwned = \'raw\'">raw anchor</a>',
  '',
  '[md js link](javascript:window.__pwned = "md")',
  '',
  '[mixed case link](JaVaScRiPt:alert(3))',
  '',
  '[entity link](&#106;avascript:alert(4))',
  '',
  '[data link](data:text/html;base64,PHNjcmlwdD5hbGVydCg1KTwvc2NyaXB0Pg==)',
  '',
  '[secure link](https://example.org/safe)',
  '',
  '[plain link](http://example.org/plain)',
  '',
  'Still **bold** after all that.',
].join('\n');

// gold lists the gold colours in an element's background and borders.
const gold = (el: Element) => {
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
  return [s.backgroundColor, s.borderTopColor, s.borderLeftColor]
    .map(rgb)
    .filter(({ a }) => a !== 0)
    .map(({ r, g, b }) => {
      const max = Math.max(r, g, b);
      const min = Math.min(r, g, b);
      const sat = max === 0 ? 0 : (max - min) / max;
      let hue = 0;
      if (max !== min) {
        if (max === r) hue = (60 * ((g - b) / (max - min)) + 360) % 360;
        else if (max === g) hue = 60 * ((b - r) / (max - min)) + 120;
        else hue = 60 * ((r - g) / (max - min)) + 240;
      }
      return sat > 0.25 && hue >= 30 && hue <= 60 ? `rgb(${r},${g},${b})` : '';
    })
    .filter(Boolean);
};

const wide = {
  content: [
    '## Wide things',
    '',
    '```sh',
    `deploy --server-side --force-conflicts ${'--very-long-flag=value '.repeat(20)}`,
    '```',
    '',
    `| ${Array.from({ length: 14 }, (_, i) => `column number ${i}`).join(' | ')} |`,
    `| ${Array.from({ length: 14 }, () => '---').join(' | ')} |`,
    `| ${Array.from({ length: 14 }, (_, i) => `a long cell value ${i}`).join(' | ')} |`,
  ].join('\n'),
};

eachViewport((width, theme) => {
  test('an article is captured and axe-clean', async ({ page }) => {
    await stubArticle(page, article({ enforced: true, overrides: ['global'] }));
    await open(page);
    await expect(h1(page)).toHaveText('Deploying to Kubernetes');
    await evidence(page, width, theme, '-article', 'article');
  });

  test('a deprecated article is captured and axe-clean', async ({ page }) => {
    await stubArticle(page, article({ status: 'deprecated', replaced_by: 'article:platform/deploy-v2', enforced: false, overrides: [] }));
    await open(page);
    await expect(banner(page).filter({ hasText: /Deprecated/ })).toBeVisible();
    await evidence(page, width, theme, '-deprecated', 'article');
  });

  test('a draft article is captured and axe-clean', async ({ page }) => {
    await stubArticle(page, article({ status: 'draft' }));
    await open(page);
    await expect(banner(page).filter({ hasText: /Draft preview/ })).toBeVisible();
    await evidence(page, width, theme, '-draft', 'article');
  });

  test('the hostile article is captured and axe-clean', async ({ page }) => {
    await stubArticle(page, article({ content: hostileMd }));
    await open(page);
    await expect(h1(page)).toHaveText('Deploying to Kubernetes');
    await evidence(page, width, theme, '-hostile', 'article');
  });

  test('wide code and tables are captured and axe-clean', async ({ page }) => {
    await stubArticle(page, article(wide));
    await open(page);
    await expect(main(page).locator('pre')).toHaveCount(1);
    await evidence(page, width, theme, '-wide', 'article');
  });

  test('a missing article is captured and axe-clean', async ({ page }) => {
    await page.route(apiArticle, (route) =>
      json(route, 404, envelope('not_found', 'No article has this id.', 'Search for it by its keywords.')),
    );
    await open(page, 'nope');
    await expect(alert(page)).toBeVisible();
    await evidence(page, width, theme, '-missing', 'article');
  });
});

test.describe('the article page', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('shows the title as the one h1, the id in mono, and the rendered Markdown below the h1', async ({ page }) => {
    await stubArticle(page, article({ content: '# Overview\n\nUse the **platform chart**.\n\n## Details\n\n- one\n- two' }));
    await open(page);
    await expect(main(page).getByRole('heading', { level: 1 })).toHaveCount(1);
    await expect(h1(page)).toHaveText('Deploying to Kubernetes');

    const id = main(page).getByText('platform/deploy', { exact: true });
    await expect(id).toHaveCount(1);
    expect(await id.evaluate((el) => getComputedStyle(el).fontFamily)).toMatch(/mono/i);

    await expect(main(page).getByRole('heading', { level: 2, name: 'Overview' })).toBeVisible();
    await expect(main(page).getByRole('heading', { level: 3, name: 'Details' })).toBeVisible();
    await expect(main(page).locator('strong', { hasText: 'platform chart' })).toBeVisible();
    await expect(main(page).getByRole('listitem')).toHaveCount(2);
  });

  test('keeps the shell around the article and sets the page title', async ({ page }) => {
    await stubArticle(page, article());
    await open(page);
    await expect(h1(page)).toHaveText('Deploying to Kubernetes');
    await expect(page.getByRole('banner')).toBeVisible();
    await expect(page.getByRole('navigation')).toBeVisible();
    await expect(page).toHaveTitle(/goldie/);
  });

  test('requests a nested id as one %2F segment, and a flat id as is', async ({ page }) => {
    const seen = await stubArticle(page, article());
    await open(page, 'platform%2Fdeploy');
    await expect(h1(page)).toHaveText('Deploying to Kubernetes');
    expect(seen[0]).toBe('/api/v1/articles/platform%2Fdeploy');
    expect(seen).toHaveLength(1);

    await page.unroute(apiArticle);
    const flat = await stubArticle(page, article({ id: 'retry', name: 'Retry' }));
    await open(page, 'retry');
    await expect(h1(page)).toHaveText('Retry');
    expect(flat[0]).toBe('/api/v1/articles/retry');
  });

  test('opened for a Project it requests the article with that Project, encoded; without one, none (#826)', async ({ page }) => {
    const seen = await stubArticle(page, article());
    await page.goto('/items/article/platform%2Fdeploy?project=github.com%2Fharbor%2Fcheckout');
    await expect(h1(page)).toHaveText('Deploying to Kubernetes');
    expect(seen).toEqual(['/api/v1/articles/platform%2Fdeploy?project=github.com%2Fharbor%2Fcheckout']);

    await page.unroute(apiArticle);
    const plain = await stubArticle(page, article());
    await open(page);
    await expect(h1(page)).toHaveText('Deploying to Kubernetes');
    expect(plain).toEqual(['/api/v1/articles/platform%2Fdeploy']);
  });

  test("an unregistered Project shows the global article and the API's hint, styled like the index hint (#826)", async ({ page }) => {
    const unknown = 'github.com/nobody/nothing';
    const hint = 'No Project is registered under this id, so this is the global article.';
    const apiList = /\/api\/v1\/articles(\?.*)?$/;
    await page.route(/\/api\/v1\/projects$/, (route) => json(route, 200, { projects: [] }));
    await page.route(apiList, (route) => json(route, 200, { articles: [], hint }));
    await page.goto(`/articles?project=${encodeURIComponent(unknown)}`);
    const note = main(page).getByText(hint);
    await expect(note).toBeVisible();
    const style = (l: ReturnType<Page['locator']>) =>
      l.evaluate((e) => {
        const c = getComputedStyle(e);
        return { color: c.color, size: c.fontSize };
      });
    const want = await style(note);

    await page.unroute(apiList);
    await page.route(apiArticle, (route) => json(route, 200, { ...article(), hint }));
    await page.goto(`/items/article/platform%2Fdeploy?project=${encodeURIComponent(unknown)}`);
    await expect(h1(page)).toHaveText('Deploying to Kubernetes');
    await expect(alert(page)).toHaveCount(0);
    const shown = main(page).getByText(hint);
    await expect(shown).toBeVisible();
    expect(await style(shown)).toEqual(want);
  });

  test('shows the enforced chip only for enforced: true', async ({ page }) => {
    await stubArticle(page, article({ enforced: true, overrides: [] }));
    await open(page);
    await expect(h1(page)).toBeVisible();
    await expect(chip(page).first()).toBeVisible();

    await page.unroute(apiArticle);
    await stubArticle(page, article({ enforced: false, overrides: [] }));
    await open(page);
    await expect(h1(page)).toBeVisible();
    await expect(chip(page)).toHaveCount(0);
  });

  test('says "Overrides article:<name> from <scope>, <scope>" for shadowed Scopes, and only then', async ({ page }) => {
    await stubArticle(page, article({ enforced: false, overrides: ['org', 'global'] }));
    await open(page);
    await expect(h1(page)).toBeVisible();
    const text = await mainText(page);
    expect(text).toMatch(/Overrides\s+article:(Deploying to Kubernetes|platform\/deploy)\s+from\s+org/);
    expect(text).toMatch(/from\s+org,\s*global/);

    await page.unroute(apiArticle);
    await stubArticle(page, article({ enforced: false, overrides: [] }));
    await open(page);
    await expect(h1(page)).toBeVisible();
    expect(await mainText(page)).not.toMatch(/Overrides/);
  });

  test('without enforced and overrides there is no chip, no overrides line and no error (#529)', async ({ page, baseURL }) => {
    const seen = await problems(page, baseURL ?? '');
    await stubArticle(page, article());
    await open(page);
    await expect(h1(page)).toHaveText('Deploying to Kubernetes');
    await expect(chip(page)).toHaveCount(0);
    expect(await mainText(page)).not.toMatch(/Overrides/);
    await expect(alert(page)).toHaveCount(0);
    await expect(banner(page)).toHaveCount(0);
    expect(seen.errors).toEqual([]);
  });

  test('with only one of the two present it shows that one and no error (#529)', async ({ page }) => {
    await stubArticle(page, article({ enforced: true }));
    await open(page);
    await expect(chip(page).first()).toBeVisible();
    expect(await mainText(page)).not.toMatch(/Overrides/);
    await expect(alert(page)).toHaveCount(0);

    await page.unroute(apiArticle);
    await stubArticle(page, article({ overrides: ['global'] }));
    await open(page);
    await expect(h1(page)).toBeVisible();
    expect(await mainText(page)).toMatch(/Overrides\s+article:\S.*from\s+global/);
    await expect(chip(page)).toHaveCount(0);
    await expect(alert(page)).toHaveCount(0);
  });
});

test.describe('hostile Markdown', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('is never run: no script, no handler, no frame, no hidden page, no CSP violation', async ({ page, baseURL }) => {
    const seen = await problems(page, baseURL ?? '');
    let dialog = false;
    page.on('dialog', async (d) => {
      dialog = true;
      await d.dismiss();
    });
    await stubArticle(page, article({ content: hostileMd }));
    await open(page);
    await expect(h1(page)).toHaveText('Deploying to Kubernetes');
    await expect(main(page).locator('strong', { hasText: 'bold' })).toBeVisible();
    await page.waitForLoadState('networkidle');

    expect(await page.evaluate(() => (window as unknown as { __pwned?: string }).__pwned), 'something ran').toBeUndefined();
    expect(dialog, 'an alert dialog opened').toBe(false);
    await expect(main(page).locator('script, iframe, style, object, embed, img, svg, form')).toHaveCount(0);
    await expect(page.locator('iframe')).toHaveCount(0);
    await expect(main(page).locator('[onerror], [onload], [onclick], [onmouseover]')).toHaveCount(0);
    await expect(main(page), 'the style element hid the page').toBeVisible();
    expect(await main(page).evaluate((el) => getComputedStyle(el).display)).not.toBe('none');
    expect(seen.errors, 'page errors and CSP violations').toEqual([]);
    expect(seen.external, 'a request left the server').toEqual([]);
    expect(seen.failed).toEqual([]);
  });

  test('leaves only the http: and https: links, with rel="noopener noreferrer"', async ({ page }) => {
    await stubArticle(page, article({ content: hostileMd }));
    await open(page);
    await expect(h1(page)).toBeVisible();
    await expect(main(page).getByRole('link', { name: 'secure link' })).toBeVisible();

    const hrefs = await main(page).locator('a').filter({ hasNotText: shellBack }).evaluateAll((as) => as.map((a) => a.getAttribute('href') ?? ''));
    expect(hrefs.sort()).toEqual(['http://example.org/plain', 'https://example.org/safe']);

    for (const name of ['secure link', 'plain link']) {
      const rel = (await main(page).getByRole('link', { name }).getAttribute('rel')) ?? '';
      expect(rel.split(/\s+/)).toEqual(expect.arrayContaining(['noopener', 'noreferrer']));
    }
    const unsafe = main(page).getByRole('link', { name: /md js link|mixed case link|entity link|data link|raw anchor/ });
    await expect(unsafe).toHaveCount(0);
  });

  test('keeps the text of an unsafe link and shows nothing of it as a link', async ({ page }) => {
    await stubArticle(page, article({ content: '[click here](javascript:alert(1)) then [read](data:text/html,x)' }));
    await open(page);
    await expect(h1(page)).toBeVisible();
    const text = await mainText(page);
    expect(text).toContain('click here');
    expect(text).toContain('read');
    await expect(main(page).locator('a').filter({ hasNotText: shellBack })).toHaveCount(0);
  });

  test('an h1 in the source never makes a second page h1', async ({ page }) => {
    await stubArticle(page, article({ content: '# Source h1\n\n<h1>raw h1</h1>' }));
    await open(page);
    await expect(main(page).getByRole('heading', { level: 1 })).toHaveCount(1);
    await expect(h1(page)).toHaveText('Deploying to Kubernetes');
    await expect(main(page).getByRole('heading', { level: 2, name: 'Source h1' })).toBeVisible();
  });

  test('a hostile title and keyword are text too', async ({ page }) => {
    let dialog = false;
    page.on('dialog', async (d) => {
      dialog = true;
      await d.dismiss();
    });
    await stubArticle(
      page,
      article({ id: 'x/<img src=x onerror=alert(1)>', name: '<img src=x onerror=alert(2)> **t**', keywords: ['<script>alert(3)</script>'], content: '## Steps\n\nPlain text.' }),
    );
    await open(page, 'x%2F%3Cimg%20src%3Dx%20onerror%3Dalert(1)%3E');
    await expect(h1(page)).toContainText('<img src=x onerror=alert(2)> **t**');
    await expect(main(page).locator('img, script, strong')).toHaveCount(0);
    expect(dialog).toBe(false);
  });
});

test.describe('the lifecycle banners', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('a draft shows the §8 copy above the title, dashed', async ({ page }) => {
    await stubArticle(page, article({ status: 'draft' }));
    await open(page);
    const note = banner(page).filter({ hasText: /Draft preview/ });
    await expect(note).toHaveCount(1);
    expect(norm(await note.innerText())).toMatch(
      /^Draft preview\.\s+Agents and API consumers can[’']t see this yet\.\s+Merge with\s+status: published\s+to release it\.$/,
    );
    expect(await note.evaluate((el) => getComputedStyle(el).borderTopStyle)).toBe('dashed');
    const [n, t] = [await note.boundingBox(), await h1(page).boundingBox()];
    expect(n!.y + n!.height, 'the banner sits above the title').toBeLessThanOrEqual(t!.y);
    await expect(banner(page).filter({ hasText: /Deprecated/ })).toHaveCount(0);
  });

  test('a deprecated article shows the §8 copy with a link to the replacement', async ({ page }) => {
    await stubArticle(page, article({ status: 'deprecated', replaced_by: 'article:platform/deploy-v2' }));
    await open(page);
    const note = banner(page).filter({ hasText: /Deprecated/ });
    await expect(note).toHaveCount(1);
    expect(norm(await note.innerText())).toBe(
      'Deprecated. Still served to agents with a warning. Use article:platform/deploy-v2 instead.',
    );
    const link = note.getByRole('link');
    await expect(link).toHaveCount(1);
    expect(new URL((await link.getAttribute('href'))!, 'file:///').pathname, 'one %2F segment').toBe('/items/article/platform%2Fdeploy-v2');
    const [n, t] = [await note.boundingBox(), await h1(page).boundingBox()];
    expect(n!.y + n!.height, 'the banner sits above the title').toBeLessThanOrEqual(t!.y);
    await expect(banner(page).filter({ hasText: /Draft preview/ })).toHaveCount(0);
  });

  test('a replaced_by of another Kind links to that Kind', async ({ page }) => {
    await stubArticle(page, article({ status: 'deprecated', replaced_by: 'skill:retry' }));
    await open(page);
    const link = banner(page).filter({ hasText: /Deprecated/ }).getByRole('link');
    await expect(link).toHaveCount(1);
    expect(new URL((await link.getAttribute('href'))!, 'file:///').pathname).toBe('/items/skill/retry');
  });

  test('following the replacement link opens that article inside the shell', async ({ page }) => {
    await page.route(apiArticle, (route) => {
      const id = decodeURIComponent(new URL(route.request().url()).pathname.split('/').pop()!);
      return json(
        route,
        200,
        id === 'platform/deploy-v2'
          ? article({ id, name: 'Deploying, v2' })
          : article({ status: 'deprecated', replaced_by: 'article:platform/deploy-v2' }),
      );
    });
    await open(page);
    await page.evaluate(() => ((window as unknown as { marker: number }).marker = 3));
    await banner(page).getByRole('link').click();
    await expect(h1(page)).toHaveText('Deploying, v2');
    expect(await page.evaluate(() => (window as unknown as { marker?: number }).marker), 'a page load lost the marker').toBe(3);
  });

  for (const bad of ['', 'platform/deploy-v2', 'article:', ':platform', 'a b:c d', 'article:<img src=x onerror=alert(1)>', '<img src=x onerror=alert(2)>']) {
    test(`a replaced_by of ${JSON.stringify(bad)} is not a link and runs nothing`, async ({ page }) => {
      let dialog = false;
      page.on('dialog', async (d) => {
        dialog = true;
        await d.dismiss();
      });
      await stubArticle(page, article({ status: 'deprecated', replaced_by: bad }));
      await open(page);
      const note = banner(page).filter({ hasText: /Deprecated/ });
      await expect(note).toHaveCount(1);
      await expect(note.locator('img, script')).toHaveCount(0);
      await expect(note.getByRole('link')).toHaveCount(0);
      expect(dialog).toBe(false);
    });
  }

  test('a replaced_by with a script scheme is never a javascript: link', async ({ page }) => {
    await stubArticle(page, article({ status: 'deprecated', replaced_by: 'javascript:alert(1)' }));
    await open(page);
    const note = banner(page).filter({ hasText: /Deprecated/ });
    await expect(note).toHaveCount(1);
    for (const href of await note.locator('a').evaluateAll((as) => as.map((a) => a.getAttribute('href') ?? ''))) {
      expect(href, 'a link, if any, stays inside the app').toMatch(/^\/items\//);
    }
  });

  test('a deprecated article without replaced_by has the banner and no replacement link', async ({ page }) => {
    await stubArticle(page, article({ status: 'deprecated' }));
    await open(page);
    const note = banner(page).filter({ hasText: /Deprecated\./ });
    await expect(note).toHaveCount(1);
    await expect(note.getByRole('link')).toHaveCount(0);
    expect(norm(await note.innerText())).not.toMatch(/undefined|null|Use\s+instead/);
  });

  test('a published article has no banner', async ({ page }) => {
    await stubArticle(page, article());
    await open(page);
    await expect(h1(page)).toBeVisible();
    await expect(banner(page)).toHaveCount(0);
  });
});

test.describe('a missing article', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  const notFound = () => envelope('not_found', 'No article has the id platform/gone.', 'Search for it by its keywords on the Home page.');

  test('shows the API message and hint and a link back to home, never in gold', async ({ page }) => {
    await page.route(apiArticle, (route) => json(route, 404, notFound()));
    await open(page, 'platform%2Fgone');
    await expect(alert(page)).toHaveCount(1);
    await expect(alert(page)).toContainText('No article has the id platform/gone.');
    await expect(alert(page)).toContainText('Search for it by its keywords on the Home page.');
    const back = main(page).getByRole('link', { name: /Back to home/ });
    await expect(back).toHaveCount(1);
    await expect(back).toHaveText(/^\s*←\s*Back to home\s*$/);
    await expect(main(page).getByRole('link', { name: /Back to search/ })).toHaveCount(0);
    expect(new URL((await back.getAttribute('href'))!, 'file:///').pathname).toBe('/');

    expect(await alert(page).evaluate(gold), 'the error panel is gold').toEqual([]);
    expect(await back.evaluate(gold)).toEqual([]);
  });

  test('a message without a closing period gets one before the hint (#759)', async ({ page }) => {
    await page.route(apiArticle, (route) => json(route, 404, envelope('not_found', 'No article has the id platform/gone', 'Search for it by its keywords.')));
    await open(page, 'platform%2Fgone');
    expect(norm(await alert(page).innerText())).toBe('No article has the id platform/gone. Search for it by its keywords.');
  });

  for (const end of ['.', '?', '!']) {
    test(`a message already ending in "${end}" gets nothing extra before the hint (#759)`, async ({ page }) => {
      await page.route(apiArticle, (route) => json(route, 404, envelope('not_found', `Is this the id${end}`, 'Search for it by its keywords.')));
      await open(page, 'platform%2Fgone');
      expect(norm(await alert(page).innerText())).toBe(`Is this the id${end} Search for it by its keywords.`);
    });
  }

  test('a message without a period and without a hint is rendered unchanged (#759)', async ({ page }) => {
    await page.route(apiArticle, (route) => json(route, 404, envelope('not_found', 'No article has the id platform/gone')));
    await open(page, 'platform%2Fgone');
    expect(norm(await alert(page).innerText())).toBe('No article has the id platform/gone');
  });

  test('a message ending in a period and without a hint is rendered unchanged (#759)', async ({ page }) => {
    await page.route(apiArticle, (route) => json(route, 404, envelope('not_found', 'No article has the id platform/gone.')));
    await open(page, 'platform%2Fgone');
    expect(norm(await alert(page).innerText())).toBe('No article has the id platform/gone.');
  });

  test('the back link returns home inside the shell, without a page load', async ({ page }) => {
    await page.route(apiArticle, (route) => json(route, 404, notFound()));
    await open(page, 'platform%2Fgone');
    await page.evaluate(() => ((window as unknown as { marker: number }).marker = 5));
    await main(page).getByRole('link', { name: /Back to home/ }).click();
    await expect(page).toHaveURL((u) => u.pathname === '/');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('What are you deciding today?');
    expect(await page.evaluate(() => (window as unknown as { marker?: number }).marker)).toBe(5);
  });

  test('shows no article, no banner and no chip', async ({ page }) => {
    await page.route(apiArticle, (route) => json(route, 404, notFound()));
    await open(page, 'platform%2Fgone');
    await expect(alert(page)).toBeVisible();
    await expect(banner(page)).toHaveCount(0);
    await expect(main(page).getByText(/Overrides/)).toHaveCount(0);
    await expect(main(page).getByText(/enforced/i)).toHaveCount(0);
  });

  test('shows a hostile message and hint as text', async ({ page }) => {
    let dialog = false;
    page.on('dialog', async (d) => {
      dialog = true;
      await d.dismiss();
    });
    await page.route(apiArticle, (route) =>
      json(route, 404, envelope('not_found', '<img src=x onerror=alert(1)> **no**', '<script>alert(2)</script> [x](javascript:alert(3))')),
    );
    await open(page, 'x');
    await expect(alert(page)).toContainText('<img src=x onerror=alert(1)> **no**');
    await expect(alert(page)).toContainText('<script>alert(2)</script> [x](javascript:alert(3))');
    await expect(alert(page).locator('img, script, strong, a')).toHaveCount(0);
    expect(dialog).toBe(false);
  });

  test('a server error shows its message and hint', async ({ page }) => {
    await page.route(apiArticle, (route) => json(route, 500, envelope('internal', 'Something went wrong.', 'Try again in a moment.')));
    await open(page);
    await expect(alert(page)).toContainText('Something went wrong.');
    await expect(alert(page)).toContainText('Try again in a moment.');
    await expect(main(page).getByRole('link', { name: /Back to home/ })).toHaveCount(1);
  });

  test('a reply that is not an envelope is not echoed', async ({ page }) => {
    await page.route(apiArticle, (route) =>
      route.fulfill({ status: 502, contentType: 'text/html', body: '<pre>panic at /srv/x.go 10.0.0.7</pre>' }),
    );
    await open(page);
    await expect(alert(page)).toBeVisible();
    const text = await mainText(page);
    expect(text).not.toMatch(/panic|\/srv|10\.0\.0\.7/);
    await expect(main(page).getByRole('link', { name: /Back to home/ })).toHaveCount(1);
  });
});

// Item detail layout (#732, handoff §3.4): a Kind badge beside the name, and an aside
// with "Use it" and "Details" next to the article, below it under 1024 px.
// Contract: the aside is the one role=complementary in main; the badge is the text
// "Article" beside the h1; the copy button is named "Copy command". The Scope value
// is not covered: the Article the API returns has no `scope` field (see the header).
test.describe('the item detail aside', () => {
  const aside = (page: Page) => main(page).getByRole('complementary');
  const body = (page: Page) => main(page).getByText('Use the platform chart.');
  const box = async (l: ReturnType<Page['locator']>) => (await l.boundingBox())!;

  test.use({ viewport: { width: 1280, height: 800 } });

  test('shows the Kind badge next to the name', async ({ page }) => {
    await stubArticle(page, article());
    await open(page);
    const badge = h1(page).locator('xpath=..').getByText('Article', { exact: true });
    await expect(badge).toBeVisible();
    const h = await box(h1(page));
    const b = await box(badge);
    expect(b.y + b.height / 2, 'the badge is on the line of the name').toBeGreaterThanOrEqual(h.y);
    expect(b.y + b.height / 2, 'the badge is on the line of the name').toBeLessThanOrEqual(h.y + h.height);
  });

  test('Use it has the CLI command with a copy button and the MCP tool name', async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await stubArticle(page, article());
    await open(page);
    await expect(aside(page)).toHaveCount(1);
    await expect(aside(page).getByText('Use it', { exact: true })).toBeVisible();
    await expect(aside(page).getByText('goldie lookup platform/deploy', { exact: true })).toBeVisible();
    await expect(aside(page).getByText('get_article')).toBeVisible();
    await aside(page).getByRole('button', { name: 'Copy command', exact: true }).click();
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe('goldie lookup platform/deploy');
    await expect(aside(page).locator('[aria-live="polite"]')).toHaveText('Copied');
    await expect(aside(page).getByRole('status')).toHaveCount(0);
  });

  test('Details has the keywords, and the override only when the article shadows one', async ({ page }) => {
    await stubArticle(page, article({ overrides: ['global'] }));
    await open(page);
    await expect(aside(page).getByText('Details', { exact: true })).toBeVisible();
    await expect(aside(page).getByText('Scope', { exact: true })).toBeVisible();
    await expect(aside(page).getByText('kubernetes', { exact: true })).toBeVisible();
    await expect(aside(page).getByText('deploy', { exact: true })).toBeVisible();
    expect(norm(await aside(page).innerText())).toMatch(/Overrides .+ from global/);
  });

  test('without overrides the aside has no Overrides line', async ({ page }) => {
    await stubArticle(page, article({ overrides: [] }));
    await open(page);
    await expect(aside(page).getByText('Details', { exact: true })).toBeVisible();
    await expect(aside(page).getByText(/Overrides/)).toHaveCount(0);
  });

  test('at 1280 px the aside sits beside the article, no wider than 360 px', async ({ page }) => {
    await stubArticle(page, article());
    await open(page);
    await expect(aside(page)).toBeVisible();
    const a = await box(aside(page));
    const t = await box(body(page));
    expect(a.width).toBeLessThanOrEqual(360);
    expect(t.width).toBeLessThanOrEqual(760);
    expect(a.x, 'the aside is right of the article').toBeGreaterThanOrEqual(t.x + t.width);
    expect(a.y, 'the aside starts beside the article, not below it').toBeLessThan(t.y + t.height);
  });

  test('under 1024 px the aside drops below the article', async ({ page }) => {
    await page.setViewportSize({ width: 1000, height: 800 });
    await stubArticle(page, article());
    await open(page);
    await expect(aside(page)).toBeVisible();
    const a = await box(aside(page));
    const t = await box(body(page));
    expect(a.y, 'the aside is below the article').toBeGreaterThanOrEqual(t.y + t.height);
  });

  test('loading shows the layout as a skeleton and the h1 is never "Loading…"', async ({ page }) => {
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    await page.route(apiArticle, async (route) => {
      await gate;
      await json(route, 200, article());
    });
    await open(page);
    await expect(aside(page)).toBeVisible();
    await expect(main(page).getByRole('heading', { level: 1 }).filter({ hasText: /Loading/ })).toHaveCount(0);
    release();
    await expect(h1(page)).toHaveText('Deploying to Kubernetes');
    await expect(main(page).getByRole('heading', { level: 1 }).filter({ hasText: /Loading/ })).toHaveCount(0);
  });
});

test.describe('wide content at 375 px', () => {
  test.use({ viewport: { width: 375, height: 812 } });

  // scrollBox finds the nearest ancestor of the element, inside main, that
  // scrolls on its own, and reports whether it overflows while the page does not.
  const scrollBox = (el: Element) => {
    let box: Element | null = el;
    while (box && box.tagName !== 'MAIN') {
      const ox = getComputedStyle(box).overflowX;
      if (ox === 'auto' || ox === 'scroll') break;
      box = box.parentElement;
    }
    if (!box || box.tagName === 'MAIN') return { found: false, overflows: false, fits: false, page: 0 };
    return {
      found: true,
      overflows: box.scrollWidth > box.clientWidth,
      fits: box.getBoundingClientRect().right <= document.documentElement.clientWidth,
      page: document.documentElement.scrollWidth - document.documentElement.clientWidth,
    };
  };

  test('a wide code block scrolls inside its own box', async ({ page }) => {
    await stubArticle(page, article(wide));
    await open(page);
    const box = await main(page).locator('pre').evaluate(scrollBox);
    expect(box.found, 'the code block or a wrapper has overflow-x auto or scroll').toBe(true);
    expect(box.overflows, 'the box scrolls').toBe(true);
    expect(box.fits, 'the box stays inside the viewport').toBe(true);
    expect(box.page, 'the page scrolls sideways').toBeLessThanOrEqual(0);
  });

  test('a wide table scrolls inside its own box', async ({ page }) => {
    await stubArticle(page, article(wide));
    await open(page);
    const box = await main(page).locator('table').evaluate(scrollBox);
    expect(box.found, 'the table or a wrapper has overflow-x auto or scroll').toBe(true);
    expect(box.overflows, 'the box scrolls').toBe(true);
    expect(box.fits, 'the box stays inside the viewport').toBe(true);
    expect(box.page, 'the page scrolls sideways').toBeLessThanOrEqual(0);
  });

  test('a long unbroken word in the text does not widen the page', async ({ page }) => {
    await stubArticle(page, article({ content: `${'x'.repeat(300)}\n\n[${'y'.repeat(300)}](https://example.org/)` }));
    await open(page);
    await expect(h1(page)).toBeVisible();
    const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(over).toBeLessThanOrEqual(0);
  });
});

// Choosing another Project in the selector on an open article (#844). The view fetches again
// for the new Project; it never keeps the previous Project's copy on screen.
test.describe('an article when the Project changes', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  const shop = 'github.com/acme/shop';
  const billing = 'github.com/acme/billing';
  const selector = (page: Page) => page.locator('header').getByRole('combobox', { name: /project/i });

  async function stub(page: Page, copies: Record<string, ReturnType<typeof article> | null>) {
    await page.route(/\/api\/v1\/projects$/, (route) => json(route, 200, { projects: [shop, billing] }));
    await page.route(apiArticle, (route) => {
      const project = new URL(route.request().url()).searchParams.get('project') ?? '';
      const copy = copies[project];
      return copy ? json(route, 200, copy) : json(route, 404, envelope('not_found', 'No article has the id platform/deploy.', 'Search for it by its keywords.'));
    });
  }

  async function choose(page: Page, project: string) {
    await selector(page).click();
    await page.getByRole('option', { name: new RegExp(project.replace(/[./]/g, '\\$&')) }).click();
    await expect.poll(() => new URL(page.url()).searchParams.get('project')).toBe(project);
  }

  test("shows the other Project's copy of the article without a reload", async ({ page }) => {
    await stub(page, {
      [shop]: article({ name: 'Shop deploy copy', content: '## Steps\n\nShop steps.' }),
      [billing]: article({ name: 'Billing deploy copy', content: '## Steps\n\nBilling steps.' }),
    });
    await open(page, `platform%2Fdeploy?project=${encodeURIComponent(shop)}`);
    await expect(h1(page)).toHaveText('Shop deploy copy');
    await page.evaluate(() => ((window as unknown as { __kept: boolean }).__kept = true));

    await choose(page, billing);

    await expect(h1(page)).toHaveText('Billing deploy copy');
    await expect(main(page)).toContainText('Billing steps.');
    await expect(main(page)).not.toContainText('Shop steps.');
    expect(await page.evaluate(() => (window as unknown as { __kept?: boolean }).__kept), 'the page was reloaded').toBe(true);
  });

  test('shows the not-found state, not the stale copy, where the article does not resolve', async ({ page }) => {
    await stub(page, { [shop]: article({ name: 'Shop deploy copy' }), [billing]: null });
    await open(page, `platform%2Fdeploy?project=${encodeURIComponent(shop)}`);
    await expect(h1(page)).toHaveText('Shop deploy copy');

    await choose(page, billing);

    await expect(alert(page)).toContainText('No article has the id platform/deploy.');
    await expect(main(page)).not.toContainText('Shop deploy copy');
    await expect(h1(page)).toHaveCount(0);
  });
});
