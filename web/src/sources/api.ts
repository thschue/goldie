// The Sources calls (#633): the sync status of every Source, for operators (GET /api/v1/sync).
import { ref } from 'vue';
import { plain } from '../assets/api';
import { get, post } from '../api/client';
import type { components } from '../api/schema';

export type SourceStatus = components['schemas']['SourceSyncStatus'];

export const isFailing = (s: SourceStatus) => !!s.last_error;

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
// read from the status afterwards (waitForSync).
export const resync = () => post('/api/v1/sync');

const pollTimes = (sources: SourceStatus[]) => sources.map((s) => s.last_poll ?? '').join('|');

// The sync has ended once a Source's last_poll differs from `before`. Polls the status every
// `every` ms for at most `limit` ms and resolves with the latest list read (undefined when no read
// worked); an aborted signal rejects with the AbortError.
export async function waitForSync(before: SourceStatus[], signal: AbortSignal, every = 1000, limit = 45000) {
  const start = pollTimes(before);
  const deadline = Date.now() + limit;
  let latest: SourceStatus[] | undefined;
  while (Date.now() < deadline) {
    await new Promise<void>((resolve, reject) => {
      const t = setTimeout(resolve, every);
      signal.addEventListener('abort', () => (clearTimeout(t), reject(new DOMException('', 'AbortError'))), { once: true });
    });
    try {
      latest = await loadSources(signal);
      if (pollTimes(latest) !== start) break;
    } catch (e) {
      if (signal.aborted) throw e;
    }
  }
  return latest;
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
