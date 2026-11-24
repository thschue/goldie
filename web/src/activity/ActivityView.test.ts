// @vitest-environment ./src/activity/testdata/client-env.ts
// ActivityView's Pause (#874, handoff §9.1 2.2.2): Pause stops everything that updates on its own,
// not only the feed rows: the stage starts no particle, the tiles and the Miss board stay as they
// were, and the header says Paused. What arrives meanwhile is held and applied on Resume at once,
// counted and not animated (no burst of particles). The page is mounted with Vue's own renderer
// on a minimal in-memory tree (no DOM library); the stream is fed by hand.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createRenderer, nextTick } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import type { ActivityEvent, Frame } from './stream';

type Node = { tag: string; props: Record<string, unknown>; kids: Node[]; text?: string; parent?: Node };

let feed: (frames: Frame[]) => void = () => {};
vi.mock('./stream', () => ({
  streamActivity: (_signal: AbortSignal, on: (frames: Frame[]) => void) => {
    feed = on;
    return new Promise<void>(() => {});
  },
}));

const ops = {
  createElement: (tag: string): Node => ({ tag, props: {}, kids: [] }),
  createText: (text: string): Node => ({ tag: '#text', props: {}, kids: [], text }),
  createComment: (text: string): Node => ({ tag: '#comment', props: {}, kids: [], text }),
  setText: (n: Node, text: string) => void (n.text = text),
  setElementText: (n: Node, text: string) => void (n.kids = [{ tag: '#text', props: {}, kids: [], text, parent: n }]),
  patchProp: (n: Node, key: string, _old: unknown, value: unknown) => void (n.props[key] = value),
  insert: (n: Node, parent: Node, anchor?: Node | null) => {
    ops.remove(n);
    n.parent = parent;
    const at = anchor ? parent.kids.indexOf(anchor) : -1;
    if (at < 0) parent.kids.push(n);
    else parent.kids.splice(at, 0, n);
  },
  remove: (n: Node) => {
    if (n.parent) n.parent.kids = n.parent.kids.filter((k) => k !== n);
    n.parent = undefined;
  },
  // Static (hoisted) markup is kept as one node of raw HTML; textOf strips its tags.
  insertStaticContent: (html: string, parent: Node, anchor?: Node | null): [Node, Node] => {
    const n = ops.createText(html);
    n.props.html = true;
    ops.insert(n, parent, anchor);
    return [n, n];
  },
  parentNode: (n: Node) => n.parent ?? null,
  nextSibling: (n: Node) => n.parent?.kids[n.parent.kids.indexOf(n) + 1] ?? null,
};

const textOf = (n: Node): string =>
  n.tag === '#text' ? (n.props.html ? (n.text ?? '').replace(/<[^>]*>/g, '') : (n.text ?? '')) : n.kids.map(textOf).join('');
const squash = (s: string) => s.replace(/\s+/g, ' ').trim();
const all = (n: Node, hit: (n: Node) => boolean): Node[] => [...(hit(n) ? [n] : []), ...n.kids.flatMap((k) => all(k, hit))];

async function mount() {
  const { default: ActivityView } = await import('./ActivityView.vue');
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: ActivityView }] });
  await router.push('/');
  await router.isReady();
  const root: Node = ops.createElement('root');
  const app = createRenderer<Node, Node>(ops).createApp(ActivityView);
  app.use(router);
  app.mount(root);
  await nextTick();
  const page = {
    header: () => squash(textOf(all(root, (n) => n.tag === 'p' && n.props.class === 'label')[0])),
    tiles: () => squash(textOf(all(root, (n) => n.props['aria-label'] === 'Last five minutes')[0])),
    particles: () => all(root, (n) => n.tag === 'circle' && String(n.props.class).includes('particle')).length,
    lane: () => squash(textOf(all(root, (n) => n.tag === 'button' && n.props.class === 'lane')[0])),
    board: () => all(root, (n) => n.props['aria-labelledby'] === 'board-h').length,
    press: async (label: string) => {
      const b = all(root, (n) => n.tag === 'button' && textOf(n).trim() === label)[0];
      const h = b.props.onClick;
      for (const f of Array.isArray(h) ? h : [h]) (f as () => void)();
      await nextTick();
    },
    send: async (frames: Frame[]) => {
      feed(frames);
      await nextTick();
    },
  };
  return { app, page };
}

const ev = (id: number, o: Partial<ActivityEvent> = {}): Frame => ({
  type: 'event',
  event: {
    id,
    time: new Date().toISOString(),
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
  },
});

beforeEach(() => {
  vi.stubGlobal('window', { matchMedia: () => ({ matches: false }) });
  vi.stubGlobal('document', { hidden: false, addEventListener() {}, removeEventListener() {} });
  vi.stubGlobal('requestAnimationFrame', () => 0);
  vi.stubGlobal('cancelAnimationFrame', () => {});
});
afterEach(() => vi.unstubAllGlobals());

describe('ActivityView Pause', () => {
  it('holds the stage, the tiles and the Miss board while paused, and applies the held events on Resume without a burst', async () => {
    const { app, page } = await mount();
    await page.send([{ type: 'live' }, ev(1)]);
    expect(page.particles()).toBe(1);
    expect(page.tiles()).toContain('Requests 0 per minute');

    await page.press('Pause');
    const held = [
      ...Array.from({ length: 9 }, (_, i) => ev(10 + i)),
      ...Array.from({ length: 5 }, (_, i) => ev(30 + i, { miss: true, query: 'nothing here', served: undefined })),
    ];
    await page.send(held);

    expect(page.particles()).toBe(1);
    expect(page.tiles()).toContain('Requests 0 per minute');
    expect(page.tiles()).toContain('Misses 0 %');
    expect(page.board()).toBe(0);

    await page.press('Resume');
    expect(page.particles()).toBe(1);
    expect(page.tiles()).toContain('Requests 3 per minute');
    expect(page.tiles()).toContain('Misses 33 %');
    expect(page.board()).toBe(1);
    expect(page.lane()).toContain('15');
    app.unmount();
  });

  it('reads Paused in the header while paused, and Live again after Resume', async () => {
    const { app, page } = await mount();
    await page.send([{ type: 'live' }, ev(1)]);
    expect(page.header()).toContain('Live');

    await page.press('Pause');
    expect(page.header()).toContain('Paused');
    expect(page.header()).not.toContain('Live');

    await page.press('Resume');
    expect(page.header()).toContain('Live');
    expect(page.header()).not.toContain('Paused');
    app.unmount();
  });
});
