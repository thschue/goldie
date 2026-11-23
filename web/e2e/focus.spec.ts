// The focus cue on a route change (#473, handoff §8, §9, WCAG 2.4.3): a nav or
// drawer link moves focus to the new view's h1 (tabindex="-1"); the initial
// load and a browser back or forward do not move it. Live updates never move
// focus (A8), so only user navigation does.
import { expect, test, type Page } from '@playwright/test';
import { openDrawer, sideNav } from './ux';

const h1 = (page: Page) => page.getByRole('heading', { level: 1 });

// focusOnH1 is true when the focused element is an h1.
const focusOnH1 = (page: Page) => page.evaluate(() => document.activeElement?.tagName === 'H1');

// focusAtPageStart is true when nothing inside the page has taken focus: the
// focus is on the body, as after a fresh load.
const focusAtPageStart = (page: Page) =>
  page.evaluate(() => document.activeElement === document.body || document.activeElement === document.documentElement);

test.describe('a nav link in the inline nav (1280 px)', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test("moves focus to the new view's h1, which is tabindex -1", async ({ page }) => {
    await page.goto('/');
    await sideNav(page).getByRole('link', { name: 'Decisions', exact: true }).click();
    await expect(page).toHaveURL((u) => u.pathname === '/decisions');
    await expect(h1(page)).toHaveText('Decisions');
    await expect(h1(page)).toHaveAttribute('tabindex', '-1');
    await expect(h1(page)).toBeFocused();
  });

  test("moves focus again on each following nav link, to that view's h1", async ({ page }) => {
    await page.goto('/');
    for (const name of ['Skills', 'Agents']) {
      await sideNav(page).getByRole('link', { name, exact: true }).click();
      await expect(h1(page)).toHaveText('Agent assets');
      await expect(h1(page)).toBeFocused();
    }
  });

  test('moves focus to the h1 of the view reached from the not-found view', async ({ page }) => {
    await page.goto('/no/such/page');
    await expect(h1(page)).toHaveText('Page not found');
    await page.getByRole('link', { name: 'Go to Home' }).click();
    await expect(page).toHaveURL((u) => u.pathname === '/');
    await expect(h1(page)).toHaveText('What are you deciding today?');
    await expect(h1(page)).toBeFocused();
  });

  test('gives the h1 no visible focus ring after a pointer click', async ({ page }) => {
    await page.goto('/');
    await sideNav(page).getByRole('link', { name: 'Decisions', exact: true }).click();
    await expect(h1(page)).toBeFocused();
    const ring = await h1(page).evaluate((el) => {
      const s = getComputedStyle(el);
      return { visible: el.matches(':focus-visible'), none: s.outlineStyle === 'none' || parseFloat(s.outlineWidth) === 0 };
    });
    expect(ring, 'a pointer-driven programmatic focus shows no ring').toEqual({ visible: false, none: true });
  });

  test('does not move focus on the initial load', async ({ page }) => {
    await page.goto('/decisions');
    await expect(h1(page)).toHaveText('Decisions');
    expect(await focusOnH1(page), 'h1 has focus after the initial load').toBe(false);
    expect(await focusAtPageStart(page), 'focus is at the page start').toBe(true);
    // The first Tab still reaches the skip link, as at the page start.
    await page.keyboard.press('Tab');
    await expect(page.getByRole('link', { name: 'Skip to content' })).toBeFocused();
  });

  test('does not move focus on browser back or forward to the same view', async ({ page }) => {
    await page.goto('/');
    await sideNav(page).getByRole('link', { name: 'Decisions', exact: true }).click();
    await expect(h1(page)).toBeFocused();

    await page.goBack();
    await expect(page).toHaveURL((u) => u.pathname === '/');
    await expect(h1(page)).toHaveText('What are you deciding today?');
    expect(await focusOnH1(page), 'h1 has focus after back').toBe(false);
    expect(await focusAtPageStart(page), 'focus is at the page start after back').toBe(true);

    await page.goForward();
    await expect(page).toHaveURL((u) => u.pathname === '/decisions');
    await expect(h1(page)).toHaveText('Decisions');
    expect(await focusOnH1(page), 'h1 has focus after forward').toBe(false);
    expect(await focusAtPageStart(page), 'focus is at the page start after forward').toBe(true);
  });
});

test.describe('a drawer link (375 px)', () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test("closes the drawer and moves focus to the new view's h1, which is tabindex -1", async ({ page }) => {
    await page.goto('/');
    const drawer = await openDrawer(page);
    await drawer.getByRole('link', { name: 'Decisions', exact: true }).click();
    await expect(page).toHaveURL((u) => u.pathname === '/decisions');
    await expect(drawer).toBeHidden();
    await expect(h1(page)).toHaveText('Decisions');
    await expect(h1(page)).toHaveAttribute('tabindex', '-1');
    await expect(h1(page)).toBeFocused();
    expect(await page.getByRole('main').evaluate((el) => !!el.closest('[inert]')), 'main is no longer inert').toBe(false);
  });

  test('does not move focus on the initial load or on back from a drawer navigation', async ({ page }) => {
    await page.goto('/');
    expect(await focusAtPageStart(page), 'focus is at the page start after load').toBe(true);
    const drawer = await openDrawer(page);
    await drawer.getByRole('link', { name: 'Skills', exact: true }).click();
    await expect(h1(page)).toBeFocused();
    await page.goBack();
    await expect(h1(page)).toHaveText('What are you deciding today?');
    expect(await focusOnH1(page), 'h1 has focus after back').toBe(false);
    expect(await focusAtPageStart(page), 'focus is at the page start after back').toBe(true);
  });
});

// #473 polish: the keyboard path shows the §6.4 ring on the h1.
test.describe('a nav link activated with the keyboard (1280 px)', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('moves focus to the h1, which matches :focus-visible', async ({ page }) => {
    await page.goto('/');
    await sideNav(page).getByRole('link', { name: 'Decisions', exact: true }).focus();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL((u) => u.pathname === '/decisions');
    await expect(h1(page)).toBeFocused();
    expect(await h1(page).evaluate((el) => el.matches(':focus-visible')), 'keyboard navigation shows the ring').toBe(true);
  });
});
