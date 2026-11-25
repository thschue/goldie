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
import source from './ActivityView.vue?raw';

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
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: ActivityView },
      { path: '/items/:kind/:name', component: { render: () => null } },
    ],
  });
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
  return { app, page, router };
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

describe('ActivityView 503 over a kept feed (#885)', () => {
  afterEach(() => vi.useRealTimers());

  // A live feed drops, and the reconnect is refused with a 503: the feed stays under the message.
  async function refusedOverKeptFeed() {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval', 'setTimeout', 'clearTimeout'] });
    dropFirst = true;
    const mounted = await mount();
    await mounted.page.send([{ type: 'live' }, ...Array.from({ length: 10 }, (_, i) => ev(i + 1))]);
    refuse = new ApiError(503, 'Unavailable.', 'Try again shortly.');
    drop();
    await nextTick();
    await vi.advanceTimersByTimeAsync(2_000);
    return mounted;
  }

  it('names a lost live connection, not a failed open, while the feed is still shown', async () => {
    const { app, page } = await refusedOverKeptFeed();
    expect(page.text()).toContain('q10');
    expect(page.text()).toContain('Lost the live connection.');
    expect(page.text()).not.toContain("Couldn't open the activity feed.");
    app.unmount();
  });

  it('keeps the feed visible when Retry asks again, until the new replay arrives', async () => {
    const { app, page } = await refusedOverKeptFeed();
    await page.press('Retry');
    expect(page.text()).toContain('q10');
    app.unmount();
  });
});

describe('ActivityView Miss detail and Miss board (#878)', () => {
  afterEach(() => vi.useRealTimers());
  // nearest is in the contract (ActivityEvent) but not yet in the generated types.
  const miss = (id: number, o: Record<string, unknown> = {}) =>
    ev(id, { miss: true, served: undefined, query: 'helm chart', ...o } as Partial<ActivityEvent>);
  const links = (root: Node) => all(root, (n) => n.tag === 'a' && typeof n.props.href === 'string');
  const detail = (root: Node) => all(root, (n) => n.props['aria-label'] === 'Event detail')[0];
  const rootOf = (n: Node) => {
    while (n.parent) n = n.parent;
    return n;
  };
  const click = async (b: Node) => {
    const h = b.props.onClick;
    for (const f of Array.isArray(h) ? h : [h]) (f as () => void)();
    await nextTick();
  };
  const rowButtons = (root: Node) => all(root, (n) => n.tag === 'button' && n.props.class === 'row');

  it('shows a selected miss in the Miss panel: "No Item matched", then each nearest keyword linking to the search', async () => {
    const { app, page } = await mount();
    await page.send([{ type: 'live' }, miss(1, { nearest: ['helm', 'chart'] })]);
    const root = rootOf(page.button('Pause'));
    await click(rowButtons(root)[0]);

    const panel = detail(root);
    expect(String(panel.props.class).split(' ')).toContain('miss');
    expect(squash(textOf(panel))).toMatch(/No Item matched\s*·?\s*helm\s*·?\s*chart/);
    const keywords = links(panel).filter((a) => ['helm', 'chart'].includes(squash(textOf(a))));
    expect(keywords.map((a) => a.props.href)).toEqual(['/?q=helm', '/?q=chart']);
    app.unmount();
  });

  it('leaves the nearest keywords out of the Miss panel when the event has none, and keeps a served event in the neutral panel', async () => {
    const { app, page } = await mount();
    await page.send([{ type: 'live' }, miss(1), ev(2)]);
    const root = rootOf(page.button('Pause'));
    const rows = rowButtons(root);

    // The feed is newest first: the served event is row 0, the miss row 1.
    await click(rows[1]);
    const panel = detail(root);
    expect(String(panel.props.class).split(' ')).toContain('miss');
    expect(squash(textOf(panel))).toContain('No Item matched');
    expect(squash(textOf(panel))).not.toMatch(/No Item matched\s*·\s*\S/);
    expect(links(panel).filter((a) => String(a.props.href).startsWith('/?q=') && squash(textOf(a)) !== 'Try this search')).toHaveLength(0);

    await click(rows[0]);
    const served = detail(root);
    expect(String(served.props.class ?? '').split(' ')).not.toContain('miss');
    expect(squash(textOf(served))).not.toContain('No Item matched');
    app.unmount();
  });

  it('words a Miss board card "last seen <relative time>" and ages it while the page is open', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval', 'setTimeout', 'clearTimeout'] });
    const { app, page } = await mount();
    const at = new Date(Date.now() - 2 * 60_000).toISOString();
    await page.send([{ type: 'live' }, miss(1, { time: at })]);
    expect(page.text()).toContain('last seen 2 minutes ago');

    vi.advanceTimersByTime(3 * 60_000);
    await nextTick();
    expect(page.text()).toContain('last seen 5 minutes ago');
    expect(page.text()).not.toContain('last seen 2 minutes ago');
    app.unmount();
  });
});

