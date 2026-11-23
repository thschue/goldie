import { ref } from 'vue';
import { failure } from '../api/client';
import type { components } from '../api/schema';

// The session store (#495). The browser never holds the token: sign-in
// exchanges it once for an HttpOnly cookie, and this module only knows who the
// cookie signs in (GET /api/v1/session). Every call a view makes goes through
// api(), so a 401 on a later call ends the session visibly.
export type Session = components['schemas']['Session'];
export type Role = 'reader' | 'operator';

const endpoint = '/api/v1/session';

export const session = ref<Session | null>(null);
// True once a session that was signed in has answered 401.
export const ended = ref(false);
// True when the first session check got no answer (429, 5xx, offline): who the
// cookie signs in is unknown, which is not the same as signed out (#673).
export const checkFailed = ref(false);

// The CSRF token of the session (#490, ADR 0027): GET and POST /api/v1/session return it in
// X-Goldie-CSRF, and every unsafe request sends it back. Held in memory only.
export const csrf = ref('');
const keepCSRF = (res: Response) => {
  const t = res.headers.get('X-Goldie-CSRF');
  if (t) csrf.value = t;
};

export const role = (): Role => (session.value?.role === 'operator' ? 'operator' : 'reader');

// api is fetch for the API: a 401 marks a signed-in session as ended, and the
// caller still gets the response. Sign-in does not use it: a wrong token is
// not a session ending.
export async function api(path: string, init?: RequestInit): Promise<Response> {
  const res = await fetch(path, init);
  if (res.status === 401 && session.value) ended.value = true;
  return res;
}

// refresh asks who the cookie signs in. True when there is a session to show:
// a known session that now answers 401 is marked ended and the view stays.
// Only a 401 means signed out. Any other failure with no known session sets
// checkFailed and reports true, so the guard keeps the route and the app shows
// a retryable notice instead of sending the person to /signin.
//
// checkFailed changes only once the answer is in, so a notice stays up while a
// retry is in flight and the unconfirmed page never mounts meanwhile.
export async function refresh(): Promise<boolean> {
  try {
    const res = await api(endpoint);
    if (res.ok) {
      keepCSRF(res);
      session.value = (await res.json()) as Session;
      ended.value = false;
      checkFailed.value = false;
      return true;
    }
    if (res.status === 401) {
      checkFailed.value = false;
      return session.value !== null;
    }
  } catch {
    // Offline or the server is down: treated like any other failed check.
  }
  checkFailed.value = session.value === null;
  return true;
}

export type SignInResult =
  | { ok: true }
  | { ok: false; kind: 'refused' | 'failed'; hint: string }
  | { ok: false; kind: 'limited'; seconds: number; hint: string };

// signIn exchanges the token, sent as JSON from JavaScript (the UI's CSP is
// form-action 'none'), for the session cookie.
export async function signIn(token: string): Promise<SignInResult> {
  let res: Response;
  try {
    res = await fetch(endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token }),
    });
  } catch {
    return { ok: false, kind: 'failed', hint: '' };
  }
  if (res.ok) {
    keepCSRF(res);
    session.value = (await res.json()) as Session;
    ended.value = false;
    return { ok: true };
  }
  const { hint, retryAfter } = await failure(res);
  if (res.status === 401) return { ok: false, kind: 'refused', hint };
  if (res.status === 429) return { ok: false, kind: 'limited', seconds: retryAfter, hint };
  return { ok: false, kind: 'failed', hint };
}

// signOut clears the cookie and then the local session. True when the server
// confirmed; on a failed request the person stays signed in (the cookie may
// still be valid) and the caller offers a retry.
export async function signOut(): Promise<boolean> {
  try {
    const res = await fetch(endpoint, { method: 'DELETE' });
    if (!res.ok) return false;
  } catch {
    return false;
  }
  session.value = null;
  csrf.value = '';
  ended.value = false;
  return true;
}
