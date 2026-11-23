// The App shell at / (#166 slice 1, handoff §3.1, §7, §9): the frame every
// screen renders into. Per width and theme: the shot, axe (with best-practice),
// the landmarks, the empty state and the top bar. The side nav, the drawer, the
// routes and the 1024 px change are slice 2.
import { expect, test } from '@playwright/test';
import { eachViewport, evidence, missingBrandFonts, problems, themeToggle } from './ux';

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

  test('the shell has header and main landmarks and one h1 with the empty state', async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('header')).toHaveCount(1);
    await expect(page.getByRole('banner')).toBeVisible();
    await expect(page.getByRole('main')).toBeVisible();
    await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('What are you deciding today?');
    const panel = page.getByRole('main').getByRole('status');
    await expect(panel).toHaveText(emptyCopy);
  });

  test('the empty-state panel is neutral, never gold', async ({ page }) => {
    await page.goto('/');
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

  test('the top bar is 64 px: the goldie mark, then the theme toggle', async ({ page }) => {
    await page.goto('/');
    const header = await page.locator('header').boundingBox();
    expect(Math.round(header!.height)).toBe(64);
    const mark = page.locator('header').getByRole('img', { name: /goldie/i }).or(page.locator('header').getByRole('link', { name: /goldie/i }));
    await expect(mark.first()).toBeVisible();
    await expect(page.locator('header')).not.toContainText(/phippy/i);
    const m = (await mark.first().boundingBox())!;
    const t = (await themeToggle(page).boundingBox())!;
    expect(m.x + m.width, 'mark before the toggle').toBeLessThanOrEqual(t.x);
  });

  test('the skip link is the first focusable element', async ({ page }) => {
    await page.goto('/');
    const first = await page.evaluate(
      () => document.querySelector<HTMLElement>('a[href], button, input, [tabindex]:not([tabindex="-1"])')?.textContent?.trim(),
    );
    expect(first).toBe('Skip to content');
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
