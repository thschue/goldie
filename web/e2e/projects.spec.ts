// The Project selector (#496, handoff §2.1, §5.1, §9). It sits in the top bar
// and reads GET /api/v1/projects. `/?project=host/org/repo` is Home with that
// Project selected (#725): what Home shows for it is in home.spec.ts. The API is
// stubbed per test (page.route) so every state is deterministic: the shared
// server's own Projects never matter.
//
// Contract these specs pin down for the implementation:
// - the selector is a role=combobox named "Project" in the header (a native
//   select or an ARIA combobox), whose choices are role=option.
import { expect, test, type Page, type Route } from '@playwright/test';

const shop = 'github.com/acme/shop';
const none = 'github.com/acme/none';
const apiList = /\/api\/v1\/projects$/;

const json = (route: Route, status: number, body: unknown) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

const stubProjects = (page: Page, projects: string[] = [shop, 'github.com/acme/billing']) =>
  page.route(apiList, (route) => json(route, 200, { projects }));

const selector = (page: Page) => page.locator('header').getByRole('combobox', { name: /project/i });
const projectParam = (page: Page) => new URL(page.url()).searchParams.get('project');

test.describe('the Project selector', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('lists the Projects from GET /api/v1/projects, full host/org/repo, each with a registered chip', async ({ page }) => {
    await stubProjects(page);
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    await expect(selector(page)).toContainText(shop);
    await selector(page).click();
    const options = page.getByRole('option');
    await expect(options).toHaveCount(3);
    await expect(options.nth(0)).toContainText('All projects (global guidance)');
    await expect(options.nth(1)).toContainText(shop);
    await expect(options.nth(1)).toContainText('registered');
    await expect(options.nth(1)).not.toContainText('unregistered');
    await expect(options.nth(2)).toContainText('github.com/acme/billing');
  });

  test('shows the id in a mono face', async ({ page }) => {
    await stubProjects(page);
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    const family = await selector(page).getByText(shop, { exact: false }).first().evaluate((el) => getComputedStyle(el).fontFamily);
    expect(family).toMatch(/mono/i);
  });

  test('a Project the server does not list carries an unregistered chip with text', async ({ page }) => {
    await stubProjects(page);
    await page.goto(`/?project=${encodeURIComponent(none)}`);
    await expect(selector(page)).toContainText(none);
    await expect(selector(page)).toContainText('unregistered');
  });

  test('shows org/repo below 768 px and the full id from 768 px', async ({ page }) => {
    await stubProjects(page);
    await page.setViewportSize({ width: 767, height: 900 });
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    await expect(selector(page)).toContainText('acme/shop');
    await expect(selector(page)).not.toContainText('github.com');
    await page.setViewportSize({ width: 768, height: 900 });
    await expect(selector(page)).toContainText(shop);
  });

  test('holds the selection in the project query parameter, never in storage, and keeps other parameters', async ({ page }) => {
    await stubProjects(page);
    await page.goto('/?kind=article');
    await selector(page).click();
    await page.getByRole('option').filter({ hasText: 'github.com/acme/billing' }).click();
    await expect.poll(() => projectParam(page)).toBe('github.com/acme/billing');
    expect(new URL(page.url()).searchParams.get('kind')).toBe('article');
    const stored = await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }));
    expect(stored, 'the Project is not in web storage').not.toContain('billing');
    await page.reload();
    await expect(selector(page)).toContainText('github.com/acme/billing');
  });

  test('can be operated by keyboard alone: open, move, choose, and Escape closes', async ({ page }) => {
    await stubProjects(page);
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    await selector(page).focus();
    await expect(selector(page)).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.getByRole('option').first()).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('option')).toHaveCount(0);
    await expect(selector(page), 'focus stays on the selector after Escape').toBeFocused();
    await page.keyboard.press('Enter');
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('Enter');
    await expect.poll(() => projectParam(page)).toBe('github.com/acme/billing');
  });

  test('renders a hostile Project id from the list as text', async ({ page }) => {
    await stubProjects(page, ['github.com/acme/<img src=x onerror=window.__pwn=1>']);
    await page.goto('/');
    await selector(page).click();
    await expect(page.getByRole('option').nth(1)).toContainText('<img src=x onerror=window.__pwn=1>');
    expect(await page.locator('header img').count()).toBe(0);
    expect(await page.evaluate(() => (window as unknown as { __pwn?: number }).__pwn)).toBeUndefined();
  });
});

