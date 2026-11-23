// The Sources calls (#633): the sync status of every Source, for operators (GET /api/v1/sync).
import { ref } from 'vue';
import { plain } from '../assets/api';
import { get } from '../api/client';
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
