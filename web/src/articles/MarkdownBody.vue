<script lang="ts">
import { defineComponent, h, type VNodeChild } from 'vue';
import type { Token } from 'markdown-it';
import { parse } from './markdown';

// The article's body (#498). `source` is untrusted Markdown. It is parsed with
// raw HTML off and turned into Vue nodes, never into an HTML string (ADR 0010):
// text is always a text node, only known tags are made, and the only attributes
// are an http(s) href with rel on a link and the start of an ordered list.
const tags = new Set(['p', 'h2', 'h3', 'h4', 'h5', 'h6', 'ul', 'ol', 'li', 'blockquote', 'em', 'strong', 's', 'a', 'span', 'table', 'thead', 'tbody', 'tr', 'th', 'td']);

type Frame = { tag: string; props: Record<string, string>; kids: VNodeChild[] };

function props(t: Token): Record<string, string> {
  if (t.tag === 'a') return { href: String(t.attrGet('href') ?? ''), rel: 'noopener noreferrer' };
  const start = t.tag === 'ol' ? t.attrGet('start') : null;
  return start && /^\d{1,9}$/.test(String(start)) ? { start: String(start) } : {};
}

// A box that scrolls sideways is a focusable, named region, so a keyboard user can scroll it.
const scrollable = (label: string) => ({ tabindex: '0', role: 'region', 'aria-label': label });

function render(tokens: Token[]): VNodeChild[] {
  const stack: Frame[] = [{ tag: '', props: {}, kids: [] }];
  const add = (n: VNodeChild) => stack[stack.length - 1].kids.push(n);
  for (const t of tokens) {
    if (t.nesting === 1) {
      // A hidden paragraph (a tight list item's) has no element of its own.
      stack.push({ tag: t.hidden ? '' : tags.has(t.tag) ? t.tag : 'span', props: props(t), kids: [] });
    } else if (t.nesting === -1) {
      const f = stack.pop();
      if (!f || stack.length === 0) return [];
      if (f.tag === '') {
        for (const n of f.kids) add(n);
        continue;
      }
      const el = h(f.tag, f.props, f.kids);
      // A table scrolls inside its own box, never the page.
      add(f.tag === 'table' ? h('div', { class: 'scroll', ...scrollable('Table') }, [el]) : el);
    } else if (t.type === 'inline') {
      for (const n of render(t.children ?? [])) add(n);
    } else if (t.type === 'fence' || t.type === 'code_block') {
      add(h('pre', scrollable('Code'), [h('code', t.content)]));
    } else if (t.type === 'code_inline') {
      add(h('code', t.content));
    } else if (t.type === 'hardbreak') {
      add(h('br'));
    } else if (t.type === 'softbreak') {
      add('\n');
    } else if (t.type === 'hr') {
      add(h('hr'));
    } else if (t.content) {
      add(t.content);
    }
  }
  return stack[0].kids;
}

export default defineComponent({
  props: { source: { type: String, required: true }, contiguous: Boolean },
  setup: (props) => () => h('div', { class: 'article-body' }, render(parse(props.source, props.contiguous))),
});
</script>

<!-- Not scoped: the nodes are made by the render function, and a scope id on every tag would make the markup noisy. -->
<style>
.article-body {
  max-width: 760px;
  overflow-wrap: anywhere;
}
.article-body h2,
.article-body h3,
.article-body h4,
.article-body h5,
.article-body h6 {
  margin: var(--space-6) 0 var(--space-3);
  font-family: var(--font-display);
  line-height: var(--leading-tight);
}
.article-body h2 {
  font-size: var(--text-display-md);
}
.article-body h3 {
  font-size: var(--text-display-sm);
}
.article-body pre {
  max-width: 100%;
  margin: var(--space-4) 0;
  padding: var(--space-4);
  overflow-x: auto;
  border-radius: var(--radius-md);
  background: var(--code-surface);
  color: var(--color-on-brand);
  font-size: var(--text-md);
}
.article-body code {
  font-family: var(--font-mono);
  font-size: 0.92em;
}
.article-body pre:focus-visible,
.article-body .scroll:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: 2px;
}
.article-body pre code {
  overflow-wrap: normal;
}
.article-body .scroll {
  max-width: 100%;
  margin: var(--space-4) 0;
  overflow-x: auto;
}
.article-body table {
  border-collapse: collapse;
  font-size: var(--text-md);
}
.article-body th,
.article-body td {
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border-default);
  text-align: left;
  white-space: nowrap;
}
.article-body th {
  background: var(--surface-2);
}
</style>
