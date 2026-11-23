import { api, csrf } from '../auth/session';

// The shared API fetch (moved from projects/api.ts for #587): an error shrinks to the envelope's
// message and hint, and a 429 carries its Retry-After.
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

export const fallback = "Couldn't load this. Try again in a moment.";
const clip = (v: unknown, max: number) => (typeof v === 'string' ? v.slice(0, max) : '');

export async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  return (await request(path, { signal, headers: { Accept: 'application/json' } })).json() as Promise<T>;
}

// post sends an unsafe request from the session: the CSRF token goes in X-Goldie-CSRF (#490). It
// resolves on any 2xx and throws the same ApiError as get otherwise.
export async function post(path: string): Promise<void> {
  await request(path, { method: 'POST', headers: { Accept: 'application/json', 'X-Goldie-CSRF': csrf.value } });
}

async function request(path: string, init: RequestInit): Promise<Response> {
  let res: Response;
  try {
    res = await api(path, init);
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e;
    throw new ApiError(0, "goldie isn't answering right now.", 'Check your connection, then retry.');
  }
  if (res.ok) return res;
  throw await failure(res);
}

// failure turns a non-ok response into an ApiError: the envelope's message and hint, or the
// generic copy when the body is not an envelope.
export async function failure(res: Response): Promise<ApiError> {
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
  return new ApiError(res.status, message || fallback, message ? hint : '', retryAfter);
}
