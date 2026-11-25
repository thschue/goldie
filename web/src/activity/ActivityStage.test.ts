// ActivityStage (#811, handoff §3.6): the stage as SVG with a text summary beside it. Props: `view`
// (what createStage().read(now) returns) and `filter` (the Project the feed is filtered to, if any);
// it emits `filter` with a lane's Project (undefined to clear) when a lane button is pressed.
// Rendered with Vue's server renderer; the browser behaviour is in web/e2e.
import { describe, expect, it } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import ActivityStage from './ActivityStage.vue';
import { createStage, summaryEvery, travelMs } from './stage';
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

async function render(events: ActivityEvent[], o: { reduced?: boolean; filter?: string; at?: number; picked?: string[]; missesOnly?: boolean } = {}) {
  const s = createStage({ reduced: o.reduced });
  s.read(t0);
  s.add(events, t0);
  const view = s.read(o.at ?? t0);
  const app = createSSRApp({ render: () => h(ActivityStage, { view, filter: o.filter, surfaces: o.picked, missesOnly: o.missesOnly }) });
  return (await renderToString(app)).replace(/<!--[\s\S]*?-->/g, '').replaceAll('&#39;', "'");
}

const imgOf = (html: string) => /<svg[^>]*role="img"[^>]*>[\s\S]*?<\/svg>/.exec(html)?.[0];

// #880: the stage is four columns, Project -> Surface -> goldie -> Served. Every node is an SVG <circle>
// with a data-node: `surface:<name>` for mcp, hook, cli and ui, `goldie`, `item:<Reference>` for each of
// the three most-served Items, `other` (its text "other Items & bundles") and `miss`. Each lane has one
// element with class `lane-path`, the path from the lane to goldie. A particle passes through the node
// of its surface at the middle of its first leg, and ends at the node of its Item, of other, or Miss.
const nodes = (html: string) => [...html.matchAll(/<circle[^>]*data-node="([^"]*)"[^>]*>/g)].map((m) => ({ id: m[1].replaceAll('&amp;', '&'), tag: m[0] }));
const attr = (tag: string, name: string) => Number.parseFloat(new RegExp(`\\b${name}="(-?[\\d.]+)`).exec(tag)?.[1] ?? 'NaN');
const node = (html: string, id: string) => nodes(html).find((n) => n.id === id);
const particles = (html: string) => [...html.matchAll(/<circle[^>]*class="[^"]*\bparticle\b[^"]*"[^>]*>/g)].map((m) => m[0]);
const surfaceNames = ['mcp', 'hook', 'cli', 'ui'];

