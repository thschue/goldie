// The model of the Live stream stage (#811, handoff §3.6, Q53): Project -> Surface -> goldie -> Served (#880), a
// particle per request. It is pure and driven by a clock the caller passes in (epoch ms), as
// tiles.ts is. The pool is capped: above the cap the stage samples and says so, while the counters
// still see every event. While the tab is hidden nothing is queued; under reduced motion no
// particle exists, yet the counters and the summary go on.
import type { ActivityEvent } from './stream';

// The particles alive at once; about 30 a second over travelMs.
export const maxParticles = 100;
// One request, from its Project to goldie (the first half) and on to the Item or the miss marker.
export const travelMs = 3600;
// The text for a screen reader changes at most this often.
export const summaryEvery = 30_000;
// The sampling chip, a polite status too, changes at most this often (A11).
export const chipEvery = 1000;
// A label is cut to this many code points, the last being "…".
export const maxLabel = 40;
// The lanes read; the Projects tallied are capped so a flood of distinct ones cannot grow the map.
export const maxLanes = 6;
const maxTallied = 1000;

// The four Surface nodes (#880); any other `via` reads as 'other' and has no node.
export const surfaces = ['mcp', 'hook', 'cli', 'ui'] as const;
// The Items the Served column names; the rest are one node.
export const maxItems = 3;
export type Surface = (typeof surfaces)[number] | 'other';
export type Particle = {
  project: string;
  surface: Surface;
  target: 'served' | 'miss';
  // Where the particle ends: its Item's node, "other Items & bundles", or Miss.
  end: 'item' | 'other' | 'miss';
  label: string;
  leg: 'in' | 'out';
  progress: number;
};
export type StageView = {
  particles: Particle[];
  lanes: { project: string; count: number }[];
  items: { item: string; count: number }[];
  otherItems: number;
  seen: number;
  missed: number;
  chip: string;
  summary: string;
};

// Invisible and bidirectional controls would let a label reorder or hide the text around it; a lone
// surrogate (\p{Cs}) would make it ill-formed.
const hidden = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}\p{Cs}]/gu;

// labelText makes untrusted text a short, plain label: controls removed, cut by code point.
export function labelText(s: string): string {
  const chars = [...s.replace(hidden, '')];
  return chars.length <= maxLabel ? chars.join('') : `${chars.slice(0, maxLabel - 1).join('')}…`;
}

const surfaceOf = (via: string | undefined): Surface => (surfaces.find((x) => x === via) ?? 'other');
const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;
const every = 'Showing every request';
const calm = 'Animation is off · the counters, the feed and the Miss board still get every event';

type Live = Omit<Particle, 'leg' | 'progress' | 'end'> & { start: number };

export function createStage(o: { reduced?: boolean } = {}) {
  let live: Live[] = [];
  let seen = 0;
  let missed = 0;
  let away = false;
  let sample: { n: number; at: number } | undefined;
  let said: { text: string; at: number } | undefined;
  let chipSaid: { text: string; at: number } | undefined;
  const lanes = new Map<string, number>();
  // Items served since the stage was created, by cut label; capped like the lanes.
  const items = new Map<string, number>();
  // Served events with no Item named: their particle ends at "other", so they count there.
  let unnamed = 0;

  const expire = (now: number) => (live = live.filter((p) => now - p.start < travelMs && now >= p.start));
  const summaryText = () => {
    if (!seen) return 'No requests yet.';
    const top = [...items].sort((a, b) => b[1] - a[1]).slice(0, maxItems);
    const most = top.length ? ` Most served: ${top.map(([item, n]) => `${item} (${n})`).join(', ')}.` : '';
    return `${plural(seen, 'request', 'requests')} so far, ${plural(missed, 'miss', 'misses')}.${most}`;
  };

  const tally = (events: ActivityEvent[]) => {
    for (const e of events) {
      seen++;
      if (e.miss) missed++;
      if (!e.miss && !e.served) unnamed++;
      const item = e.miss || !e.served ? '' : labelText(e.served);
      if (item && (items.has(item) || items.size < maxTallied)) items.set(item, (items.get(item) ?? 0) + 1);
      if (e.project && (lanes.has(e.project) || lanes.size < maxTallied)) lanes.set(e.project, (lanes.get(e.project) ?? 0) + 1);
    }
  };

  return {
    // count only counts (lanes, totals): what a Pause held, applied on Resume without a particle.
    count: tally,
    // add counts every event, and queues a particle for as many as the pool has room for.
    add(events: ActivityEvent[], now: number) {
      tally(events);
      if (o.reduced || away || !events.length) return;
      expire(now);
      const room = maxParticles - live.length;
      const n = events.length > room ? Math.max(2, Math.ceil(events.length / Math.max(room, 1))) : 1;
      if (n > 1) sample = { n, at: now };
      for (let i = 0; i < events.length && live.length < maxParticles; i += n) {
        const e = events[i];
        live.push({
          project: e.project ?? '',
          surface: surfaceOf(e.via),
          target: e.miss ? 'miss' : 'served',
          label: labelText(e.miss ? e.query : (e.served ?? e.query)),
          start: now,
        });
      }
    },
    // setHidden pauses rendering: the queue is dropped, and what arrives while hidden is only counted.
    setHidden(hide: boolean, now: number) {
      away = hide;
      if (hide) live = [];
      else expire(now);
    },
    read(now: number): StageView {
      expire(now);
      if (!said || now - said.at >= summaryEvery) said = { text: summaryText(), at: now };
      const sampling = sample && now - sample.at < travelMs ? sample.n : 0;
      const half = travelMs / 2;
      // The chip is a polite status (A11): it changes at most once a second, however fast the sampling flips.
      const chipText = o.reduced ? calm : sampling ? `Showing 1 in ${sampling} requests · the feed and Miss board still get every event` : every;
      if (!chipSaid || (chipText !== chipSaid.text && now - chipSaid.at >= chipEvery)) chipSaid = { text: chipText, at: now };
      const chip = chipSaid;
      const ranked = [...items].sort((a, b) => b[1] - a[1]);
      const top = ranked.slice(0, maxItems).map(([item, count]) => ({ item, count }));
      const named = new Set(top.map((i) => i.item));
      return {
        particles: live.map(({ start, ...p }) => {
          const t = now - start;
          const end: Particle['end'] = p.target === 'miss' ? 'miss' : named.has(p.label) ? 'item' : 'other';
          return t < half ? { ...p, end, leg: 'in', progress: t / half } : { ...p, end, leg: 'out', progress: (t - half) / half };
        }),
        items: top,
        otherItems: ranked.slice(maxItems).reduce((n, [, c]) => n + c, unnamed),
        lanes: [...lanes]
          .sort((a, b) => b[1] - a[1])
          .slice(0, maxLanes)
          .map(([project, count]) => ({ project, count })),
        seen,
        missed,
        chip: chip.text,
        summary: said.text,
      };
    },
  };
}
