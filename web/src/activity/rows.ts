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

// The noun a get asked for, from the Reference it was sent as (ADR 0021). A bare name comes from
// a replica older than that form, so the kind is unknown.
function nounOf(e: ActivityEvent): string {
  if (e.kind === 'search') return 'result';
  if (e.kind === 'lookup') return 'article';
  const m = /^(skill|agent|article):/.exec(e.query);
  return m ? m[1] : 'item';
}

// The Result cell: a served row says so, and a miss says Miss in words with copy that names
// what was asked for (#576). The Reference is shown as sent in the Query cell, so it is not
// repeated here.
export function resultText(e: ActivityEvent): string {
  if (!e.miss) return 'Served';
  switch (nounOf(e)) {
    case 'result':
      return 'Miss · no results';
    case 'item':
      return 'Miss · no skill or agent matched';
    default:
      return `Miss · no ${nounOf(e)} matched`;
  }
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
