// The Live stream stage (#811, handoff §3.6): Project -> goldie -> Served, a particle per request.
// The model is pure and driven by a clock the caller passes in (epoch ms), as tiles.ts is. Pinned here:
// - createStage({ reduced? }) returns { add(events, now), setHidden(hidden, now), read(now) };
//   `reduced` is prefers-reduced-motion;
// - maxParticles is the cap on particles alive at once; travelMs is how long one request takes, from
//   its Project to goldie (leg 'in', the first half) and on to the Item or the miss marker (leg
//   'out', the second half); a particle is gone from `travelMs` after it arrived;
// - read(now) is { particles, lanes, seen, missed, chip, summary }. A particle is { project,
//   surface, target: 'served' | 'miss', label, leg, progress } with progress 0..1 within its leg;
//   surface is `via` when it is mcp, hook, cli or ui (#880: the four Surface nodes), else 'other'; label is the served Reference, or
//   for a miss the query. Lanes are { project, count }, one per Project asking, busiest first;
// - seen and missed count every event, whatever the stage shows; chip is the sampling note;
//   summary is the text for a screen reader and changes at most once per summaryEvery;
// - labelText(s) is text only: invisible and bidirectional controls removed, cut to at most
//   maxLabel code points, the last of them "…" when it was cut.
import { describe, expect, it } from 'vitest';
import { add, maxRows, missBoard } from './rows';
import type { ActivityEvent } from './stream';
import { createStage, labelText, maxLabel, maxParticles, summaryEvery, travelMs } from './stage';

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
  ...o,
});
const many = (n: number, o: Partial<ActivityEvent> = {}) => Array.from({ length: n }, (_, i) => ev(i + 1, o));

describe('stage particles', () => {
  it('is a bounded pool and needs no more than the page can draw', () => {
    expect(maxParticles).toBeGreaterThan(0);
    expect(maxParticles).toBeLessThanOrEqual(200);
    expect(travelMs).toBeGreaterThan(0);
  });

  it('sends a request from its Project to goldie, then to the served Item, coloured by surface', () => {
    const s = createStage();
    s.add([ev(1, { via: 'cli' })], t0);
    const start = s.read(t0).particles;
    expect(start).toHaveLength(1);
    expect(start[0]).toMatchObject({ project: 'github.com/acme/shop', surface: 'cli', target: 'served', label: 'skill:review', leg: 'in', progress: 0 });
    const early = s.read(t0 + travelMs / 4).particles[0];
    expect(early.leg).toBe('in');
    expect(early.progress).toBeGreaterThan(0);
    expect(early.progress).toBeLessThan(1);
    const late = s.read(t0 + (travelMs * 3) / 4).particles[0];
    expect(late).toMatchObject({ leg: 'out', target: 'served' });
    expect(s.read(t0 + travelMs).particles).toEqual([]);
  });

  it('ends a miss at a miss marker, and reads an unknown or absent surface as other', () => {
    const s = createStage();
    s.add([ev(1, { miss: true, served: undefined, via: 'mcp' }), ev(2, { via: 'sidecar' }), ev(3, { via: 'ui' }), ev(4)], t0);
    const p = s.read(t0 + (travelMs * 3) / 4).particles;
    expect(p.map((x) => x.surface).sort()).toEqual(['mcp', 'other', 'other', 'ui']);
    expect(p.find((x) => x.surface === 'mcp')).toMatchObject({ target: 'miss', label: 'q1' });
    expect(p.filter((x) => x.target === 'served')).toHaveLength(3);
  });

  it('lists the Projects asking as lanes, busiest first', () => {
    const s = createStage();
    s.add([ev(1, { project: 'a/b/c' }), ev(2, { project: 'x/y/z' }), ev(3, { project: 'x/y/z' })], t0);
    expect(s.read(t0).lanes).toEqual([
      { project: 'x/y/z', count: 2 },
      { project: 'a/b/c', count: 1 },
    ]);
  });
});

