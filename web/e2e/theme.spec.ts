// ThemeToggle (#166, handoff §6.2): light, dark and system; the choice is
// persisted, data-theme is set before first paint, an explicit choice wins over
// the system preference, and the aria-label names the state.
import { expect, test, type Page } from '@playwright/test';
import { themeToggle } from './ux';

test.use({ viewport: { width: 1280, height: 800 } });

type State = 'light' | 'dark' | 'system';

// chooseTheme clicks the toggle until its aria-label names the wanted state.
async function chooseTheme(page: Page, want: State) {
  for (let i = 0; i < 4; i++) {
    const label = ((await themeToggle(page).getAttribute('aria-label')) ?? '').toLowerCase();
    if (label.includes(want)) return;
    await themeToggle(page).click();
  }
  throw new Error(`no theme toggle state is labelled ${want}`);
}

// surfaceLuminance reads the --surface-1 role token as sRGB luminance (0..1).
const surfaceLuminance = (page: Page) =>
  page.evaluate(() => {
    const raw = getComputedStyle(document.documentElement).getPropertyValue('--surface-1').trim();
    const ctx = document.createElement('canvas').getContext('2d')!;
    ctx.fillStyle = raw;
    ctx.fillRect(0, 0, 1, 1);
    const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data;
    return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255;
  });

test.describe('system light', () => {
  test.use({ colorScheme: 'light' });

  test('the toggle has three states and its aria-label names each one', async ({ page }) => {
    await page.goto('/');
    const seen: string[] = [];
    for (let i = 0; i < 3; i++) {
      const label = (await themeToggle(page).getAttribute('aria-label')) ?? '';
      seen.push(label);
      await themeToggle(page).click();
    }
    expect(new Set(seen).size, `three distinct labels: ${seen}`).toBe(3);
    for (const state of ['light', 'dark', 'system']) {
      expect(seen.filter((l) => l.toLowerCase().includes(state)), `one label names ${state}`).toHaveLength(1);
    }
    expect((await themeToggle(page).getAttribute('aria-label')) ?? '', 'three clicks come full circle').toBe(seen[0]);
  });

  test('choosing dark sets data-theme=dark, is persisted, and is set before first paint after a reload', async ({ page }) => {
    await page.goto('/');
    await chooseTheme(page, 'dark');
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
    expect(await surfaceLuminance(page), 'dark surface under a light system').toBeLessThan(0.5);
    const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }));
    expect(stored.toLowerCase(), 'the choice is in localStorage').toContain('dark');

    // Record the document's state at the moment data-theme first appears.
    // Observe `document` itself: at init-script time documentElement can still be null.
    await page.addInitScript(() => {
      const w = window as unknown as { __themeAt?: string };
      const note = () => {
        const root = document.documentElement;
        if (root && root.hasAttribute('data-theme') && !w.__themeAt) w.__themeAt = document.readyState;
      };
      new MutationObserver(note).observe(document, { attributes: true, subtree: true, attributeFilter: ['data-theme'] });
      note();
    });
    await page.reload();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
    expect(await page.evaluate(() => (window as unknown as { __themeAt?: string }).__themeAt), 'data-theme is set while the page is still loading').toBe('loading');
    expect(await themeToggle(page).getAttribute('aria-label')).toMatch(/dark/i);
  });

  test('system follows the system preference, live', async ({ page }) => {
    await page.goto('/');
    await chooseTheme(page, 'system');
    expect(await surfaceLuminance(page)).toBeGreaterThan(0.5);
    await page.emulateMedia({ colorScheme: 'dark' });
    await expect.poll(() => surfaceLuminance(page)).toBeLessThan(0.5);
  });
});

test.describe('system dark', () => {
  test.use({ colorScheme: 'dark' });

  test('with no choice the page follows the system preference (dark)', async ({ page }) => {
    await page.goto('/');
    expect(await surfaceLuminance(page)).toBeLessThan(0.5);
  });

  test('an explicit light wins over a dark system preference, and survives a reload', async ({ page }) => {
    await page.goto('/');
    await chooseTheme(page, 'light');
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
    expect(await surfaceLuminance(page)).toBeGreaterThan(0.5);
    await page.reload();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
    expect(await surfaceLuminance(page)).toBeGreaterThan(0.5);
  });

  test('the toggle meets the 44 px target size', async ({ page }) => {
    await page.goto('/');
    const box = (await themeToggle(page).boundingBox())!;
    expect(box.width).toBeGreaterThanOrEqual(44);
    expect(box.height).toBeGreaterThanOrEqual(44);
  });
});
