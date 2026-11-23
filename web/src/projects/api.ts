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
