// The /articles route (#724): the article index resolves with its ?project= query intact, and is
// not the not-found page. The cards and states are in web/e2e/articles.spec.ts.
import { describe, expect, it } from 'vitest';
import { createMemoryHistory, createRouter } from 'vue-router';
import { routes } from '../router';

async function go(path: string) {
  const router = createRouter({ history: createMemoryHistory(), routes });
  await router.push(path);
  await router.isReady();
  return router.currentRoute.value;
}

describe('the /articles route', () => {
  it('resolves /articles?project= to its own page and keeps the query', async () => {
    const r = await go('/articles?project=github.com%2Facme%2Fshop');
    expect(r.matched[0]?.path).toBe('/articles');
    expect(r.query.project).toBe('github.com/acme/shop');
    expect(r.meta.title).toBe('Articles');
  });

  it('does not take over the article item route', async () => {
    const r = await go('/items/article/platform%2Fdeploy');
    expect(r.matched[0]?.path).toBe('/items/article/:name');
    expect(r.params.name).toBe('platform/deploy');
  });
});
