// The activity stream (#630, handoff §3.6): GET /api/v1/activity is server-sent events, read with
// fetch through api() (not EventSource) so a 401 ends the session like any other call and the
// error envelope is the one the other views read. The parser is pure and fed chunk by chunk.
import { failure } from '../api/client';
import type { components } from '../api/schema';
import { api } from '../auth/session';

export type ActivityEvent = components['schemas']['ActivityEvent'];

export type Frame = { type: 'event'; event: ActivityEvent } | { type: 'dropped'; count: number } | { type: 'live' };

// One frame is far below this; a longer one without a blank line is hostile or broken.
const maxFrame = 256 * 1024;

const isEvent = (v: unknown): v is ActivityEvent => {
  const e = v as Partial<ActivityEvent> | null;
  return (
    typeof e === 'object' &&
    e !== null &&
    typeof e.id === 'number' &&
    typeof e.time === 'string' &&
    (e.kind === 'search' || e.kind === 'lookup' || e.kind === 'get') &&
    typeof e.query === 'string' &&
    typeof e.miss === 'boolean'
  );
};

// parseFrame reads one SSE frame (the lines between blank lines). Unknown event types, comments
// and malformed data are ignored, as the contract asks of clients.
export function parseFrame(raw: string): Frame | undefined {
  let type = '';
  const data: string[] = [];
  for (const line of raw.split('\n')) {
    if (line.startsWith('event:')) type = line.slice(6).trim();
    else if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''));
  }
  if (type === 'live') return { type: 'live' };
  let body: unknown;
  try {
    body = JSON.parse(data.join('\n'));
  } catch {
    return undefined;
  }
  if (type === '' && isEvent(body)) {
    return {
      type: 'event',
      event: {
        ...body,
        project: typeof body.project === 'string' ? body.project : '',
        replica: typeof body.replica === 'string' ? body.replica : '',
        late: body.late === true,
      },
    };
  }
  if (type === 'dropped') {
    const n = (body as { dropped?: unknown } | null)?.dropped;
    return { type: 'dropped', count: typeof n === 'number' ? n : 0 };
  }
  return undefined;
}

// split cuts what has arrived into whole frames and the unfinished rest.
export function split(buffer: string): { frames: Frame[]; rest: string } {
  const parts = buffer.replace(/\r\n?/g, '\n').split('\n\n');
  const rest = parts.pop() ?? '';
  const frames: Frame[] = [];
  for (const part of parts) {
    const f = parseFrame(part);
    if (f) frames.push(f);
  }
  return { frames, rest: rest.length > maxFrame ? '' : rest };
}

// streamActivity opens the local replica's stream and calls onFrames with each chunk's frames,
// so a burst is one update. It resolves when the stream ends, throws an ApiError for a non-200
// answer and a plain Error when the connection fails; an abort is an AbortError.
export async function streamActivity(signal: AbortSignal, onFrames: (frames: Frame[]) => void): Promise<void> {
  const res = await api('/api/v1/activity', { signal, headers: { Accept: 'text/event-stream' } });
  if (!res.ok) throw await failure(res);
  if (!res.body) throw new Error('no stream');
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buffer = '';
  for (;;) {
    const { done, value } = await reader.read();
    if (done) return;
    const out = split(buffer + value);
    buffer = out.rest;
    if (out.frames.length) onFrames(out.frames);
  }
}
