// The first heading of an article body is an h2 whatever level the source gave it, so the page outline has
// no gap after the title's h1 (#498; axe heading-order).
import { describe, expect, it } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import MarkdownBody from './MarkdownBody.vue';

const render = async (source: string) => renderToString(createSSRApp({ render: () => h(MarkdownBody, { source }) }));

describe('MarkdownBody heading levels', () => {
  it('turns a body that opens with an h2 into an h2, not an h3', async () => {
    const html = await render('## Steps\n\n### Detail\n');
    expect(html).toMatch(/<h2[^>]*>Steps<\/h2>/);
    expect(html).toMatch(/<h3[^>]*>Detail<\/h3>/);
  });

  it('opens with an h2 even when the source starts deeper, and never goes above h2', async () => {
    const html = await render('#### Deep\n\n# Top\n');
    expect(html).toMatch(/<h2[^>]*>Deep<\/h2>/);
    expect(html).toMatch(/<h2[^>]*>Top<\/h2>/);
    expect(html).not.toMatch(/<h1\b/);
  });
});
