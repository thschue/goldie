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

// A view whose h1 only renders after an async load (#550). No routed view does
// that yet, so the browser stands in for one: lateH1Script pulls the next h1
// that a render adds to the page out of the DOM and puts it back after delayMs,
// which is what such a view looks like to the shell.
const lateH1Script = (delayMs: number) => {
  const observer = new MutationObserver((records) => {
    for (const record of records) {
      for (const node of Array.from(record.addedNodes)) {
        if (!(node instanceof Element)) continue;
        const heading = node.matches('h1') ? node : node.querySelector('h1');
        const parent = heading?.parentNode;
        if (!heading || !parent) continue;
        observer.disconnect();
        const anchor = heading.nextSibling;
        parent.removeChild(heading);
        setTimeout(() => {
          parent.insertBefore(heading, anchor && anchor.parentNode === parent ? anchor : parent.firstChild);
        }, delayMs);
        return;
      }
    }
  });
  observer.observe(document.querySelector('main') ?? document.body, { childList: true, subtree: true });
};

const lateH1 = (page: Page, delayMs: number) => page.evaluate(`(${lateH1Script.toString()})(${delayMs})`);

test.describe('a view whose h1 renders late (1280 px)', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('moves focus to the h1 once it renders after a nav link click', async ({ page }) => {
    await page.goto('/');
    await expect(h1(page)).toBeVisible();
    await lateH1(page, 600);
    await sideNav(page).getByRole('link', { name: 'Decisions', exact: true }).click();
    await expect(page).toHaveURL((u) => u.pathname === '/decisions');
    await expect(h1(page), 'the h1 is absent until the view has loaded').toHaveCount(0);
    await expect(h1(page)).toHaveText('Decisions');
    await expect(h1(page)).toHaveAttribute('tabindex', '-1');
    await expect(h1(page)).toBeFocused();
  });

  test('does not move focus when the h1 renders late on the initial load', async ({ page }) => {
    await page.addInitScript(`document.addEventListener('DOMContentLoaded', () => (${lateH1Script.toString()})(500))`);
    await page.goto('/decisions');
    await expect(h1(page)).toHaveText('Decisions');
    await page.waitForTimeout(300);
    expect(await focusOnH1(page), 'h1 has focus after the initial load').toBe(false);
    expect(await focusAtPageStart(page), 'focus is at the page start').toBe(true);
  });

  test('does not move focus when the h1 renders late after browser back', async ({ page }) => {
    await page.goto('/');
    await sideNav(page).getByRole('link', { name: 'Decisions', exact: true }).click();
    await expect(h1(page)).toBeFocused();
    await lateH1(page, 500);
    await page.goBack();
    await expect(page).toHaveURL((u) => u.pathname === '/');
    await expect(h1(page)).toHaveText('What are you deciding today?');
    await page.waitForTimeout(300);
    expect(await focusOnH1(page), 'h1 has focus after back').toBe(false);
    expect(await focusAtPageStart(page), 'focus is at the page start after back').toBe(true);
  });

  // App.vue keys the routed view by path (#550): a path change remounts it, so
  // per-view state starts fresh. Pinned until a stateful view needs otherwise.
  test('a path change renders a new view: the old view and its state are gone', async ({ page }) => {
    await page.goto('/decisions');
    await expect(h1(page)).toHaveText('Decisions');
    await h1(page).evaluate((el) => ((el as HTMLElement & { seen?: boolean }).seen = true));
    await sideNav(page).getByRole('link', { name: 'Skills', exact: true }).click();
    await expect(h1(page)).toHaveText('Agent assets');
    await sideNav(page).getByRole('link', { name: 'Decisions', exact: true }).click();
    await expect(h1(page)).toHaveText('Decisions');
    expect(await h1(page).evaluate((el) => (el as HTMLElement & { seen?: boolean }).seen)).toBeUndefined();
  });
});

test.describe('a view whose h1 renders late, from the drawer (375 px)', () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test('closes the drawer and moves focus to the h1 once it renders', async ({ page }) => {
    await page.goto('/');
    const drawer = await openDrawer(page);
    await lateH1(page, 600);
    await drawer.getByRole('link', { name: 'Decisions', exact: true }).click();
    await expect(page).toHaveURL((u) => u.pathname === '/decisions');
    await expect(drawer).toBeHidden();
    await expect(h1(page)).toHaveText('Decisions');
    await expect(h1(page)).toBeFocused();
  });
});