// #880: view.items is the three most-served Items since the stage was created, { item, count }, most
// served first, the Item cut by labelText; view.otherItems counts what the rest were served; a miss is
// no Item. A particle's `end` is 'item' when its Item is among those three, 'other' when it is not, and
// 'miss' for a miss. Counting goes on past the pool, a Pause (count) and reduced motion.
describe('stage Surface and Item nodes (#880)', () => {
  const served = (item: string, n: number, o: Partial<ActivityEvent> = {}) =>
    Array.from({ length: n }, (_, i) => ev(1000 + i, { served: item, query: `q-${item}-${i}`, ...o }));

  it('ranks the three most-served Items since the page opened, with the rest as one count', () => {
    const s = createStage();
    s.add([...served('skill:a', 5), ...served('skill:b', 4), ...served('skill:c', 3), ...served('skill:d', 2), ...served('skill:e', 1)], t0);
    const v = s.read(t0);
    expect(v.items).toEqual([
      { item: 'skill:a', count: 5 },
      { item: 'skill:b', count: 4 },
      { item: 'skill:c', count: 3 },
    ]);
    expect(v.otherItems).toBe(3);
  });

  it('keeps counting through a Pause and reduced motion, and counts no miss as an Item', () => {
    const s = createStage({ reduced: true });
    s.add([...served('skill:a', 2), ...served('skill:b', 1), ev(50, { miss: true, served: undefined, query: 'nothing' })], t0);
    s.count(served('skill:b', 3));
    const v = s.read(t0);
    expect(v.particles).toEqual([]);
    expect(v.items).toEqual([
      { item: 'skill:b', count: 4 },
      { item: 'skill:a', count: 2 },
    ]);
    expect(v.otherItems).toBe(0);
  });

  it('is empty before any request is served', () => {
    const v = createStage().read(t0);
    expect(v.items).toEqual([]);
    expect(v.otherItems).toBe(0);
  });

  it('ends a particle at its Item node, at other Items & bundles, or at Miss', () => {
    const s = createStage();
    s.add(
      [...served('skill:a', 3), ...served('skill:b', 3), ...served('skill:c', 3), ...served('skill:rare', 1), ev(60, { miss: true, served: undefined, query: 'nothing' })],
      t0,
    );
    const v = s.read(t0 + (travelMs * 3) / 4);
    const top = v.items.map((i) => i.item);
    expect(top).toHaveLength(3);
    expect(top).not.toContain('skill:rare');
    const ends = (label: string) => new Set(v.particles.filter((p) => p.label === label).map((p) => p.end));
    for (const item of top) expect(ends(item)).toEqual(new Set(['item']));
    expect(ends('skill:rare')).toEqual(new Set(['other']));
    expect(ends('nothing')).toEqual(new Set(['miss']));
  });

  it('cuts an Item label like any other label', () => {
    const s = createStage();
    s.add([ev(1, { served: `<img src=x onerror=alert(1)>${'x'.repeat(500)}` })], t0);
    const [item] = s.read(t0).items;
    expect([...item.item].length).toBeLessThanOrEqual(maxLabel);
  });
});

describe('stage render budget', () => {
  it('shows every request below the cap', () => {
    const s = createStage();
    s.add(many(3), t0);
    const v = s.read(t0);
    expect(v.particles).toHaveLength(3);
    expect(v.chip).toBe('Showing every request');
  });

  it('samples above the cap and says so, while the counters, the feed and the Miss board still see every event', () => {
    const n = maxParticles + 50;
    expect(n).toBeLessThanOrEqual(maxRows);
    const s = createStage();
    const events = many(n).map((e, i) => (i % 2 ? { ...e, miss: true, served: undefined, query: `gap${i % 3}` } : e));
    s.add(events, t0);
    const v = s.read(t0);
    expect(v.particles.length).toBeLessThanOrEqual(maxParticles);
    expect(v.particles.length).toBeGreaterThan(0);
    const m = /^Showing 1 in (\d+) requests · the feed and Miss board still get every event$/.exec(v.chip);
    expect(m).not.toBeNull();
    expect(Number(m![1])).toBeGreaterThanOrEqual(2);
    expect(v.seen).toBe(n);
    expect(v.missed).toBe(events.filter((e) => e.miss).length);
    const feed = add([], events);
    expect(feed).toHaveLength(n);
    expect(missBoard(feed).reduce((sum, g) => sum + g.count, 0)).toBe(v.missed);
  });
});