describe('ActivityView tiles and rows (#879)', () => {
  const classes = (n: Node) => String(n.props.class ?? '').split(' ');
  const rootOf = (n: Node) => {
    while (n.parent) n = n.parent;
    return n;
  };
  // A tile is a list item of the tiles section; its sub-line is the element classed `sub`.
  const tile = (root: Node, label: string) =>
    all(
      all(root, (n) => n.props['aria-label'] === 'Last five minutes')[0],
      (n) => n.tag === 'li' && textOf(n).includes(label),
    )[0];
  const sub = (t: Node) =>
    squash(
      all(t, (n) => classes(n).includes('sub'))
        .map(textOf)
        .join(' '),
    );
  const feedRows = (root: Node) => all(root, (n) => n.tag === 'tr' && n.parent?.tag === 'tbody');
  const rowOf = (root: Node, query: string) => feedRows(root).find((r) => textOf(r).includes(query))!;

  it('gives each tile a sub-line: per minute, of requests, and p95 under p50 for latency', async () => {
    const { app, page } = await mount();
    await page.send([{ type: 'live' }, ev(1, { latency_us: 500 }), ev(2, { latency_us: 1000 })]);
    const root = rootOf(page.button('Pause'));
    expect(sub(tile(root, 'Requests'))).toBe('per minute');
    expect(sub(tile(root, 'Misses'))).toBe('of requests');
    expect(sub(tile(root, 'Latency'))).toBe('p95 1.0 ms');
    expect(squash(textOf(tile(root, 'Latency')))).toMatch(/^Latency (p50 )?0\.5 ms p95 1\.0 ms$/);
    app.unmount();
  });

  it('turns the Misses tile attention (gold) only when misses are above 0 %', async () => {
    const { app, page } = await mount();
    await page.send([{ type: 'live' }, ev(1)]);
    const root = rootOf(page.button('Pause'));
    expect(page.tiles()).toContain('Misses 0 %');
    expect(classes(tile(root, 'Misses'))).not.toContain('attention');

    await page.send([ev(2, { miss: true, served: undefined, query: 'nothing here' })]);
    expect(page.tiles()).toContain('Misses 50 %');
    expect(classes(tile(root, 'Misses'))).toContain('attention');
    expect(classes(tile(root, 'Requests'))).not.toContain('attention');
    app.unmount();
  });

  it('reads a latency under 0.05 ms as "< 0.1 ms" and 0.05 ms as "0.1 ms"', async () => {
    const { app, page } = await mount();
    await page.send([{ type: 'live' }, ev(1, { latency_us: 49 })]);
    expect(page.tiles()).toContain('Latency < 0.1 ms');
    expect(page.tiles()).toContain('p95 < 0.1 ms');
    expect(page.tiles()).not.toContain('0.0 ms');

    await page.send([ev(2, { latency_us: 50 }), ev(3, { latency_us: 50 })]);
    expect(page.tiles()).toContain('Latency 0.1 ms');
    expect(page.tiles()).not.toContain('< 0.1 ms');
    app.unmount();
  });

  it('flashes a live row as fresh, and never a row replayed from history', async () => {
    const { app, page } = await mount();
    await page.send([ev(1, { query: 'old-one' }), ev(2, { query: 'old-two' }), { type: 'live' }, ev(3, { query: 'new-one' })]);
    const root = rootOf(page.button('Pause'));
    expect(classes(rowOf(root, 'new-one'))).toContain('fresh');
    expect(classes(rowOf(root, 'old-one'))).not.toContain('fresh');
    expect(classes(rowOf(root, 'old-two'))).not.toContain('fresh');
    app.unmount();
  });

  it('flashes nothing under prefers-reduced-motion', async () => {
    // The same live row flashes without the preference, so its absence below is the preference's doing.
    const motion = await mount();
    await motion.page.send([{ type: 'live' }, ev(1, { query: 'new-one' })]);
    expect(classes(rowOf(rootOf(motion.page.button('Pause')), 'new-one'))).toContain('fresh');
    motion.app.unmount();

    vi.stubGlobal('window', { matchMedia: (q: string) => ({ matches: q.includes('prefers-reduced-motion') }) });
    const { app, page } = await mount();
    await page.send([{ type: 'live' }, ev(1, { query: 'new-one' })]);
    const root = rootOf(page.button('Pause'));
    expect(feedRows(root)).toHaveLength(1);
    expect(classes(rowOf(root, 'new-one'))).not.toContain('fresh');
    app.unmount();
  });

  it('paints the fresh row with --row-flash and fades it in about one second', () => {
    expect(source).toMatch(/\.fresh[^{]*\{[^}]*var\(--row-flash\)/);
    expect(source).toMatch(/(?:transition|animation)[^;}]*\b1s\b/);
  });

  it('lights each live row for its own second: events 600 ms apart do not re-light the first row', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval', 'setTimeout', 'clearTimeout'] });
    try {
      const { app, page } = await mount();
      await page.send([{ type: 'live' }, ev(1, { query: 'first-one' })]);
      const root = rootOf(page.button('Pause'));
      await vi.advanceTimersByTimeAsync(600);
      await page.send([ev(2, { query: 'second-one' })]);
      await vi.advanceTimersByTimeAsync(600);
      expect(classes(rowOf(root, 'first-one'))).not.toContain('fresh');
      expect(classes(rowOf(root, 'second-one'))).toContain('fresh');
      app.unmount();
    } finally {
      vi.useRealTimers();
    }
  });
});

