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

  // #731, handoff §3.3 and §4: the card's parts and their order.
  it('reads Kind, name, Scope, lifecycle, the matched text, then an aria-hidden score bar with the score as text', async () => {
    const html = await render({ status: 'deprecated', score: 6 });
    const at = (s: string) => html.indexOf(s);
    expect(at('article')).toBeLessThan(at('Deploying'));
    expect(at('Deploying')).toBeLessThan(at('global'));
    expect(at('global')).toBeLessThan(at('deprecated'));
    expect(at('deprecated')).toBeLessThan(at('Matched: kubernetes, name'));
    const bar = html.search(/<[a-z]+[^>]*aria-hidden="true"(?![^>]*viewBox)[^>]*>/);
    expect(bar, 'a score bar').toBeGreaterThan(at('Matched: kubernetes, name'));
    // the score is text outside every aria-hidden element
    const visible = html
      .replace(/<[a-z]+[^>]*aria-hidden="true"[^>]*>[\s\S]*?<\/(span|div|i)>/g, '')
      .replace(/<[^>]+>/g, ' ');
    expect(visible).toMatch(/\b6\b/);
  });

  it('shows a draft and a deprecated badge, and none for a published hit', async () => {
    expect(await render({ status: 'draft' })).toContain('draft');
    expect(await render({ status: 'deprecated', replaced_by: 'article:platform/deploy-v2' })).toContain('deprecated');
    expect(await render({})).not.toMatch(/draft|deprecated/);
  });

  it('shows a summary only when the hit carries one, as text', async () => {
    expect(await render({})).not.toMatch(/summary/i);
    const html = await render({ summary: 'Ship it <b>safely</b>' } as Partial<SearchHit>);
    expect(html).toContain('Ship it &lt;b&gt;safely&lt;/b&gt;');
    expect(html).not.toContain('<b>');
  });
});