describe('ActivityStage Surface column, Item nodes and lanes (#880)', () => {
  it('shows the four Surface nodes between the Projects and goldie, even before a request', async () => {
    const html = await render([]);
    const ids = nodes(html).map((n) => n.id);
    for (const s of surfaceNames) expect(ids).toContain(`surface:${s}`);
    const x = (id: string) => attr(node(html, id)!.tag, 'cx');
    for (const s of surfaceNames) {
      expect(x(`surface:${s}`)).toBeGreaterThan(8);
      expect(x(`surface:${s}`)).toBeLessThan(x('goldie'));
    }
    const svg = imgOf(html)!;
    for (const s of surfaceNames) expect(svg).toMatch(new RegExp(`<text[^>]*>${s}</text>`));
  });

  it('passes a particle through the node of its surface', async () => {
    for (const via of surfaceNames) {
      const html = await render([ev(1, { via })], { at: t0 + travelMs / 4 });
      const [p] = particles(html);
      const n = node(html, `surface:${via}`)!.tag;
      expect(Math.abs(attr(p, 'cx') - attr(n, 'cx'))).toBeLessThan(0.5);
      expect(Math.abs(attr(p, 'cy') - attr(n, 'cy'))).toBeLessThan(0.5);
    }
  });

  it('shows the three most-served Items, other Items & bundles, and Miss in the Served column', async () => {
    const served = (item: string, n: number) => Array.from({ length: n }, (_, i) => ev(100 + i, { served: item, query: `q-${item}-${i}` }));
    const html = await render([...served('skill:a', 4), ...served('skill:b', 3), ...served('skill:c', 2), ...served('skill:d', 1)]);
    const ids = nodes(html).map((n) => n.id);
    expect(ids.filter((i) => i.startsWith('item:')).sort()).toEqual(['item:skill:a', 'item:skill:b', 'item:skill:c']);
    expect(ids).toContain('other');
    expect(ids).toContain('miss');
    const svg = imgOf(html)!;
    expect(svg).toContain('skill:a');
    expect(svg).toContain('other Items &amp; bundles');
    expect(svg).toMatch(/<text[^>]*>Miss<\/text>/);
    expect(svg).not.toContain('skill:d');
  });

  it('ends a particle at the node of its Item, and a miss at Miss', async () => {
    const html = await render([ev(1, { served: 'skill:a' }), ev(2, { miss: true, served: undefined, query: 'nothing' })], { at: t0 + travelMs - 1 });
    const ps = particles(html);
    expect(ps).toHaveLength(2);
    const near = (p: string, id: string) => {
      const n = node(html, id)!.tag;
      return Math.abs(attr(p, 'cx') - attr(n, 'cx')) < 0.5 && Math.abs(attr(p, 'cy') - attr(n, 'cy')) < 1;
    };
    expect(ps.filter((p) => near(p, 'item:skill:a'))).toHaveLength(1);
    expect(ps.filter((p) => near(p, 'miss'))).toHaveLength(1);
  });

  it('connects every lane to goldie by a path', async () => {
    const html = await render([ev(1, { project: 'a/b/c' }), ev(2, { project: 'x/y/z' }), ev(3, { project: 'p/q/r' })]);
    expect(imgOf(html)!.match(/class="[^"]*\blane-path\b/g)).toHaveLength(3);
    expect(imgOf(await render([]))).not.toMatch(/\blane-path\b/);
  });

  it('keeps the nodes and the text alternatives under reduced motion, and renders a hostile Item as text', async () => {
    const html = await render([ev(1, { served: '<script>x</script>' })], { reduced: true });
    expect(nodes(html).map((n) => n.id)).toContain('surface:mcp');
    expect(html).toContain('role="img"');
    expect(html.match(/role="status"/g)).toHaveLength(2);
    expect(html).not.toMatch(/<script/);
    expect(html).toContain('&lt;script&gt;x&lt;/script&gt;');
  });
});

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
    const buttons = [...html.matchAll(/<button[^>]*>[\s\S]*?<\/button>/g)].map((m) => m[0]).filter((b) => /class="lane"/.test(b));
    expect(buttons).toHaveLength(2);
    for (const b of buttons) expect(b).toContain('type="button"');
    expect(buttons.find((b) => b.includes('a/b/c'))).toContain('aria-pressed="true"');
    expect(buttons.find((b) => b.includes('x/y/z'))).toContain('aria-pressed="false"');
    expect(imgOf(html)).not.toContain('<button');
  });

  it('shows the sampling chip text', async () => {
    expect(await render([ev(1)])).toContain('Showing every request');
  });

  it('makes the sampling chip a polite status of its own, beside the summary status', async () => {
    const html = await render([ev(1)]);
    expect(html).toMatch(/<p[^>]*role="status"[^>]*>Showing every request<\/p>/);
    expect(html.match(/role="status"/g)).toHaveLength(2);
    expect(html).not.toMatch(/aria-live="assertive"|role="alert"/);
    expect(imgOf(html)).not.toMatch(/role="status"/);
  });

  it('renders a hostile Project and query as text only', async () => {
    const html = await render([ev(1, { project: '<img src=x onerror=alert(1)>', miss: true, served: undefined, query: '<script>x</script>' })]);
    expect(html).not.toMatch(/<img|<script/);
    expect(html).toContain('&lt;img src=x onerror=alert(1)&gt;');
  });
});

describe('ActivityStage Served labels at 375 px (#880 fix round)', () => {
  it('cuts a long Item label in the SVG and keeps the full text in its title', async () => {
    const svg = imgOf(await render([ev(1, { served: `skill:${'x'.repeat(30)}` })]))!;
    expect(svg).toMatch(/<text[^>]*><title[^>]*>skill:x+<\/title>skill:xxx…[^<]*<\/text>/);
    expect(svg).not.toMatch(/<\/title>skill:x{20}/);
  });
});

// #881 (handoff §3.6 items 3 and 5): the nodes are real buttons outside the role="img" SVG, like the lanes.
// Props `surfaces` (the Surface pills picked) and `missesOnly` say what is on; the page asks by `data-node`
// on the button: `surface:<name>`, `item:<Reference>`, `miss`. A button is aria-pressed while its filter is on.
// A node glows (class `glow` on its <circle data-node>) as a particle passes it, and an Item node shows an
// aria-hidden "+1" as a particle arrives; both are decorative and there are none under prefers-reduced-motion.
const nodeButtons = (html: string) => [...html.matchAll(/<button[^>]*data-node="([^"]*)"[^>]*>[\s\S]*?<\/button>/g)].map((m) => ({ id: m[1].replaceAll('&amp;', '&'), html: m[0] }));
const nodeButton = (html: string, id: string) => nodeButtons(html).find((b) => b.id === id);
const glowing = (html: string) => nodes(html).filter((n) => /class="[^"]*\bglow\b/.test(n.tag)).map((n) => n.id);
const plusOnes = (html: string) => [...html.matchAll(/<text[^>]*>\+1<\/text>/g)].map((m) => m[0]);

