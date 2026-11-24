// Project calls (#496): ids are one encoded path segment (ADR 0016); the fetch and its errors live in api/client.ts.
import { ApiError, get } from '../api/client';
import type { components } from '../api/schema';

export { ApiError };
export type ProjectView = components['schemas']['ProjectView'];

export const listProjects = (signal?: AbortSignal) =>
  get<components['schemas']['ProjectList']>('/api/v1/projects', signal).then((l) => l.projects);

export const projectItems = (id: string, signal?: AbortSignal) =>
  get<ProjectView>(`/api/v1/projects/${encodeURIComponent(id)}/items`, signal);

// The Project the URL names: the first `project` value, if any.
export function projectOf(query: Record<string, unknown>): string {
  const v = Array.isArray(query.project) ? query.project[0] : query.project;
  return typeof v === 'string' ? v : '';
}

// The query parts that follow the user from page to page (handoff §2.2, §3.1): the Project, and
// for an Item's way back the search it came from. Only these keys, only the first string value:
// anything else in the URL is not carried. vue-router percent-encodes each value, so a hostile
// id stays a value and never becomes a path.
export function carried(query: Record<string, unknown>, keys: readonly string[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const k of keys) {
    const v = Array.isArray(query[k]) ? (query[k] as unknown[])[0] : query[k];
    if (typeof v === 'string' && v) out[k] = v;
  }
  return out;
}

export const projectQuery = (query: Record<string, unknown>) => carried(query, ['project']);