// The selector can return to all projects (#804, handoff §2.1, §5): the first
// option, "All projects (global guidance)", clears the Project.
test.describe('the Project selector, all projects option', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  const all = 'All projects (global guidance)';
  const billing = 'github.com/acme/billing';
  const allOption = (page: Page) => page.getByRole('option').filter({ hasText: all });
  const activeId = (page: Page) => selector(page).getAttribute('aria-activedescendant');

  test('is the first option, unselected with a Project, and choosing it drops project, keeps q and target, and Back restores it', async ({ page }) => {
    await stubProjects(page);
    await page.goto(`/?project=${encodeURIComponent(shop)}&q=deploy&target=claude`);
    await selector(page).click();
    await expect(page.getByRole('option').first()).toContainText(all);
    await expect(allOption(page)).toHaveAttribute('aria-selected', 'false');
    await allOption(page).click();
    await expect.poll(() => projectParam(page)).toBeNull();
    const params = new URL(page.url()).searchParams;
    expect(params.get('q')).toBe('deploy');
    expect(params.get('target')).toBe('claude');
    await expect(selector(page)).toContainText('Choose a Project');
    await page.goBack();
    await expect.poll(() => projectParam(page)).toBe(shop);
    await expect(selector(page)).toContainText(shop);
  });

  test('with no Project the trigger reads "Choose a Project", the option is selected, and there is no registered or unregistered chip', async ({ page }) => {
    await stubProjects(page);
    await page.goto('/');
    await expect(selector(page)).toContainText('Choose a Project');
    await selector(page).click();
    await expect(allOption(page)).toHaveAttribute('aria-selected', 'true');
    await expect(allOption(page)).not.toContainText(/registered/);
    await expect(selector(page)).not.toContainText(/registered/);
  });

  test('is present and selectable while the list loads', async ({ page }) => {
    await page.route(apiList, async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 3000));
      await json(route, 200, { projects: [shop] });
    });
    await page.goto(`/?project=${encodeURIComponent(shop)}&q=x`);
    await selector(page).click();
    await expect(page.getByText('Loading Projects')).toBeVisible();
    await allOption(page).click();
    await expect.poll(() => projectParam(page)).toBeNull();
    expect(new URL(page.url()).searchParams.get('q')).toBe('x');
  });

  test('is present and selectable when the list failed to load, with the note below it', async ({ page }) => {
    await page.route(apiList, (route) => json(route, 500, { error: { code: 'internal', message: 'boom', details: {} } }));
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    await selector(page).click();
    await expect(page.getByRole('option').first()).toContainText(all);
    await expect(page.getByRole('option').nth(1)).toContainText("Couldn't load Projects");
    await allOption(page).click();
    await expect.poll(() => projectParam(page)).toBeNull();
  });

  test('is present and selectable when no Projects are registered, with the note below it', async ({ page }) => {
    await stubProjects(page, []);
    await page.goto(`/?project=${encodeURIComponent(shop)}`);
    await selector(page).click();
    await expect(page.getByRole('option').first()).toContainText(all);
    await expect(page.getByRole('option').nth(1)).toContainText('No Projects registered yet');
    await allOption(page).click();
    await expect.poll(() => projectParam(page)).toBeNull();
  });

  test('keyboard: opening lands on the current Project, arrows wrap through all options, Enter on the first option clears the Project', async ({ page }) => {
    await stubProjects(page);
    await page.goto(`/?project=${encodeURIComponent(billing)}`);
    await selector(page).focus();
    await page.keyboard.press('Enter');
    const options = page.getByRole('option');
    await expect(options).toHaveCount(3);
    const ids = await options.evaluateAll((els) => els.map((el) => el.id));
    expect(new Set(ids).size, 'option ids are unique').toBe(3);
    expect(await activeId(page), 'opens on the current Project').toBe(ids[2]);
    await page.keyboard.press('ArrowDown');
    expect(await activeId(page), 'ArrowDown wraps to the first option').toBe(ids[0]);
    await page.keyboard.press('ArrowUp');
    expect(await activeId(page), 'ArrowUp wraps back to the last').toBe(ids[2]);
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('Enter');
    await expect.poll(() => projectParam(page)).toBeNull();
  });

  test('keyboard: with no Project opening lands on the option, Escape closes without a change, Space selects it', async ({ page }) => {
    await stubProjects(page);
    await page.goto('/?q=x');
    await selector(page).focus();
    await page.keyboard.press('Enter');
    const first = await page.getByRole('option').first().getAttribute('id');
    expect(await activeId(page), 'opens on the all projects option').toBe(first);
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('Escape');
    await expect(page.getByRole('option')).toHaveCount(0);
    expect(projectParam(page)).toBeNull();
    await page.keyboard.press('Enter');
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('ArrowUp');
    await page.keyboard.press('Space');
    await expect(page.getByRole('option')).toHaveCount(0);
    expect(projectParam(page)).toBeNull();
    expect(new URL(page.url()).searchParams.get('q')).toBe('x');
  });
});