describe('ActivityStage node buttons (#881)', () => {
  it('makes the Surface, Item and Miss nodes real buttons outside the role="img" SVG', async () => {
    const html = await render([ev(1, { served: 'skill:review' })]);
    const ids = nodeButtons(html).map((b) => b.id);
    for (const s of surfaceNames) expect(ids).toContain(`surface:${s}`);
    expect(ids).toContain('item:skill:review');
    expect(ids).toContain('miss');
    for (const b of nodeButtons(html)) expect(b.html).toContain('type="button"');
    expect(imgOf(html)).not.toContain('<button');
    expect(nodeButton(html, 'surface:mcp')!.html).toContain('mcp');
    expect(nodeButton(html, 'item:skill:review')!.html).toContain('skill:review');
    expect(nodeButton(html, 'miss')!.html).toContain('Miss');
    expect(nodeButtons(await render([])).map((b) => b.id)).toEqual(expect.arrayContaining(['surface:mcp', 'miss']));
  });

  it('marks a surface button and the Miss button pressed while their filter is on', async () => {
    const on = await render([ev(1)], { picked: ['mcp'], missesOnly: true });
    expect(nodeButton(on, 'surface:mcp')!.html).toContain('aria-pressed="true"');
    expect(nodeButton(on, 'surface:cli')!.html).toContain('aria-pressed="false"');
    expect(nodeButton(on, 'miss')!.html).toContain('aria-pressed="true"');
    const off = await render([ev(1)]);
    expect(nodeButton(off, 'surface:mcp')!.html).toContain('aria-pressed="false"');
    expect(nodeButton(off, 'miss')!.html).toContain('aria-pressed="false"');
  });

  it('renders a hostile Item on its button as text only', async () => {
    const html = await render([ev(1, { served: '<img src=x onerror=alert(1)>' })]);
    expect(html).not.toMatch(/<img/);
    expect(nodeButtons(html).some((b) => b.html.includes('&lt;img src=x onerror=alert(1)&gt;'))).toBe(true);
  });
});

describe('ActivityStage glow and +1 (#881)', () => {
  it('glows the node a particle is passing, and no node before one has started', async () => {
    const events = [ev(1, { via: 'cli', served: 'skill:review' })];
    expect(glowing(await render(events, { at: t0 }))).toEqual([]);
    expect(glowing(await render(events, { at: t0 + travelMs / 4 }))).toEqual(['surface:cli']);
    expect(glowing(await render(events, { at: t0 + travelMs / 2 }))).toEqual(['goldie']);
    expect(glowing(await render(events, { at: t0 + travelMs - 1 }))).toEqual(['item:skill:review']);
    expect(glowing(await render([ev(2, { miss: true, served: undefined })], { at: t0 + travelMs - 1 }))).toEqual(['miss']);
  });

  it('flashes an aria-hidden "+1" on an Item node as a particle arrives, and not for a miss or before it arrives', async () => {
    const served = [ev(1, { served: 'skill:review' })];
    expect(plusOnes(await render(served, { at: t0 + travelMs / 4 }))).toEqual([]);
    const arrived = await render(served, { at: t0 + travelMs - 1 });
    const flash = plusOnes(arrived);
    expect(flash).toHaveLength(1);
    expect(flash[0]).toContain('aria-hidden="true"');
    expect(imgOf(arrived)).toContain(flash[0]);
    expect(plusOnes(await render([ev(2, { miss: true, served: undefined })], { at: t0 + travelMs - 1 }))).toEqual([]);
  });

  it('shows neither under prefers-reduced-motion, though both show without it', async () => {
    const events = [ev(1, { served: 'skill:review' })];
    expect(glowing(await render(events, { at: t0 + travelMs - 1 })).length).toBeGreaterThan(0);
    expect(plusOnes(await render(events, { at: t0 + travelMs - 1 }))).toHaveLength(1);
    const calm = await render(events, { reduced: true, at: t0 + travelMs - 1 });
    expect(glowing(calm)).toEqual([]);
    expect(plusOnes(calm)).toEqual([]);
    expect(nodeButton(calm, 'surface:mcp')).toBeDefined();
  });
});