// #881: the stage's nodes act on the page. A surface node toggles its Surface pill, an Item node opens the
// Item's page, the Miss node toggles "Misses only"; the Miss board cards name their surfaces and carry pips.
describe('ActivityView stage node buttons (#881)', () => {
  const rootOf = (n: Node) => {
    while (n.parent) n = n.parent;
    return n;
  };
  const click = async (b: Node) => {
    const h = b.props.onClick;
    for (const f of Array.isArray(h) ? h : [h]) (f as () => void)();
    await nextTick();
  };
  const nodeBtn = (root: Node, id: string) => all(root, (n) => n.tag === 'button' && n.props['data-node'] === id)[0];
  const checkbox = (root: Node, label: string) =>
    all(root, (n) => n.tag === 'button' && n.props.role === 'checkbox' && squash(textOf(n)).includes(label))[0];
  const checked = (n: Node) => String(n.props['aria-checked']);

  it('toggles the Surface pill when a surface node is pressed, and the node follows the pill', async () => {
    const { app, page } = await mount();
    await page.send([{ type: 'live' }, ev(1, { via: 'mcp' }), ev(2, { via: 'cli' })]);
    const root = rootOf(page.button('Pause'));
    expect(checked(checkbox(root, 'MCP'))).toBe('false');

    await click(nodeBtn(root, 'surface:mcp'));
    expect(checked(checkbox(root, 'MCP'))).toBe('true');
    expect(checked(checkbox(root, 'CLI'))).toBe('false');
    expect(String(nodeBtn(root, 'surface:mcp').props['aria-pressed'])).toBe('true');
    expect(String(nodeBtn(root, 'surface:cli').props['aria-pressed'])).toBe('false');

    await click(nodeBtn(root, 'surface:mcp'));
    expect(checked(checkbox(root, 'MCP'))).toBe('false');
    expect(String(nodeBtn(root, 'surface:mcp').props['aria-pressed'])).toBe('false');
    app.unmount();
  });

  it('opens the Item page when an Item node is pressed, the Reference percent-encoded into one segment', async () => {
    const { app, page, router } = await mount();
    await page.send([{ type: 'live' }, ev(1, { served: 'skill:review' })]);
    await click(nodeBtn(rootOf(page.button('Pause')), 'item:skill:review'));
    await vi.waitFor(() => expect(router.currentRoute.value.fullPath).toBe('/items/skill/review'));
    app.unmount();

    const hostile = await mount();
    await hostile.page.send([{ type: 'live' }, ev(1, { served: 'article:a/../b?x#y' })]);
    await click(nodeBtn(rootOf(hostile.page.button('Pause')), 'item:article:a/../b?x#y'));
    await vi.waitFor(() => expect(hostile.router.currentRoute.value.path).toBe('/items/article/a%2F..%2Fb%3Fx%23y'));
    expect(hostile.router.currentRoute.value.hash).toBe('');
    hostile.app.unmount();
  });

  it('toggles "Misses only" when the Miss node is pressed, aria-checked following', async () => {
    const { app, page } = await mount();
    await page.send([{ type: 'live' }, ev(1), ev(2, { miss: true, served: undefined })]);
    const root = rootOf(page.button('Pause'));
    expect(checked(checkbox(root, 'Misses only'))).toBe('false');

    await click(nodeBtn(root, 'miss'));
    expect(checked(checkbox(root, 'Misses only'))).toBe('true');
    expect(String(nodeBtn(root, 'miss').props['aria-pressed'])).toBe('true');

    await click(nodeBtn(root, 'miss'));
    expect(checked(checkbox(root, 'Misses only'))).toBe('false');
    app.unmount();
  });
});

