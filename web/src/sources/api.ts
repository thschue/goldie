// The Sources calls (#633): the sync status of every Source, for operators (GET /api/v1/sync).
import { ref } from 'vue';
import { plain } from '../assets/api';
import { get, post } from '../api/client';
import type { components } from '../api/schema';

export type SourceStatus = components['schemas']['SourceSyncStatus'];

export const isFailing = (s: SourceStatus) => !!s.last_error;

// The §3.7 badge: no last error is healthy; a last error with a last success still serves that
// Snapshot; a last error and no success has nothing to serve.
export const sourceState = (s: SourceStatus) =>
  !s.last_error ? 'healthy' : s.last_success ? 'failing · serving last good' : 'errored';

// Failing Sources first, the API's order otherwise (Array.sort is stable).
export const failingFirst = (sources: SourceStatus[]) =>
  [...sources].sort((a, b) => Number(isFailing(b)) - Number(isFailing(a)));

// The failing count the nav shows; undefined until a load has answered.
export const failing = ref<number>();

export async function loadSources(signal?: AbortSignal): Promise<SourceStatus[]> {
  const { sources } = await get<components['schemas']['SyncStatus']>('/api/v1/sync', signal);
  failing.value = sources.filter(isFailing).length;
  return failingFirst(sources);
}

// Starts a sync (POST /api/v1/sync, 202): the poll runs on after the answer, so its outcome is
// read from the status afterwards (waitForSync). With a name, only that Source is triggered; the
// name travels as one encoded query value.
export const resync = (source?: string) =>
  post(source ? `/api/v1/sync?${new URLSearchParams({ source })}` : '/api/v1/sync');

// Where a Source comes from, derived from its name: secret:<ns>/<name> is a Secret, any other
// Source is configured through the environment.
export function origin(name?: string): string {
  const m = /^secret:([^/]+)\/(.+)$/.exec(name ?? '');
  return m ? `Secret ${m[1]}/${m[2]}` : 'env GOLDIE_SOURCES';
}

const pollTimes = (sources: SourceStatus[]) => sources.map((s) => s.last_poll ?? '').join('|');

// The sync has ended once a Source's last_poll differs from `before` (or, with no Source at all,
// once a read comes back empty). Polls the status every `every` ms for at most `limit` ms and
// resolves with the latest list read (undefined when no read worked) and whether the sync ended;
// an aborted signal rejects with the AbortError.
export async function waitForSync(before: SourceStatus[], signal: AbortSignal, every = 1000, limit = 45000) {
  const start = pollTimes(before);
  const deadline = Date.now() + limit;
  let rows: SourceStatus[] | undefined;
  while (Date.now() < deadline) {
    await new Promise<void>((resolve, reject) => {
      const onAbort = () => (clearTimeout(t), reject(new DOMException('', 'AbortError')));
      const t = setTimeout(() => (signal.removeEventListener('abort', onAbort), resolve()), every);
      signal.addEventListener('abort', onAbort, { once: true });
    });
    try {
      rows = await loadSources(signal);
      if (pollTimes(rows) !== start || (!before.length && !rows.length)) return { rows, ended: true };
    } catch (e) {
      if (signal.aborted) throw e;
    }
  }
  return { rows, ended: false };
}
let pending: Promise<unknown> | undefined;

// The nav's one load: it never throws (a nav count that can't load is just absent) and several
// navs share a single request.
export function loadFailing() {
  pending ??= loadSources()
    .catch(() => undefined)
    .finally(() => (pending = undefined));
  return pending;
}

// A time as the person reads it, in UTC; anything unparseable is shown as plain text.
export function when(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime())
    ? plain(iso)
    : `${d.toLocaleString('en-GB', { dateStyle: 'medium', timeStyle: 'short', timeZone: 'UTC' })} UTC`;
}
