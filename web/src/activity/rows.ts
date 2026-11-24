// What the feed shows of an event (#630, handoff §3.6, §8). Query text is untrusted (it is
// whatever a client sent) and is only ever interpolated as text; plain() removes the invisible
// and bidirectional controls that could reorder what a reader sees.
import { plain } from '../assets/api';
import type { ActivityEvent } from './stream';

// The rows kept: the newest. A replica's history holds up to 10,000 events; a page does not
// need them all, and a table that long would be slow to render.
export const maxRows = 500;

// How often the miss count may change for a screen reader (handoff §9).
export const announceEvery = 30_000;

export const queryText = (e: ActivityEvent) => plain(e.query);

// The Result cell: a served row says so, and a miss says Miss in words with the copy of handoff
// §8 that names what was asked for (#576): "No skill named x", "No agent named x", "No article
// named x", "No results for x", and for a bare name from a replica older than ADR 0021's
// Reference form "No skill or agent named x".
export function resultText(e: ActivityEvent): string {
  if (!e.miss) return 'Served';
  const q = queryText(e);
  if (e.kind === 'search') return `Miss · No results for ${q}`;
  if (e.kind === 'lookup') return `Miss · No article named ${q}`;
  const m = /^(skill|agent|article):(.*)$/s.exec(q);
  return m ? `Miss · No ${m[1]} named ${m[2]}` : `Miss · No skill or agent named ${q}`;
}

// The surface an event came through (#810): the pills' vocabulary. An event without `via` (a replica
// older than the field) belongs to none; a value this page does not know is Other.
export const surfaces = ['mcp', 'cli', 'ui', 'other'] as const;
export type Surface = (typeof surfaces)[number];
export const surfaceLabel: Record<Surface, string> = { mcp: 'MCP', cli: 'CLI', ui: 'UI', other: 'Other' };

export function surfaceOf(e: ActivityEvent): Surface | undefined {
  if (!e.via) return undefined;
  return (surfaces as readonly string[]).includes(e.via) ? (e.via as Surface) : 'other';
}

// A latency in microseconds as milliseconds with one decimal: "0.9 ms". Rounded on integers, so
// 850 is 0.9, not the 0.8 that floating point gives.
export const msText = (us: number) => `${(Math.round(us / 100) / 10).toFixed(1)} ms`;

// clip cuts text to at most max code points, never inside a character, and marks the cut. Event
// text is whatever a client sent, so no cell or detail field shows it unbounded.
export const maxText = 200;
export function clip(s: string, max = maxText): string {
  let i = 0;
  for (let n = 0; i < s.length && n < max; n++) i += s.codePointAt(i)! > 0xffff ? 2 : 1;
  return i < s.length ? `${s.slice(0, i)}…` : s;
}

// Event text as shown: without invisible or bidirectional controls, and cut.
export const shownText = (s: string | undefined) => clip(plain(s));

// The Result cell (#810): the served Item as sent, "Served" from a replica that does not say which,
// and for a miss the words that name the lack, besides Miss.
export function itemText(e: ActivityEvent): string {
  if (e.miss) return 'Miss · No Item matched';
  return e.served ? shownText(e.served) : 'Served';
}

// add puts new events (oldest first, as the stream sends them) on top, newest first, and keeps
// the newest maxRows. A reconnect starts from an empty list: the stream replays its history.
export function add(rows: ActivityEvent[], fresh: ActivityEvent[]): ActivityEvent[] {
  return [...fresh.slice(-maxRows).reverse(), ...rows].slice(0, maxRows);
}

export const timeText = (iso: string) => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleTimeString([], { hour12: false });
};

// agoText words how long ago an instant was: "30 seconds ago", "2 minutes ago". An instant that
// cannot be read, or lies ahead of now, is "a moment ago" rather than a wrong number.
export function agoText(iso: string, now: number): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t) || t > now) return 'a moment ago';
  const s = Math.floor((now - t) / 1000);
  const unit = (n: number, name: string) => `${n} ${name}${n === 1 ? '' : 's'} ago`;
  if (s < 60) return unit(s, 'second');
  if (s < 3600) return unit(Math.floor(s / 60), 'minute');
  if (s < 86400) return unit(Math.floor(s / 3600), 'hour');
  return unit(Math.floor(s / 86400), 'day');
}

export const boardSize = 4;

export type MissGroup = { query: string; count: number; projects: number; latest: ActivityEvent };

// missBoard groups the misses of a feed (newest first) by query: the most frequent first, the one
// seen last first among equals, at most boardSize. The latest event of a group is its first one.
export function missBoard(feed: ActivityEvent[]): MissGroup[] {
  const groups = new Map<string, MissGroup & { seen: Set<string> }>();
  for (const e of feed) {
    if (!e.miss) continue;
    const g = groups.get(e.query) ?? { query: e.query, count: 0, projects: 0, latest: e, seen: new Set<string>() };
    g.count++;
    if (e.project) g.seen.add(e.project);
    g.projects = g.seen.size;
    groups.set(e.query, g);
  }
  // Map keeps insertion order (newest first) and sort is stable, so ties stay newest first.
  return [...groups.values()].sort((a, b) => b.count - a.count).slice(0, boardSize);
}

// The line that says what to add for a miss (handoff §8), by what was asked for: a skill, an agent
// or an article. A bare get (a replica older than ADR 0021) could be a skill or an agent.
export function addText(e: ActivityEvent): string {
  const m = e.kind === 'get' ? /^(skill|agent|article):/.exec(e.query) : null;
  const what = e.kind === 'get' ? (m?.[1] ?? 'skill or agent') : 'article';
  return what === 'article' ? 'Start this article in the Source repo ↗' : `Add this ${what} to the Source repo ↗`;
}

// "Try this search" has a query to run for a search and a lookup, not for a get of a Reference.
export const canSearch = (e: ActivityEvent) => e.kind !== 'get';

// rowKey is what makes a row one event: ids are per replica and start again at 1 when a
// process restarts, so two replicas share ids.
export const rowKey = (e: ActivityEvent) => `${e.replica}/${e.id}`;
