// @vitest-environment ./src/activity/testdata/client-env.ts
// ActivityView's Pause (#874, handoff §9.1 2.2.2): Pause stops everything that updates on its own,
// not only the feed rows: the stage starts no particle, the tiles and the Miss board stay as they
// were, and the header says Paused. What arrives meanwhile is held and applied on Resume at once,
// counted and not animated (no burst of particles). The page is mounted with Vue's own renderer
// on a minimal in-memory tree (no DOM library); the stream is fed by hand.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createRenderer, nextTick } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { ApiError } from '../api/client';
import type { ActivityEvent, Frame } from './stream';

type Node = { tag: string; props: Record<string, unknown>; kids: Node[]; text?: string; parent?: Node };

let feed: (frames: Frame[]) => void = () => {};
// dropFirst makes the next stream end with an error that drop() raises (a dropped connection, #873).
let dropFirst = false;
let drop: () => void = () => {};
// refuse makes the next connect fail at once with this error (a 429 on connect, #877).
let refuse: Error | undefined;
vi.mock('./stream', () => ({
  streamActivity: (_signal: AbortSignal, on: (frames: Frame[]) => void) => {
    feed = on;
    if (refuse) {
      const e = refuse;
      refuse = undefined;
      return Promise.reject(e);
    }
    if (dropFirst) {
      dropFirst = false;
      return new Promise<void>((_, reject) => (drop = () => reject(new Error('network error'))));
    }
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
    text: () => squash(textOf(root)),
    busy: () => all(root, (n) => n.props['aria-busy'] === 'true' || n.props['aria-busy'] === true).length,
    button: (label: string) => all(root, (n) => n.tag === 'button' && textOf(n).trim() === label)[0],
    // The decorative dots in the header line and on the replica buttons (#877).
    headerDots: () =>
      all(all(root, (n) => n.tag === 'p' && n.props.class === 'label')[0], (n) =>
        String(n.props.class ?? '')
          .split(' ')
          .includes('dot'),
      ),
    replicaDots: (name: string) =>
      all(all(root, (n) => n.tag === 'button' && textOf(n).includes(name) && n.props['aria-pressed'] !== undefined)[0], (n) =>
        String(n.props.class ?? '')
          .split(' ')
          .includes('dot'),
      ),
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

describe('ActivityView Pause clock (#874)', () => {
  afterEach(() => vi.useRealTimers());

  it('keeps the tiles as they were however long the pause, and brings them up to date on Resume', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] });
    const { app, page } = await mount();
    await page.send([{ type: 'live' }, ...Array.from({ length: 10 }, (_, i) => ev(i + 1))]);
    expect(page.tiles()).toContain('Requests 2 per minute');

    await page.press('Pause');
    vi.advanceTimersByTime(6 * 60_000);
    await page.send([{ type: 'status', replica: 'goldie-1', up: true, lastSeen: '' }, ...Array.from({ length: 5 }, (_, i) => ev(20 + i))]);
    expect(page.tiles()).toContain('Requests 2 per minute');

    await page.press('Resume');
    expect(page.tiles()).toContain('Requests 1 per minute');
    app.unmount();
  });
});

describe('ActivityView reconnect while paused (#873)', () => {
  afterEach(() => vi.useRealTimers());

  it('keeps the tiles still during the pause and does not count the replayed history twice on Resume', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval', 'setTimeout', 'clearTimeout'] });
    dropFirst = true;
    const { app, page } = await mount();
    await page.send([{ type: 'live' }, ...Array.from({ length: 10 }, (_, i) => ev(i + 1))]);
    expect(page.tiles()).toContain('Requests 2 per minute');
    expect(page.lane()).toContain('10');

    await page.press('Pause');
    drop();
    await nextTick();
    await vi.advanceTimersByTimeAsync(2_000);
    const history = Array.from({ length: 25 }, (_, i) => ev(101 + i));
    await page.send([...history, { type: 'live' }, ev(200)]);
    expect(page.tiles()).toContain('Requests 2 per minute');

    await page.press('Resume');
    expect(page.tiles()).toContain('Requests 5 per minute');
    expect(page.lane()).toContain('11');
    expect(page.lane()).not.toContain('36');
    app.unmount();
  });
});

