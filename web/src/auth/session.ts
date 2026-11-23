import { ref } from 'vue';
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

export const role = (): Role => (session.value?.role === 'operator' ? 'operator' : 'reader');

// An error envelope's hint (details.hint), or '' when the body is not one.
async function hintOf(res: Response): Promise<string> {
  try {
    const hint = ((await res.json()) as components['schemas']['ErrorEnvelope']).error?.details?.hint;
    return typeof hint === 'string' ? hint : '';
  } catch {
    return '';
  }
}

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
export async function refresh(): Promise<boolean> {
  try {
    const res = await api(endpoint);
    if (res.ok) {
      session.value = (await res.json()) as Session;
      ended.value = false;
      return true;
    }
  } catch {
    // Offline or the server is down: carry on with what is known.
  }
  return session.value !== null;
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
    session.value = (await res.json()) as Session;
    ended.value = false;
    return { ok: true };
  }
  const hint = await hintOf(res);
  if (res.status === 401) return { ok: false, kind: 'refused', hint };
  if (res.status === 429) {
    const wait = Number.parseInt(res.headers.get('Retry-After') ?? '', 10);
    return { ok: false, kind: 'limited', seconds: Math.min(Math.max(Number.isFinite(wait) ? wait : 5, 1), 3600), hint };
  }
  return { ok: false, kind: 'failed', hint };
}

// signOut clears the cookie; the local session goes either way.
export async function signOut(): Promise<void> {
  try {
    await fetch(endpoint, { method: 'DELETE' });
  } catch {
    // The cookie then ends by itself; the person is signed out of this view.
  }
  session.value = null;
  ended.value = false;
}
