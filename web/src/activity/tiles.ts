// The last five minutes and the live header of the Activity page (#809, handoff §3.6). The tiles
// come from per-minute buckets keyed by the event's own `time`, so they count every event of the
// window, not the 500 rows the feed keeps, and an event replayed from history ages out by when it
// happened, not when it arrived. Nothing here is a time series: the buckets are the live window.
import { surfaceOf, surfaces, type Surface } from './rows';
import type { ActivityEvent } from './stream';

const minute = 60_000;
const buckets = 5;

// The distinct Projects tallied at most; a flood of distinct ones cannot grow the map.
export const maxProjects = 1000;

// The latencies kept per minute; a flood past this is not sampled, so memory stays bounded.
export const maxLatencies = 20_000;

export type Tiles = {
  perMinute: number;
  missPercent: number | undefined;
  projects: number;
  busiest: Surface | undefined;
  latency: { p50: number; p95: number } | undefined; // microseconds
};

type Bucket = { n: number; miss: number; via: Partial<Record<Surface, number>>; us: number[] };

// The nearest-rank percentile of values sorted ascending.
const rank = (sorted: number[], p: number) => sorted[Math.max(0, Math.ceil(p * sorted.length) - 1)]!;

export function createTiles() {
  const counts = new Map<number, Bucket>();
  const projects = new Map<string, number>(); // Project -> newest bucket it was asked in

  const first = (now: number) => Math.floor(now / minute) - (buckets - 1);
  const prune = (now: number) => {
    const from = first(now);
    for (const [b] of counts) if (b < from) counts.delete(b);
    for (const [p, b] of projects) if (b < from) projects.delete(p);
  };

  return {
    // add tallies events (any order) as of now. An event with a time that cannot be read, or older
    // than the window, is not counted; one stamped ahead of now counts as now.
    add(events: ActivityEvent[], now: number) {
      prune(now);
      const from = first(now);
      for (const e of events) {
        const t = Date.parse(e.time);
        if (Number.isNaN(t)) continue;
        const b = Math.floor(Math.min(t, now) / minute);
        if (b < from) continue;
        const c = counts.get(b) ?? { n: 0, miss: 0, via: {}, us: [] };
        c.n++;
        if (e.miss) c.miss++;
        const s = surfaceOf(e);
        if (s) c.via[s] = (c.via[s] ?? 0) + 1;
        if (e.latency_us && e.latency_us > 0 && c.us.length < maxLatencies) c.us.push(e.latency_us);
        counts.set(b, c);
        if (!e.project) continue;
        if (projects.has(e.project) || projects.size < maxProjects) projects.set(e.project, Math.max(b, projects.get(e.project) ?? b));
      }
    },
    read(now: number): Tiles {
      prune(now);
      let n = 0;
      let miss = 0;
      const via: Partial<Record<Surface, number>> = {};
      const us: number[] = [];
      for (const c of counts.values()) {
        n += c.n;
        miss += c.miss;
        for (const s of surfaces) if (c.via[s]) via[s] = (via[s] ?? 0) + c.via[s]!;
        for (const v of c.us) us.push(v);
      }
      let busiest: Surface | undefined;
      for (const s of surfaces) if (via[s] && (!busiest || via[s]! > via[busiest]!)) busiest = s;
      us.sort((a, b) => a - b);
      return {
        perMinute: n / buckets,
        missPercent: n ? Math.round((100 * miss) / n) : undefined,
        projects: projects.size,
        busiest,
        latency: us.length ? { p50: rank(us, 0.5), p95: rank(us, 0.95) } : undefined,
      };
    },
  };
}

// liveHeader words the header over the feed. heard maps each replica seen to when it was last
// heard (epoch ms). One replica is "Live"; several say how many answered in the last minute.
export function liveHeader(heard: Map<string, number>, now: number): string {
  if (heard.size <= 1) return 'Live';
  let n = 0;
  for (const t of heard.values()) if (now - t < minute) n++;
  return `Live · merged from all replicas (${n} of ${heard.size} answering)`;
}