// #659: the late h1 takes focus only while focus is still where the click left
// it (the body or the clicked link). Once the user has moved it, it stays put.
const activeLabel = (page: Page) =>
  page.evaluate(() => {
    const el = document.activeElement;
    if (el === document.body || el === document.documentElement) return 'body';
    return el?.getAttribute('aria-label') ?? el?.textContent?.trim() ?? el?.tagName;
  });

test.describe('a late h1 after the user moved focus (1280 px)', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('does not take focus when the user tabbed to another element while it was absent', async ({ page }) => {
    await page.goto('/');
    await expect(h1(page)).toBeVisible();
    await lateH1(page, 1200);
    await sideNav(page).getByRole('link', { name: 'Decisions', exact: true }).click();
    await expect(page).toHaveURL((u) => u.pathname === '/decisions');
    await expect(h1(page), 'the h1 is absent until the view has loaded').toHaveCount(0);
    await page.keyboard.press('Tab');
    const before = await activeLabel(page);
    expect(before, 'Tab moved focus off the clicked link').not.toBe('Decisions');
    await expect(h1(page)).toHaveText('Decisions');
    await page.waitForTimeout(300);
    expect(await focusOnH1(page), 'the late h1 took focus back').toBe(false);
    expect(await activeLabel(page), 'focus stays where the user put it').toBe(before);
  });

  test('does not take focus when the user focused a control outside the nav while it was absent', async ({ page }) => {
    await page.goto('/');
    await expect(h1(page)).toBeVisible();
    await lateH1(page, 1200);
    await sideNav(page).getByRole('link', { name: 'Decisions', exact: true }).click();
    await expect(h1(page)).toHaveCount(0);
    const skip = page.getByRole('link', { name: 'Skip to content' });
    await skip.focus();
    await expect(skip).toBeFocused();
    await expect(h1(page)).toHaveText('Decisions');
    await page.waitForTimeout(300);
    expect(await focusOnH1(page), 'the late h1 took focus back').toBe(false);
    await expect(skip).toBeFocused();
  });

  test('still focuses the h1 of a later nav click after the user moved focus during an earlier wait', async ({ page }) => {
    await page.goto('/');
    await expect(h1(page)).toBeVisible();
    await lateH1(page, 1200);
    await sideNav(page).getByRole('link', { name: 'Decisions', exact: true }).click();
    await expect(h1(page)).toHaveCount(0);
    await page.getByRole('link', { name: 'Skip to content' }).focus();
    await expect(h1(page)).toHaveText('Decisions');
    await page.waitForTimeout(300);
    expect(await focusOnH1(page)).toBe(false);
    // A fresh click is a fresh request: that view's h1 is focused again.
    await sideNav(page).getByRole('link', { name: 'Skills', exact: true }).click();
    await expect(h1(page)).toHaveText('Agent assets');
    await expect(h1(page)).toBeFocused();
  });
});

test.describe('a late h1 while focus is still where the click left it (1280 px)', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('takes focus when focus is still on the clicked link', async ({ page }) => {
    await page.goto('/');
    await expect(h1(page)).toBeVisible();
    await lateH1(page, 800);
    const link = sideNav(page).getByRole('link', { name: 'Decisions', exact: true });
    await link.click();
    await expect(h1(page)).toHaveCount(0);
    await expect(link, 'focus is on the clicked link').toBeFocused();
    await expect(h1(page)).toHaveText('Decisions');
    await expect(h1(page)).toBeFocused();
  });

  test('takes focus when focus fell back to the body during the wait', async ({ page }) => {
    await page.goto('/');
    await expect(h1(page)).toBeVisible();
    await lateH1(page, 800);
    await sideNav(page).getByRole('link', { name: 'Decisions', exact: true }).click();
    await expect(h1(page)).toHaveCount(0);
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    expect(await focusAtPageStart(page), 'focus is on the body').toBe(true);
    await expect(h1(page)).toHaveText('Decisions');
    await expect(h1(page)).toBeFocused();
  });
});
