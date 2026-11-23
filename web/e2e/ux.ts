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
const axeTags = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22a', 'wcag22aa'];

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

// evidence saves the shot shell-<width>-<theme><suffix>.png (suffix is
// "-drawer" for an open drawer) and then checks the page. The shot is taken
// first, so a failing check still leaves the evidence.
export async function evidence(page: Page, width: number, theme: Theme, suffix = '') {
  await page.evaluate(() => document.fonts.ready);
  const name = `shell-${width}-${theme}${suffix}.png`;
  const shot = await page.screenshot({
    path: `${shotDir}/${name}`,
    fullPage: true,
    animations: 'disabled',
    caret: 'hide',
  });
  await test.info().attach(name, { body: shot, contentType: 'image/png' });

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
