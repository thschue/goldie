import type { components } from '../api/schema';

export type SearchHit = components['schemas']['SearchHit'];
export type SearchResults = components['schemas']['SearchResults'];

// SearchError is what the UI may show: the envelope's message and first hint
// when the server sent one, otherwise fixed copy. A body that is not an
// envelope, a transport error text, an address or a path is never echoed.
export class SearchError extends Error {
  constructor(
    message: string,
    readonly hint?: string,
  ) {
    super(message);
    this.name = 'SearchError';
  }
}

const unavailable = "Couldn't load search results.";

// searchItems asks GET /api/v1/search for one query. The query travels as one
// encoded parameter. There is no `kind` parameter: the Kind filter is the
// client's, over whatever this one response holds. `enforced` and `overrides`
// stay exactly as the server sent them; absent means unknown (#529).
export async function searchItems(q: string, signal?: AbortSignal): Promise<SearchResults> {
  let res: Response;
  try {
    res = await fetch(`/api/v1/search?${new URLSearchParams({ q })}`, { signal, headers: { Accept: 'application/json' } });
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e;
    throw new SearchError(unavailable, "goldie isn't answering. Check your connection and try again.");
  }
  let body: unknown;
  try {
    body = await res.json();
  } catch {
    body = undefined;
  }
  if (!res.ok) throw fromEnvelope(body);
  const ok = body as Partial<SearchResults> | undefined;
  if (!ok || !Array.isArray(ok.results)) throw new SearchError(unavailable);
  return ok as SearchResults;
}

function fromEnvelope(body: unknown): SearchError {
  const err = (body as { error?: { message?: unknown; details?: { hint?: unknown } } } | undefined)?.error;
  if (typeof err?.message !== 'string' || !err.message) return new SearchError(unavailable);
  const hint = err.details?.hint;
  return new SearchError(err.message, typeof hint === 'string' ? hint : undefined);
}
