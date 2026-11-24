import { ApiError, fallback, get } from '../api/client';
import type { components } from '../api/schema';

export type SearchHit = components['schemas']['SearchHit'];
export type SearchResults = components['schemas']['SearchResults'];

// The search words the URL names: the first `q` value, trimmed. Empty is no search.
export function queryOf(query: Record<string, unknown>): string {
  const q = Array.isArray(query.q) ? query.q[0] : query.q;
  return typeof q === 'string' ? q.trim() : '';
}

// The Kinds the API's `kind` parameter accepts, in the order the pills show them.
export const searchKinds = ['article', 'skill', 'agent', 'decision'] as const;

// searchItems asks GET /api/v1/search for one query. The query travels as one
// encoded parameter, and so do `kind` and `project` when there are any: the
// server filters the Kind and applies the Project's Scope. `drafts` is never
// sent. `enforced` and `overrides` stay exactly as the server sent them; absent
// means unknown (#529).
// Failures are the shared ApiError (envelope message and hint, or fixed copy),
// and a 401 ends a signed-in session visibly because get goes through api().
export async function searchItems(q: string, scope: { kind?: string; project?: string } = {}, signal?: AbortSignal): Promise<SearchResults> {
  const params = new URLSearchParams({ q });
  if (scope.kind) params.set('kind', scope.kind);
  if (scope.project) params.set('project', scope.project);
  let ok: Partial<SearchResults> | undefined;
  try {
    ok = await get<Partial<SearchResults> | undefined>(`/api/v1/search?${params}`, signal);
  } catch (e) {
    throw e instanceof ApiError ? searchCopy(e) : e;
  }
  if (!ok || !Array.isArray(ok.results)) throw new ApiError(200, unavailable);
  return ok as SearchResults;
}

const unavailable = "Couldn't load search results.";

// searchCopy keeps an envelope's message and hint and gives every other failure
// the search's own copy (handoff §8).
function searchCopy(e: ApiError): ApiError {
  if (e.status === 0) return new ApiError(0, unavailable, "goldie isn't answering. Check your connection and try again.");
  return e.message === fallback ? new ApiError(e.status, unavailable, e.hint, e.retryAfter) : e;
}
