import { ApiError, get } from '../api/client';
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
  const ok = await get<Partial<SearchResults> | undefined>(`/api/v1/search?${new URLSearchParams({ q })}`, signal);
  if (!ok || !Array.isArray(ok.results)) throw new ApiError(200, "Couldn't load search results.");
  return ok as SearchResults;
}
