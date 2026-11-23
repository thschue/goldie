import MarkdownIt, { type Token } from 'markdown-it';

// The article's Markdown comes from a Source and is untrusted. Raw HTML is
// switched off (it renders as text), the only links are http: and https: ones,
// images are not rendered, and the first heading is an h2 (the page title is the h1).
// MarkdownBody turns the tokens into Vue nodes, never into an HTML string.
const md = new MarkdownIt({ html: false, linkify: false });

// Let every link form, then keep or unwrap it in the core rule below, so an
// unsafe link keeps its text instead of showing its Markdown source.
md.validateLink = () => true;

const safeUrl = /^https?:\/\//i;

md.core.ruler.push('goldie_safe', (state) => {
  for (const block of state.tokens) {
    if (block.type !== 'inline' || !block.children) continue;
    let linked = false;
    // An unsafe link keeps its text, in a plain span.
    const unwrap = (type: string, nesting: 1 | -1) => new state.Token(type, 'span', nesting);
    block.children = block.children.map((t) => {
      if (t.type === 'link_open') {
        const href = String(t.attrGet('href') ?? '');
        linked = safeUrl.test(href);
        if (!linked) return unwrap('span_open', 1);
        t.attrs = [['href', href], ['rel', 'noopener noreferrer']];
      } else if (t.type === 'link_close') {
        if (!linked) return unwrap('span_close', -1);
        linked = false;
      } else if (t.type === 'image') {
        const text = new state.Token('text', '', 0);
        text.content = t.content;
        return text;
      }
      return t;
    });
  }
});

md.core.ruler.push('goldie_headings', (state) => {
  // The page title is the h1, so the first heading of the body becomes an h2 (whatever level the source gave
  // it: no gap after the h1, WCAG 1.3.1) and the rest move by the same amount, between h2 and h6.
  let shift: number | undefined;
  let tag = 'h2';
  for (const t of state.tokens) {
    if (t.type === 'heading_open') {
      const level = Number(t.tag.slice(1));
      shift ??= 2 - level;
      tag = `h${Math.min(Math.max(level + shift, 2), 6)}`;
      t.tag = tag;
    } else if (t.type === 'heading_close') {
      t.tag = tag;
    }
  }
});

export const parse = (source: string): Token[] => md.parse(source, {});
