// MarkdownBody (#498): the article's Markdown, untrusted, rendered without raw
// HTML. Rendered with Vue's server renderer; the browser behaviour (CSP, no
// script run, scrolling) is in web/e2e/article.spec.ts.
//
// Contract for the implementation: `MarkdownBody.vue` takes the prop `source`
// (the Markdown) and renders the body. The only links it makes are http: and
// https: ones, with rel="noopener noreferrer". Headings start at h2.
import { describe, expect, it } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import MarkdownBody from './MarkdownBody.vue';

async function render(source: string) {
  const app = createSSRApp({ render: () => h(MarkdownBody, { source }) });
  return (await renderToString(app)).replace(/<!--[\s\S]*?-->/g, '');
}

// Built from parts so this file names no external URL.
const https = 'https:';
const http = 'http:';

const hostile = [
  '# Top',
  '',
  'Text <script>window.pwned = 1</script> after.',
  '',
  '<img src=x onerror="window.pwned = 2">',
  '',
  '<iframe src="//evil.example/frame"></iframe>',
  '',
  '<style>body { display: none }</style>',
  '',
  '<a href="javascript:window.pwned = 3">raw anchor</a>',
  '',
  '[md js](javascript:window.pwned = 4)',
  '',
  '[mixed case](JaVaScRiPt:alert(5))',
  '',
  '[entity](&#106;avascript:alert(6))',
  '',
  '[data link](data:text/html;base64,PHNjcmlwdD5hbGVydCg3KTwvc2NyaXB0Pg==)',
  '',
  '[vb](vbscript:msgbox(8))',
  '',
  '![js image](javascript:alert(9))',
  '',
  '![data image](data:text/html,x)',
  '',
  `[quote break](${https}//example.org/" onmouseover="alert(10))`,
].join('\n');

describe('MarkdownBody with hostile Markdown', () => {
  it('emits no script, iframe, style, img or object element and no event handler attribute', async () => {
    const html = await render(hostile);
    expect(html).not.toMatch(/<\s*(script|iframe|style|img|object|embed|svg|link|meta|base|form)\b/i);
    expect(html).not.toMatch(/<[^>]*\son\w+\s*=/i);
    expect(html).not.toMatch(/<[^>]*\sstyle\s*=/i);
  });

  it('emits no javascript:, data: or vbscript: URL in any attribute', async () => {
    const html = await render(hostile);
    expect(html).not.toMatch(/\b(href|src|action|formaction|xlink:href)\s*=\s*["']?\s*(javascript|data|vbscript)\s*:/i);
    expect(html).not.toMatch(/\b(href|src)\s*=\s*["']?\s*(&#|&amp;#)/i);
  });

  it('makes no link of the unsafe ones: their text stays, their URL is gone', async () => {
    const html = await render(hostile);
    expect(html).not.toMatch(/<a\b/i);
    for (const text of ['md js', 'mixed case', 'entity', 'data link', 'vb', 'quote break']) {
      expect(html, `text of the link "${text}" is kept`).toContain(text);
    }
  });

  it('shows raw HTML as escaped text, or drops it', async () => {
    const html = await render(hostile);
    expect(html).not.toContain('<script');
    expect(html).not.toContain('<iframe');
    expect(html).not.toContain('<img');
  });

  it('starts the headings at h2: no h1, one level down, never past h6', async () => {
    const html = await render('# One\n\n## Two\n\n### Three\n\n###### Six\n');
    expect(html).not.toMatch(/<h1\b/);
    expect(html).toMatch(/<h2[^>]*>One</);
    expect(html).toMatch(/<h3[^>]*>Two</);
    expect(html).toMatch(/<h4[^>]*>Three</);
    expect(html).toMatch(/<h6[^>]*>Six</);
    expect(html).not.toMatch(/<h7\b/);
  });
});

describe('MarkdownBody with ordinary Markdown', () => {
  it('links only http: and https:, with rel="noopener noreferrer"', async () => {
    const html = await render(`[secure](${https}//example.org/a) and [plain](${http}//example.org/b)`);
    const anchors = [...html.matchAll(/<a\b[^>]*>/g)].map(([a]) => a);
    expect(anchors).toHaveLength(2);
    for (const a of anchors) {
      expect(a).toMatch(/rel="[^"]*\bnoopener\b[^"]*"/);
      expect(a).toMatch(/rel="[^"]*\bnoreferrer\b[^"]*"/);
    }
    expect(anchors[0]).toContain(`href="${https}//example.org/a"`);
    expect(anchors[1]).toContain(`href="${http}//example.org/b"`);
  });

  it('does not link mailto:, relative, protocol-relative or file: targets', async () => {
    const html = await render('[m](mailto:a@example.org) [r](/api/v1/search) [p](//evil.example/x) [f](file:///etc/passwd) [h](#frag)');
    expect(html).not.toMatch(/<a\b/i);
    expect(html).toContain('>m<');
  });

  it('renders emphasis, lists, tables and fenced code, and escapes code content', async () => {
    const md = ['**bold** and *em*', '', '- one', '- two', '', '| a | b |', '| - | - |', '| 1 | 2 |', '', '```sh', 'echo "<script>x</script>"', '```'].join('\n');
    const html = await render(md);
    expect(html).toMatch(/<strong>bold<\/strong>/);
    expect(html).toMatch(/<em>em<\/em>/);
    expect(html).toMatch(/<ul\b[^>]*>[\s\S]*<li>one<\/li>/);
    expect(html).toMatch(/<table\b[^>]*>[\s\S]*<th[^>]*>a<\/th>/);
    expect(html).toMatch(/<pre\b[^>]*>[\s\S]*<code[^>]*>/);
    expect(html).not.toContain('<script');
    expect(html).toContain('&lt;script&gt;');
  });

  it('renders an empty source as nothing and does not throw', async () => {
    expect(await render('')).not.toMatch(/<(script|a|img)\b/);
  });
});
