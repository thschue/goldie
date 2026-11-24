// The boxes of MarkdownBody that scroll sideways are keyboard reachable (#498).
import { describe, expect, it } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import MarkdownBody from './MarkdownBody.vue';

const render = async (source: string) => renderToString(createSSRApp({ render: () => h(MarkdownBody, { source }) }));

describe('MarkdownBody scroll boxes', () => {
  it('makes a code block a focusable named region', async () => {
    expect(await render('```sh\necho hi\n```')).toMatch(/<pre\b(?=[^>]*tabindex="0")(?=[^>]*role="region")(?=[^>]*aria-label="Code")/);
  });

  it('makes a table box a focusable named region', async () => {
    const html = await render('| a | b |\n| - | - |\n| 1 | 2 |');
    expect(html).toMatch(/<div\b(?=[^>]*class="scroll")(?=[^>]*tabindex="0")(?=[^>]*role="region")(?=[^>]*aria-label="Table")/);
  });
});
