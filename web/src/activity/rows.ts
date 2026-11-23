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

// add puts new events (oldest first, as the stream sends them) on top, newest first, and keeps
// the newest maxRows. A reconnect starts from an empty list: the stream replays its history.
export function add(rows: ActivityEvent[], fresh: ActivityEvent[]): ActivityEvent[] {
  return [...fresh.slice(-maxRows).reverse(), ...rows].slice(0, maxRows);
}

export const timeText = (iso: string) => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleTimeString([], { hour12: false });
};
