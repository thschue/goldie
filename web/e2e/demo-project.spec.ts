// The demo Project in the browser (#767): `goldie server --dev` on an empty directory serves the
// built-in demo, which registers github.com/harbor/checkout. Nothing is stubbed: the specs run
// against that server, not the shared ./run ux server on examples/knowledge.
//
// ./run ux starts the --dev server and exports its address and throwaway token as
// GOLDIE_UX_DEMO_URL and GOLDIE_UX_DEMO_TOKEN. Without them the specs fail, they never skip.
//
// Contract these specs pin down for the implementation:
// - the Project selector is a role=combobox named "Project" in the header, whose choices are
//   role=option, and it offers the demo Project;
// - choosing it puts it in the ?project= query and Home (handoff §3.2) shows the Articles tile and a
//   "Specific to <project>" card that links the Project-only article (Checkout payment flow) and the
//   demo's retries article (override of the global one);
// - the Skills tab of /assets/skills?project=<id> shows the demo skill service-health as a card
//   (role=listitem in the tabpanel) headed by its name.
import { expect, test, type Page } from '@playwright/test';

const demoProject = 'github.com/harbor/checkout';
const demoURL = process.env.GOLDIE_UX_DEMO_URL ?? '';
const demoToken = process.env.GOLDIE_UX_DEMO_TOKEN ?? '';

test.use({ baseURL: demoURL || undefined, storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 800 } });

test.beforeEach(async ({ page }) => {
  expect(demoURL, 'GOLDIE_UX_DEMO_URL is set by ./run ux to its server --dev on an empty directory').not.toBe('');
  expect(demoToken, 'GOLDIE_UX_DEMO_TOKEN is set by ./run ux').not.toBe('');
  const res = await page.request.post('/api/v1/session', { data: { token: demoToken }, headers: { Origin: new URL(demoURL).origin } });
  expect(res.status()).toBe(200);
});

const main = (page: Page) => page.getByRole('main');
const selector = (page: Page) => page.locator('header').getByRole('combobox', { name: /project/i });

test('the Project selector offers the demo Project', async ({ page }) => {
  await page.goto('/');
  await selector(page).click();
  await expect(page.getByRole('option', { name: new RegExp(demoProject.replace(/\./g, '\\.')) })).toBeVisible();
  await expect(page.getByText('No Projects registered yet.')).toHaveCount(0);
});

test('choosing the demo Project shows its Items on Home', async ({ page }) => {
  await page.goto('/');
  await selector(page).click();
  await page.getByRole('option', { name: new RegExp(demoProject.replace(/\./g, '\\.')) }).click();
  await expect.poll(() => new URL(page.url()).searchParams.get('project')).toBe(demoProject);
  await expect(selector(page)).toContainText(demoProject);
  const card = main(page)
    .locator('section, div, article')
    .filter({ has: page.getByRole('heading', { name: `Specific to ${demoProject}` }) })
    .last();
  await expect(card.getByRole('link', { name: /Checkout payment flow/ })).toBeVisible();
  await expect(card.getByRole('link', { name: /Retries and timeouts in checkout/ })).toHaveAttribute('href', /^\/items\/article\/[^?]*retries(\?|$)/);
  await expect(main(page).getByRole('link', { name: /^\s*Articles\b/ })).toBeVisible();
});

test('choosing the demo Project shows its Items on the asset tabs', async ({ page }) => {
  await page.goto(`/assets/skills?project=${encodeURIComponent(demoProject)}`);
  const panel = page.getByRole('tabpanel');
  await expect(panel.getByRole('listitem').filter({ has: page.getByRole('heading', { name: 'service-health' }) })).toBeVisible();
  await expect(page.getByRole('alert')).toHaveCount(0);
});

test('a Project article opened from Home renders, and Details shows the Project as Scope (#826)', async ({ page }) => {
  await page.goto(`/?project=${encodeURIComponent(demoProject)}`);
  const card = main(page)
    .locator('section, div, article')
    .filter({ has: page.getByRole('heading', { name: `Specific to ${demoProject}` }) })
    .last();
  await card.getByRole('link', { name: /Checkout payment flow/ }).click();
  await expect(page).toHaveURL((u) => u.pathname === '/items/article/payment-flow' && u.searchParams.get('project') === demoProject);
  await expect(main(page).getByRole('heading', { level: 1 })).toHaveText(/Checkout payment flow/);
  await expect(page.getByRole('alert')).toHaveCount(0);
  const details = main(page).locator('section').filter({ has: page.getByRole('heading', { name: 'Details' }) });
  await expect(details.locator('dt', { hasText: 'Scope' }).locator('xpath=following-sibling::dd[1]')).toHaveText(demoProject);
});
