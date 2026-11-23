// Project calls (#496): ids are one encoded path segment (ADR 0016); errors shrink to the envelope's message and hint.
import type { components } from '../api/schema';

export type ProjectView = components['schemas']['ProjectView'];

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly hint = '',
    readonly retryAfter = 0,
  ) {
    super(message);
  }
}

const fallback = "Couldn't load this. Try again in a moment.";
const clip = (v: unknown, max: number) => (typeof v === 'string' ? v.slice(0, max) : '');

async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, { signal, headers: { Accept: 'application/json' } });
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e;
    throw new ApiError(0, "goldie isn't answering right now.", 'Check your connection, then retry.');
  }
  if (res.ok) return (await res.json()) as T;
  let message = '';
  let hint = '';
  try {
    const body = (await res.json()) as { error?: { message?: unknown; details?: { hint?: unknown } } };
    message = clip(body.error?.message, 300);
    hint = clip(body.error?.details?.hint, 300);
  } catch {
    // Not an envelope: the generic copy only.
  }
  const seconds = Number.parseInt(res.headers.get('Retry-After') ?? '', 10);
  const retryAfter = res.status === 429 ? Math.min(Math.max(Number.isFinite(seconds) ? seconds : 5, 1), 3600) : 0;
  throw new ApiError(res.status, message || fallback, message ? hint : '', retryAfter);
}

export const listProjects = (signal?: AbortSignal) =>
  get<components['schemas']['ProjectList']>('/api/v1/projects', signal).then((l) => l.projects);

export const projectItems = (id: string, signal?: AbortSignal) =>
  get<ProjectView>(`/api/v1/projects/${encodeURIComponent(id)}/items`, signal);

// The Project the URL names: the first `project` value, if any.
export function projectOf(query: Record<string, unknown>): string {
  const v = Array.isArray(query.project) ? query.project[0] : query.project;
  return typeof v === 'string' ? v : '';
}
