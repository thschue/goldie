// The UX evidence every screen spec shares (handoff §7, §9, §12): a
// screenshot per width and theme, axe with zero violations, no horizontal
// page scroll, targets of at least 44 px, and no request leaving the server.
import { expect, test, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

export const widths = [375, 768, 1280] as const;
export const themes = ['light', 'dark'] as const;
export type Theme = (typeof themes)[number];

// Heights for a typical device at each width; shots are full page anyway.
const heights: Record<(typeof widths)[number], number> = { 375: 812, 768: 1024, 1280: 800 };

const shotDir = `${process.env.UX_DIR ?? '../ux'}/screenshots`;

// The handoff's bar is WCAG 2.1 AA (§9); 2.2 is included for its new rules.
// best-practice adds the page-structure rules (one h1, landmarks, regions).
const axeTags = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22a', 'wcag22aa', 'best-practice'];

// Controls a user acts on (§9: targets of at least 44 px).
const targets =
  'a[href], button, input:not([type="hidden"]), select, textarea, summary, ' +
  '[role="button"], [role="link"], [role="tab"], [role="menuitem"], [role="checkbox"], ' +
  '[role="switch"], [tabindex]:not([tabindex="-1"])';

// eachViewport runs the spec body once per width and theme. The theme is set by
// colorScheme emulation, never by clicking a toggle, so a shot shows what a
// visitor with that system preference sees first.
export function eachViewport(body: (width: number, theme: Theme) => void) {
  for (const width of widths) {
    for (const theme of themes) {
      test.describe(`${width} px, ${theme}`, () => {
        test.use({ viewport: { width, height: heights[width] }, colorScheme: theme });
        body(width, theme);
      });
    }
  }
}

// blockExternal fails the page's requests to any other origin and returns
// them: fonts and every other asset are self-hosted, so a shot never depends
// on the network.
export async function blockExternal(page: Page, baseURL: string): Promise<string[]> {
  const external: string[] = [];
  const origin = new URL(baseURL).origin;
  await page.route('**/*', (route) => {
    const url = route.request().url();
    if (new URL(url).origin === origin) {
      return route.continue();
    }
    external.push(url);
    return route.abort();
  });
  return external;
}

// evidence saves the shot <name>-<width>-<theme><suffix>.png (name defaults to
// "shell"; suffix is "-drawer" for an open drawer) and then checks the page.
// The shot is taken first, so a failing check still leaves the evidence.
export async function evidence(page: Page, width: number, theme: Theme, suffix = '', name = 'shell') {
  await page.evaluate(() => document.fonts.ready);
  const file = `${name}-${width}-${theme}${suffix}.png`;
  const shot = await page.screenshot({
    path: `${shotDir}/${file}`,
    fullPage: true,
    animations: 'disabled',
    caret: 'hide',
  });
  await test.info().attach(file, { body: shot, contentType: 'image/png' });

  const axe = await new AxeBuilder({ page }).withTags(axeTags).analyze();
  expect.soft(axe.violations, 'axe violations').toEqual([]);

  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect.soft(overflow, 'horizontal page scroll in px').toBeLessThanOrEqual(0);

  const small = await page.locator(targets).evaluateAll((els) =>
    els.flatMap((el) => {
      const r = el.getBoundingClientRect();
      const style = getComputedStyle(el);
      // Not rendered, visually hidden (a skip link until focused), or a link
      // inside a sentence (the WCAG 2.5.5 inline exception).
      if (style.visibility === 'hidden' || r.width <= 1 || r.height <= 1) return [];
      if (style.display === 'inline' && el.closest('p')) return [];
      if (r.width >= 44 && r.height >= 44) return [];
      const label = (el.getAttribute('aria-label') ?? el.textContent ?? '').trim().slice(0, 40);
      return [`${el.tagName.toLowerCase()} "${label}" is ${Math.round(r.width)}x${Math.round(r.height)} px`];
    }),
  );
  expect.soft(small, 'targets under 44 px').toEqual([]);
}

// problems watches a page for what must never happen (handoff §12): a request
// to another origin, a same-origin 4xx or 5xx response or failed request, a
// page error, and a Content-Security-Policy violation. Call it before goto.
export async function problems(page: Page, baseURL: string) {
  const external = await blockExternal(page, baseURL);
  const origin = new URL(baseURL).origin;
  const failed: string[] = [];
  const errors: string[] = [];
  page.on('response', (res) => {
    if (new URL(res.url()).origin === origin && res.status() >= 400) {
      failed.push(`${res.status()} ${new URL(res.url()).pathname}`);
    }
  });
  page.on('requestfailed', (req) => {
    if (new URL(req.url()).origin === origin) failed.push(`failed ${new URL(req.url()).pathname}`);
  });
  page.on('pageerror', (err) => errors.push(err.message));
  page.on('console', (msg) => {
    if (/content security policy/i.test(msg.text())) errors.push(msg.text());
  });
  return { external, failed, errors };
}

// missingBrandFonts lists the brand fonts (§6.4) that are not loaded. They are
// self-hosted: Space Grotesk is the display face of the h1, Inter the body.
export async function missingBrandFonts(page: Page): Promise<string[]> {
  await page.evaluate(() => document.fonts.ready);
  return page.evaluate(() => {
    const loaded = new Set<string>();
    document.fonts.forEach((f) => {
      if (f.status === 'loaded') loaded.add(f.family.replace(/["']/g, ''));
    });
    return ['Inter', 'Space Grotesk'].filter((f) => !loaded.has(f) || !document.fonts.check(`16px "${f}"`));
  });
}

type Loc = ReturnType<Page['locator']>;

export const menuButton = (page: Page) => page.locator('header').getByRole('button', { name: /menu/i });
export const themeToggle = (page: Page) => page.locator('header').getByRole('button', { name: /theme/i });
export const sideNav = (page: Page) => page.getByRole('navigation');

// The drawer is the element the menu button controls (aria-controls).
async function controlled(page: Page): Promise<Loc> {
  const id = await menuButton(page).getAttribute('aria-controls');
  expect(id, 'the menu button has aria-controls').toBeTruthy();
  return page.locator(`[id="${id}"]`);
}

export const focusInside = (container: Loc) =>
  container.evaluate((el) => el.contains(document.activeElement) && document.activeElement !== document.body);

// openDrawer opens the drawer below 1024 px and returns it.
export async function openDrawer(page: Page): Promise<Loc> {
  const button = menuButton(page);
  await expect(button).toHaveAttribute('aria-expanded', 'false');
  await button.click();
  await expect(button).toHaveAttribute('aria-expanded', 'true');
  const drawer = await controlled(page);
  await expect(drawer).toBeVisible();
  await expect.poll(() => focusInside(drawer), 'focus moves into the drawer').toBe(true);
  return drawer;
}

// withNav makes the side nav usable at any width: it opens the drawer when the
// menu button is showing, and does nothing when the nav is inline.
export async function withNav(page: Page): Promise<Loc> {
  if ((await menuButton(page).isVisible()) && (await menuButton(page).getAttribute('aria-expanded')) !== 'true') {
    await openDrawer(page);
  }
  await expect(sideNav(page)).toBeVisible();
  return sideNav(page);
}

// expectNavCollapsed checks the shell change at 1024 px (§7): from 1024 px
// up the nav is inline and there is no menu button; below it the nav is
// hidden behind the menu button.
export async function expectNavCollapsed(page: Page, width: number) {
  if (width >= 1024) {
    await expect(menuButton(page)).toBeHidden();
    await expect(sideNav(page)).toBeVisible();
  } else {
    await expect(menuButton(page)).toBeVisible();
    await expect(menuButton(page)).toHaveAttribute('aria-expanded', 'false');
    await expect(sideNav(page)).toBeHidden();
  }
}

// expectFocusRing: the focused element has a 2 px outline with a 2 px offset (§6.4).
export async function expectFocusRing(locator: Loc) {
  await expect(locator).toBeFocused();
  const ring = await locator.evaluate((el) => {
    const s = getComputedStyle(el);
    return { width: s.outlineWidth, offset: s.outlineOffset, style: s.outlineStyle };
  });
  expect(ring, 'focus ring of the focused element').toEqual({ width: '2px', offset: '2px', style: 'solid' });
}

// keyboardPath walks the keyboard flow of the shell below 1024 px: the skip
// link first (sized, on screen, with a focus ring), then the header controls
// to the menu button, open with Enter, focus trapped both ways, Esc closes,
// and focus returns to the menu button.
export async function keyboardPath(page: Page) {
  const skip = page.getByRole('link', { name: 'Skip to content' });
  await page.keyboard.press('Tab');
  await expect(skip, 'the skip link is the first focusable element').toBeFocused();
  const box = await skip.boundingBox();
  expect(box, 'the focused skip link is on screen').not.toBeNull();
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.y).toBeGreaterThanOrEqual(0);
  expect(box!.height).toBeGreaterThanOrEqual(44);
  expect(box!.width).toBeGreaterThanOrEqual(44);
  await expectFocusRing(skip);

  const button = menuButton(page);
  for (let i = 0; i < 8; i++) {
    await page.keyboard.press('Tab');
    if (await button.evaluate((el) => el === document.activeElement)) break;
  }
  await expectFocusRing(button);
  await page.keyboard.press('Enter');
  await expect(button).toHaveAttribute('aria-expanded', 'true');
  const drawer = await controlled(page);
  await expect.poll(() => focusInside(drawer)).toBe(true);

  const count = await drawer.locator('a[href], button, [tabindex]:not([tabindex="-1"])').count();
  expect(count, 'the drawer holds the nav links').toBeGreaterThanOrEqual(6);
  for (const key of ['Tab', 'Shift+Tab']) {
    for (let i = 0; i < count + 2; i++) {
      await page.keyboard.press(key);
      expect(await focusInside(drawer), `${key} #${i + 1} stays in the drawer`).toBe(true);
    }
  }
  await page.keyboard.press('Escape');
  await expect(button).toHaveAttribute('aria-expanded', 'false');
  await expect(drawer).toBeHidden();
  await expect(button, 'focus returns to the menu button').toBeFocused();
}
