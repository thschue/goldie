// The last five minutes and the live header (#809, handoff §3.6). The tiles are computed from time
// buckets keyed by the event's own `time`, never from the 500 rows of the feed. Pinned here:
// - createTiles() returns { add(events, now), read(now) }; now is epoch milliseconds;
// - read(now) is { perMinute, missPercent, projects }: events of the last five minutes per
//   minute, the share of misses in whole percent (undefined with no request), and the distinct
//   Projects asking (an event without a Project asks for none);
// - maxProjects bounds the distinct Projects tallied;
// - liveHeader(heard, now) takes the replicas seen and when each was last heard (epoch ms, from an
//   event or a status) and words the header: "Live" for one replica, and for several
//   "Live · merged from all replicas (n of m answering)", n being those heard in the last minute.
import { describe, expect, it } from 'vitest';
import type { ActivityEvent } from './stream';
import { createTiles, liveHeader, maxProjects } from './tiles';

const now = Date.parse('2026-11-24T10:00:00Z');
const min = 60_000;

const ev = (id: number, o: Partial<ActivityEvent> = {}): ActivityEvent => ({
  id,
  time: new Date(now).toISOString(),
  kind: 'search',
  query: 'q',
  project: '',
  token: 'ci',
  client_version: '',
  trace_id: '',
  miss: false,
  replica: 'goldie-0',
  late: false,
  ...o,
});
const at = (ms: number, o: Partial<ActivityEvent> = {}, id = 1) => ev(id, { time: new Date(now + ms).toISOString(), ...o });

describe('createTiles', () => {
  it('reads requests per minute, miss percent and distinct projects of the last five minutes', () => {
    const t = createTiles();
    t.add(
      [
        ev(1, { project: 'a/b/c', miss: true }),
        ev(2, { project: 'a/b/c' }),
        ev(3, { project: 'a/b/d' }),
        ev(4, { project: '' }),
        at(-4 * min, { project: 'a/b/e' }, 5),
      ],
      now,
    );
    const r = t.read(now);
    expect(r.perMinute).toBeCloseTo(1, 5);
    expect(r.missPercent).toBe(20);
    expect(r.projects).toBe(3);
  });

  it('reads no miss percent when nothing was asked', () => {
    expect(createTiles().read(now)).toMatchObject({ perMinute: 0, missPercent: undefined, projects: 0 });
  });

  it('counts every event in the window, not the newest 500', () => {
    const t = createTiles();
    t.add(
      Array.from({ length: 1500 }, (_, i) => ev(i + 1)),
      now,
    );
    expect(t.read(now).perMinute).toBeCloseTo(300, 5);
  });

  it('never counts replayed history older than five minutes', () => {
    const t = createTiles();
    t.add([at(-6 * min, { project: 'a/b/old', miss: true }), at(-3600 * 1000, { project: 'a/b/older' }, 2)], now);
    expect(t.read(now)).toMatchObject({ perMinute: 0, missPercent: undefined, projects: 0 });
  });

  it('ages an event out by its own time, not by when it arrived', () => {
    const t = createTiles();
    t.add([ev(1, { project: 'a/b/c' })], now);
    expect(t.read(now + 4 * min).projects).toBe(1);
    expect(t.read(now + 6 * min)).toMatchObject({ perMinute: 0, projects: 0 });
  });

  it('counts an event stamped in the future as now, so it ages out five minutes after now', () => {
    const t = createTiles();
    t.add([at(3600 * 1000, { project: 'a/b/skew' })], now);
    expect(t.read(now).perMinute).toBeCloseTo(0.2, 5);
    expect(t.read(now).projects).toBe(1);
    expect(t.read(now + 6 * min)).toMatchObject({ perMinute: 0, projects: 0 });
  });

  it('bounds the project tally against a flood of distinct projects', () => {
    const t = createTiles();
    t.add(
      Array.from({ length: 20_000 }, (_, i) => ev(i + 1, { project: `a/b/p${i}` })),
      now,
    );
    expect(maxProjects).toBeLessThan(20_000);
    expect(t.read(now).projects).toBeLessThanOrEqual(maxProjects);
  });
});

describe('liveHeader', () => {
  it('reads just Live for one replica, whenever it was last heard', () => {
    expect(liveHeader(new Map([['goldie-0', now - 10 * min]]), now)).toBe('Live');
    expect(liveHeader(new Map(), now)).toBe('Live');
  });

  it('counts the replicas heard in the last minute against the replicas seen', () => {
    const heard = new Map([
      ['goldie-0', now - 10_000],
      ['goldie-1', now - 3 * min],
      ['goldie-2', now],
    ]);
    expect(liveHeader(heard, now)).toBe('Live · merged from all replicas (2 of 3 answering)');
    const quiet = new Map([
      ['a', now - 5 * min],
      ['b', now - 5 * min],
    ]);
    expect(liveHeader(quiet, now)).toBe('Live · merged from all replicas (0 of 2 answering)');
  });
});
