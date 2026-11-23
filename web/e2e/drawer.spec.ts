// The nav drawer below 1024 px (#166, handoff §7, §9): the shots
// shell-<375|768>-<theme>-drawer.png, the way it closes, the focus trap, the
// inert page behind it, and the keyboard path.
import { expect, test } from '@playwright/test';
import { eachViewport, evidence, expectFocusRing, focusInside, keyboardPath, menuButton, openDrawer, problems } from './ux';

eachViewport((width, theme) => {
  if (width >= 1024) return;

  test('the open drawer is captured, axe-clean and without horizontal scroll', async ({ page, baseURL }) => {
    const seen = await problems(page, baseURL ?? '');
    await page.goto('/');
    await openDrawer(page);
    await evidence(page, width, theme, '-drawer');
    expect(seen.external).toEqual([]);
    expect(seen.errors).toEqual([]);
  });

  test('the menu button names what it controls and the drawer holds the nav', async ({ page }) => {
    await page.goto('/');
    const button = menuButton(page);
    await expect(button).toHaveAttribute('aria-expanded', 'false');
    const id = await button.getAttribute('aria-controls');
    expect(id).toBeTruthy();
    const drawer = await openDrawer(page);
    await expect(drawer.getByRole('navigation')).toBeVisible();
    await expect(drawer.getByRole('link', { name: 'Home', exact: true })).toBeVisible();
  });

  test('the page behind the open drawer is inert', async ({ page }) => {
    await page.goto('/');
    await openDrawer(page);
    const inert = await page.getByRole('main').evaluate((el) => !!el.closest('[inert]'));
    expect(inert, 'main is inert while the drawer is open').toBe(true);
  });

  test('Esc closes the drawer and focus returns to the menu button', async ({ page }) => {
    await page.goto('/');
    const drawer = await openDrawer(page);
    await page.keyboard.press('Escape');
    await expect(menuButton(page)).toHaveAttribute('aria-expanded', 'false');
    await expect(drawer).toBeHidden();
    await expect(menuButton(page)).toBeFocused();
    expect(await page.getByRole('main').evaluate((el) => !!el.closest('[inert]')), 'main is no longer inert').toBe(false);
  });

  test('activating a link closes the drawer, goes to the route and returns focus to the menu button', async ({ page }) => {
    await page.goto('/');
    const drawer = await openDrawer(page);
    await drawer.getByRole('link', { name: 'Decisions', exact: true }).click();
    await expect(page).toHaveURL((u) => u.pathname === '/decisions');
    await expect(menuButton(page)).toHaveAttribute('aria-expanded', 'false');
    await expect(drawer).toBeHidden();
    await expect(menuButton(page)).toBeFocused();
  });

  test('a click on the scrim closes the drawer and focus returns to the menu button', async ({ page }) => {
    await page.goto('/');
    const drawer = await openDrawer(page);
    const box = (await drawer.boundingBox())!;
    expect(box.x + box.width, 'the drawer leaves room for the scrim').toBeLessThan(width);
    await page.mouse.click(width - 4, 400);
    await expect(menuButton(page)).toHaveAttribute('aria-expanded', 'false');
    await expect(drawer).toBeHidden();
    await expect(menuButton(page)).toBeFocused();
  });

  test('focus is trapped in the drawer going forward and back', async ({ page }) => {
    await page.goto('/');
    const drawer = await openDrawer(page);
    const count = await drawer.locator('a[href], button, [tabindex]:not([tabindex="-1"])').count();
    for (const key of ['Tab', 'Shift+Tab']) {
      for (let i = 0; i < count + 2; i++) {
        await page.keyboard.press(key);
        expect(await focusInside(drawer), `${key} #${i + 1}`).toBe(true);
      }
    }
  });

  test('there is no transition under prefers-reduced-motion', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.goto('/');
    const drawer = await openDrawer(page);
    const durations = await drawer.evaluate((el) => {
      const scrim = [...document.querySelectorAll('*')].filter((e) => /scrim|backdrop|overlay/i.test(e.className?.toString() ?? ''));
      return [el, ...el.querySelectorAll('*'), ...scrim].map((e) => getComputedStyle(e).transitionDuration + '|' + getComputedStyle(e).animationDuration);
    });
    for (const d of durations) {
      expect(d.replace(/0s|,|\s|\|/g, ''), `durations "${d}"`).toBe('');
    }
  });

  test('the keyboard path: skip link, menu button, open, trap, Esc, focus return', async ({ page }) => {
    await page.goto('/');
    await keyboardPath(page);
  });
});

test.describe('the skip link', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('is the first focusable element, 44 px, visibly focused, and moves focus to main', async ({ page }) => {
    await page.goto('/');
    const skip = page.getByRole('link', { name: 'Skip to content' });
    await page.keyboard.press('Tab');
    await expect(skip).toBeFocused();
    const box = (await skip.boundingBox())!;
    expect(box.height).toBeGreaterThanOrEqual(44);
    expect(box.width).toBeGreaterThanOrEqual(44);
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.y).toBeGreaterThanOrEqual(0);
    await expectFocusRing(skip);
    await page.keyboard.press('Enter');
    const inMain = await page.getByRole('main').evaluate((el) => el.contains(document.activeElement));
    expect(inMain, 'focus moves into main').toBe(true);
  });

  test('every focusable control shows the 2 px focus ring with a 2 px offset', async ({ page }) => {
    await page.goto('/');
    for (let i = 0; i < 12; i++) {
      await page.keyboard.press('Tab');
      const active = page.locator(':focus');
      if ((await active.count()) === 0) break;
      if (await active.evaluate((el) => el === document.body || el === document.documentElement)) break;
      const ring = await active.evaluate((el) => {
        const s = getComputedStyle(el);
        return { width: s.outlineWidth, offset: s.outlineOffset, style: s.outlineStyle };
      });
      expect(ring, `focus ring on tab #${i + 1}`).toEqual({ width: '2px', offset: '2px', style: 'solid' });
    }
  });
});
