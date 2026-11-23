// HitCard (#497, #529): the link path, the escaped text, and an absent
// `enforced` or `overrides` shown as nothing. Rendered with Vue's server
// renderer; the browser behaviour is in web/e2e/search.spec.ts.
import { describe, expect, it } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createMemoryHistory, createRouter } from 'vue-router';
import HitCard from './HitCard.vue';
import type { SearchHit } from './api';

const base: SearchHit = { id: 'platform/deploy', name: 'Deploying', kind: 'article', scope: 'global', keywords: [], score: 4, matched: ['kubernetes', 'name'] };

async function render(hit: Partial<SearchHit>) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] });
  const app = createSSRApp({ render: () => h('ul', [h(HitCard, { hit: { ...base, ...hit } })]) });
  app.use(router);
  return (await renderToString(app)).replace(/<!--[\s\S]*?-->/g, '');
}

describe('HitCard', () => {
  it('links to /items/<kind>/<id> with the id as one encoded segment', async () => {
    expect(await render({})).toContain('href="/items/article/platform%2Fdeploy"');
  });

  it('shows the enforced chip only for enforced: true, and overrides one line per Scope', async () => {
    const html = await render({ enforced: true, overrides: ['org', 'global'] });
    expect(html).toContain('enforced');
    expect(html).toContain('Overrides article:platform/deploy from org');
    expect(html).toContain('Overrides article:platform/deploy from global');
  });

  it('shows no chip and no line when enforced and overrides are absent, false or empty', async () => {
    for (const hit of [{}, { enforced: false, overrides: [] }]) {
      const html = await render(hit);
      expect(html).not.toMatch(/enforced|Overrides/);
    }
  });

  it('renders HTML and Markdown in the name and matched text as text', async () => {
    const html = await render({ name: '<img src=x onerror=alert(1)>', matched: ['<script>x</script>'] });
    expect(html).not.toContain('<img');
    expect(html).not.toContain('<script>');
    expect(html).toContain('&lt;img src=x onerror=alert(1)&gt;');
  });
});
