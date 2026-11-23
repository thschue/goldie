// The page at / (the placeholder until the App shell lands, #166) at each
// width and theme. The drawer, the nav collapsing below 1024 px and the
// keyboard path are specs of the shell itself.
import { expect, test } from '@playwright/test';
import { blockExternal, eachViewport, evidence } from './ux';

eachViewport((width, theme) => {
  test('the page at / is captured, axe-clean, without horizontal scroll or small targets', async ({ page, baseURL }) => {
    const external = await blockExternal(page, baseURL ?? '');
    const res = await page.goto('/');
    expect(res?.status()).toBe(200);
    await evidence(page, width, theme);
    expect(external, 'requests to other origins').toEqual([]);
  });
});
