// ActivityStage (#811, handoff §3.6): the stage as SVG with a text summary beside it. Props: `view`
// (what createStage().read(now) returns) and `filter` (the Project the feed is filtered to, if any);
// it emits `filter` with a lane's Project (undefined to clear) when a lane button is pressed.
// Rendered with Vue's server renderer; the browser behaviour is in web/e2e.
import { describe, expect, it } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import ActivityStage from './ActivityStage.vue';
import { createStage, summaryEvery } from './stage';
import type { ActivityEvent } from './stream';

const t0 = Date.parse('2026-11-24T10:00:00Z');
const ev = (id: number, o: Partial<ActivityEvent> = {}): ActivityEvent => ({
  id,
  time: new Date(t0).toISOString(),
  kind: 'search',
  query: `q${id}`,
  project: 'github.com/acme/shop',
  token: 'ci',
  client_version: '',
  trace_id: '',
  miss: false,
  replica: 'goldie-0',
  late: false,
  served: 'skill:review',
  via: 'cli',
  ...o,
});

async function render(events: ActivityEvent[], o: { reduced?: boolean; filter?: string; at?: number } = {}) {
  const s = createStage({ reduced: o.reduced });
  s.read(t0);
  s.add(events, t0);
  const view = s.read(o.at ?? t0);
  const app = createSSRApp({ render: () => h(ActivityStage, { view, filter: o.filter }) });
  return (await renderToString(app)).replace(/<!--[\s\S]*?-->/g, '').replaceAll('&#39;', "'");
}

const imgOf = (html: string) => /<svg[^>]*role="img"[^>]*>[\s\S]*?<\/svg>/.exec(html)?.[0];

describe('ActivityStage', () => {
  it('is an SVG with role="img" and a live text summary beside it', async () => {
    const html = await render([ev(1), ev(2, { miss: true, served: undefined })], { at: t0 + summaryEvery });
    expect(imgOf(html)).toBeDefined();
    expect(html).toMatch(/<[a-z]+ [^>]*role="status"[^>]*>[^<]*2 requests/);
    expect(imgOf(html)).not.toMatch(/role="status"/);
  });

  it('draws a particle coloured by its surface, and none under reduced motion', async () => {
    const moving = await render([ev(1, { via: 'mcp' }), ev(2, { via: 'cli' })]);
    expect(moving).toMatch(/class="[^"]*\bsurface-mcp\b/);
    expect(moving).toMatch(/class="[^"]*\bsurface-cli\b/);
    const calm = await render([ev(1, { via: 'mcp' }), ev(2, { via: 'cli' })], { reduced: true });
    expect(calm).not.toMatch(/surface-(mcp|cli)/);
    expect(calm).toContain('role="img"');
  });

  it('makes each lane a real button outside the role="img" element, and marks the active filter', async () => {
    const html = await render([ev(1, { project: 'a/b/c' }), ev(2, { project: 'x/y/z' })], { filter: 'a/b/c' });
    const buttons = [...html.matchAll(/<button[^>]*>[\s\S]*?<\/button>/g)].map((m) => m[0]);
    expect(buttons).toHaveLength(2);
    for (const b of buttons) expect(b).toContain('type="button"');
    expect(buttons.find((b) => b.includes('a/b/c'))).toContain('aria-pressed="true"');
    expect(buttons.find((b) => b.includes('x/y/z'))).toContain('aria-pressed="false"');
    expect(imgOf(html)).not.toContain('<button');
  });

  it('shows the sampling chip text', async () => {
    expect(await render([ev(1)])).toContain('Showing every request');
  });

  it('renders a hostile Project and query as text only', async () => {
    const html = await render([ev(1, { project: '<img src=x onerror=alert(1)>', miss: true, served: undefined, query: '<script>x</script>' })]);
    expect(html).not.toMatch(/<img|<script/);
    expect(html).toContain('&lt;img src=x onerror=alert(1)&gt;');
  });
});
