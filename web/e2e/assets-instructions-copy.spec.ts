// The Instructions digest copy feedback (#590 round 2): the result is visible, what is copied is
// what is shown, and the message does not outlive a target switch.
import { expect, test, type Page } from '@playwright/test';

const shop = 'github.com/acme/shop';
const shown = 'a'.repeat(64);

async function stub(page: Page) {
  await page.route(/\/api\/v1\/projects$/, (r) => r.fulfill({ json: { projects: [shop] } }));
  await page.route(/\/api\/v1\/instructions(\?.*)?$/, (r) =>
    r.fulfill({ json: { block: 'ok', digest: `aaaa​${shown.slice(4)}` } }),
  );
  await page.goto(`/assets/instructions?project=${encodeURIComponent(shop)}`);
}

test.use({ viewport: { width: 1280, height: 800 } });

test('copying the digest shows Copied, copies the displayed text and clears on a target switch', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await stub(page);
  const panel = page.getByRole('tabpanel');
  await expect(panel.getByText(shown, { exact: true })).toBeVisible();
  await panel.getByRole('button', { name: 'Copy digest' }).click();
  const status = panel.getByRole('status').filter({ hasText: 'Copied' });
  await expect(status).toBeVisible();
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(shown);
  await panel.getByRole('group').getByRole('button', { name: 'AGENTS.md' }).click();
  await expect(panel.getByRole('status').filter({ hasText: 'Copied' })).toHaveCount(0);
});