describe('stage sampling chip', () => {
  it('changes at most once per second, so the polite status it is does not chatter (A11)', () => {
    const s = createStage();
    expect(s.read(t0).chip).toBe('Showing every request');
    s.add(many(maxParticles + 50), t0 + 200);
    expect(s.read(t0 + 200).chip).toBe('Showing every request');
    expect(s.read(t0 + 999).chip).toBe('Showing every request');
    expect(s.read(t0 + 1000).chip).toMatch(/^Showing 1 in \d+ requests/);
    // Back to every request once the sampled burst has flown, again not within a second of the last change.
    const sampled = s.read(t0 + 1000).chip;
    expect(s.read(t0 + 1500).chip).toBe(sampled);
    expect(s.read(t0 + 200 + travelMs + 1000).chip).toBe('Showing every request');
  });
});

describe('stage visibility', () => {
  it('drops the queue while the tab is hidden, so returning causes no burst, yet still counts every event', () => {
    const s = createStage();
    s.add(many(5), t0);
    s.setHidden(true, t0 + 10);
    expect(s.read(t0 + 20).particles).toEqual([]);
    s.add(many(5), t0 + 30);
    expect(s.read(t0 + 40).particles).toEqual([]);
    s.setHidden(false, t0 + 50);
    expect(s.read(t0 + 60).particles).toEqual([]);
    expect(s.read(t0 + 60).seen).toBe(10);
    s.add(many(2), t0 + 70);
    expect(s.read(t0 + 70).particles).toHaveLength(2);
  });
});

describe('stage reduced motion', () => {
  it('moves no particle, yet the counters and the summary still update', () => {
    const s = createStage({ reduced: true });
    const before = s.read(t0).summary;
    s.add([...many(3), ev(4, { miss: true, served: undefined })], t0 + 1000);
    const v = s.read(t0 + 1000);
    expect(v.particles).toEqual([]);
    expect(v.seen).toBe(4);
    expect(v.missed).toBe(1);
    expect(s.read(t0 + summaryEvery).summary).not.toBe(before);
    expect(s.read(t0 + summaryEvery).summary).toContain('4 requests');
  });
});

describe('stage summary', () => {
  it('changes at most once per summaryEvery, and then tells the counts', () => {
    expect(summaryEvery).toBe(30_000);
    const s = createStage();
    const first = s.read(t0).summary;
    s.add([...many(3), ev(4, { miss: true, served: undefined }), ev(5, { miss: true, served: undefined })], t0 + 1000);
    expect(s.read(t0 + 10_000).summary).toBe(first);
    s.add(many(2), t0 + 20_000);
    expect(s.read(t0 + 29_000).summary).toBe(first);
    const later = s.read(t0 + summaryEvery).summary;
    expect(later).not.toBe(first);
    expect(later).toContain('7 requests');
    expect(later).toContain('2 misses');
  });
});

describe('labelText', () => {
  it('leaves a short label alone and removes invisible and bidirectional controls', () => {
    expect(labelText('skill:review')).toBe('skill:review');
    expect(labelText('a‮b​c⁦d')).toBe('abcd');
  });

  it('cuts by code point, never through a surrogate pair, and marks the cut', () => {
    const long = '\u{1F600}'.repeat(maxLabel + 10);
    const out = labelText(long);
    expect([...out]).toHaveLength(maxLabel);
    expect(out.endsWith('…')).toBe(true);
    expect(out.isWellFormed()).toBe(true);
    expect(labelText('a'.repeat(maxLabel))).toBe('a'.repeat(maxLabel));
  });

  it('carries a hostile query into the particle only as cut text', () => {
    const s = createStage();
    s.add([ev(1, { miss: true, served: undefined, query: `<img src=x onerror=alert(1)>${'x'.repeat(500)}` })], t0);
    const label = s.read(t0).particles[0].label;
    expect([...label].length).toBeLessThanOrEqual(maxLabel);
  });
});

describe('stage Items in the summary and unnamed served events (#880 fix round)', () => {
  it('names the most-served Items in the summary', () => {
    const s = createStage();
    s.add([ev(1, { served: 'skill:a' }), ev(2, { served: 'skill:a' }), ev(3, { served: 'skill:b' })], t0);
    expect(s.read(t0).summary).toContain('Most served: skill:a (2), skill:b (1).');
  });

  it('counts a served event without an Item under other Items & bundles, as its particle ends there', () => {
    const s = createStage();
    s.add([ev(1, { served: undefined, query: 'no-item' })], t0);
    const v = s.read(t0 + 1);
    expect(v.particles[0].end).toBe('other');
    expect(v.otherItems).toBe(1);
  });
});