describe('ActivityView Miss board surfaces and pips (#881)', () => {
  const miss = (id: number, ago: number, o: Partial<ActivityEvent> = {}) =>
    ev(id, { miss: true, served: undefined, query: 'helm chart', time: new Date(Date.now() - ago * 60_000).toISOString(), ...o });
  const card = (root: Node, query: string) =>
    all(root, (n) => n.tag === 'button' && String(n.props.class).split(' ').includes('card') && textOf(n).includes(query))[0];
  const rootOf = (n: Node) => {
    while (n.parent) n = n.parent;
    return n;
  };
  const pips = (c: Node) => all(c, (n) => String(n.props.class ?? '').split(' ').includes('pip'));
  const opacity = (n: Node) => {
    const st = n.props.style;
    return Number.parseFloat(typeof st === 'string' ? (/opacity:\s*([\d.]+)/.exec(st)?.[1] ?? 'NaN') : String((st as { opacity?: number })?.opacity));
  };

  it('lists on each card the surfaces its misses came from', async () => {
    const { app, page } = await mount();
    await page.send([{ type: 'live' }, miss(1, 3, { via: 'mcp' }), miss(2, 2, { via: 'mcp' }), miss(3, 1, { via: 'cli' }), miss(4, 0, { query: 'other thing', via: 'ui' })]);
    const root = rootOf(page.button('Pause'));
    const text = squash(textOf(card(root, 'helm chart')));
    expect(text).toContain('MCP');
    expect(text).toContain('CLI');
    expect(text).not.toContain('UI');
    const other = squash(textOf(card(root, 'other thing')));
    expect(other).toContain('UI');
    expect(other).not.toContain('MCP');
    app.unmount();
  });

  it('shows up to five aria-hidden pips for recent occurrences, fading with age, the count in text', async () => {
    const { app, page } = await mount();
    await page.send([{ type: 'live' }, ...Array.from({ length: 7 }, (_, i) => miss(i + 1, 6 - i)), miss(20, 0, { query: 'twice' }), miss(21, 1, { query: 'twice' })]);
    const root = rootOf(page.button('Pause'));
    const busy = card(root, 'helm chart');
    const p = pips(busy);
    expect(p).toHaveLength(5);
    for (const n of p) expect(String(n.props['aria-hidden'])).toBe('true');
    expect(squash(textOf(busy))).toContain('×7');
    // Newest first: each pip is no stronger than the one before, and the oldest is fainter than the newest.
    const o = p.map(opacity);
    for (const v of o) expect(Number.isNaN(v)).toBe(false);
    for (let i = 1; i < o.length; i++) expect(o[i]).toBeLessThanOrEqual(o[i - 1]);
    expect(o[o.length - 1]).toBeLessThan(o[0]);

    const few = card(root, 'twice');
    expect(pips(few)).toHaveLength(2);
    expect(squash(textOf(few))).toContain('×2');
    app.unmount();
  });
});
