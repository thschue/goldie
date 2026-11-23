// The /assets/:tab route of the Agent assets frame (#584): /assets redirects to
// /assets/skills (keeping the query), the four tabs resolve (Loops since #647),
// and a wrong-case tab does not. The browser behaviour of the tabs is in web/e2e/assets.spec.ts.
import { describe, expect, it } from 'vitest';
import { createMemoryHistory, createRouter } from 'vue-router';
import { routes } from '../router';

async function go(path: string) {
  const router = createRouter({ history: createMemoryHistory(), routes });
  await router.push(path);
  await router.isReady();
  return router.currentRoute.value;
}

describe('the Agent assets routes', () => {
  it('redirects /assets to /assets/skills', async () => {
    expect((await go('/assets')).fullPath).toBe('/assets/skills');
  });

  it('keeps the ?project= query when it redirects', async () => {
    const r = await go('/assets?project=demo');
    expect(r.path).toBe('/assets/skills');
    expect(r.query.project).toBe('demo');
  });

  for (const tab of ['skills', 'agents', 'loops', 'instructions']) {
    it(`resolves /assets/${tab} and keeps its path and query`, async () => {
      const r = await go(`/assets/${tab}?project=demo`);
      expect(r.fullPath).toBe(`/assets/${tab}?project=demo`);
      expect(r.params.tab).toBe(tab);
    });
  }

  it('matches the tab slug case-sensitively: /assets/Loops is not a tab', async () => {
    const r = await go('/assets/Loops');
    expect(r.params.tab ?? '').not.toBe('Loops');
  });
});