describe('ActivityView states (#877)', () => {
  afterEach(() => vi.useRealTimers());
  const timers = () =>
    vi.useFakeTimers({
      toFake: ['Date', 'setInterval', 'clearInterval', 'setTimeout', 'clearTimeout'],
    });

  it('counts a 429 down from Retry-After with Retry disabled, then says you can retry now', async () => {
    timers();
    refuse = new ApiError(429, 'Too many requests.', '', 5);
    const { app, page } = await mount();
    await vi.advanceTimersByTimeAsync(0);
    expect(page.text()).toContain('Too many requests — try again in 5 s.');
    expect(page.button('Retry').props.disabled).toBe(true);

    await vi.advanceTimersByTimeAsync(2_000);
    expect(page.text()).toContain('Too many requests — try again in 3 s.');
    expect(page.button('Retry').props.disabled).toBe(true);

    await vi.advanceTimersByTimeAsync(3_000);
    expect(page.text()).toContain('Too many requests — you can retry now.');
    expect(page.text()).not.toContain('try again in');
    expect(page.button('Retry').props.disabled).toBeFalsy();
    app.unmount();
  });

  it('shows tile and row skeletons only once the connect has taken longer than 300 ms, and drops them when live', async () => {
    timers();
    const { app, page } = await mount();
    await vi.advanceTimersByTimeAsync(299);
    expect(page.busy()).toBe(0);

    await vi.advanceTimersByTimeAsync(1);
    expect(page.busy()).toBeGreaterThanOrEqual(2);
    expect(page.tiles()).not.toContain('per minute');

    await page.send([{ type: 'live' }, ev(1)]);
    expect(page.busy()).toBe(0);
    expect(page.tiles()).toContain('per minute');
    app.unmount();
  });

  it('puts a decorative status dot next to Live, and a dot marked down on a replica that is not answering', async () => {
    const { app, page } = await mount();
    await page.send([
      { type: 'live' },
      ev(1),
      { type: 'status', replica: 'goldie-1', up: true, lastSeen: '' },
      {
        type: 'status',
        replica: 'goldie-2',
        up: false,
        lastSeen: new Date().toISOString(),
      },
    ]);
    expect(page.header()).toContain('Live');
    const dots = page.headerDots();
    expect(dots).toHaveLength(1);
    expect(dots[0].props['aria-hidden']).toBe('true');
    // The words carry the state: the dot adds no text.
    expect(textOf(dots[0])).toBe('');

    const up = page.replicaDots('goldie-1');
    const off = page.replicaDots('goldie-2');
    expect(up).toHaveLength(1);
    expect(off).toHaveLength(1);
    expect(off[0].props['aria-hidden']).toBe('true');
    expect(String(off[0].props.class).split(' ')).toContain('down');
    expect(String(up[0].props.class).split(' ')).not.toContain('down');
    app.unmount();
  });

  it('announces a 429 once: no alert region changes text while the countdown ticks', async () => {
    timers();
    refuse = new ApiError(429, 'Too many requests.', '', 3);
    const { app, page } = await mount();
    await vi.advanceTimersByTimeAsync(0);
    // The page has no root handle: climb to it from the Retry button.
    let root = page.button('Retry');
    while (root.parent) root = root.parent;
    const alerts = () =>
      all(root, (n) => n.props.role === 'alert')
        .map((n) => textOf(n))
        .join('|');
    const live = () =>
      all(root, (n) => n.props.role === 'status' && textOf(n).includes('Too many requests'))
        .map((n) => all(n, (k) => k.props['aria-hidden'] !== 'true' && k.tag === 'span').map((k) => textOf(k)).join(''))
        .join('|');
    const before = [alerts(), live()];
    await vi.advanceTimersByTimeAsync(2_000);
    expect([alerts(), live()]).toEqual(before);
    expect(alerts()).toBe('');
    expect(live()).toContain('Too many requests. Try again shortly.');
    app.unmount();
  });
});
