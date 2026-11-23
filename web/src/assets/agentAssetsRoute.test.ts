// The /assets/:tab route of the Agent assets frame (#584): /assets redirects to
// /assets/skills (keeping the query), the three tabs resolve, and no route names
// a Loops tab. The browser behaviour of the tabs is in web/e2e/assets.spec.ts.
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

  for (const tab of ['skills', 'agents', 'instructions']) {
    it(`resolves /assets/${tab} and keeps its path and query`, async () => {
      const r = await go(`/assets/${tab}?project=demo`);
      expect(r.fullPath).toBe(`/assets/${tab}?project=demo`);
      expect(r.params.tab).toBe(tab);
    });
  }

  it('has no route that names a Loops tab', () => {
    expect(routes.map((r) => String(r.path)).filter((p) => /loops/i.test(p))).toEqual([]);
  });
});
