import { ApiError, fallback, get } from '../api/client';
import type { components } from '../api/schema';

export type SearchHit = components['schemas']['SearchHit'];
export type SearchResults = components['schemas']['SearchResults'];

// searchItems asks GET /api/v1/search for one query. The query travels as one
// encoded parameter. There is no `kind` parameter: the Kind filter is the
// client's, over whatever this one response holds. `enforced` and `overrides`
// stay exactly as the server sent them; absent means unknown (#529).
// Failures are the shared ApiError (envelope message and hint, or fixed copy),
// and a 401 ends a signed-in session visibly because get goes through api().
export async function searchItems(q: string, signal?: AbortSignal): Promise<SearchResults> {
  let ok: Partial<SearchResults> | undefined;
  try {
    ok = await get<Partial<SearchResults> | undefined>(`/api/v1/search?${new URLSearchParams({ q })}`, signal);
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
