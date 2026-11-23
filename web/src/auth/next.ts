// safeNext (handoff §5.1, Unauthenticated): `next` is honoured only as a
// same-origin path. The value is checked as the router hands it over (decoded
// once) and again after one more decoding, so %2f%2f cannot hide a second
// slash. Anything else falls back to "/".
const unsafe = /[\u0000- \u007f-\u009f\\]/;

function plainPath(value: string): boolean {
  return value.startsWith('/') && value[1] !== '/' && !unsafe.test(value);
}

export function safeNext(raw: unknown, origin: string = window.location.origin): string {
  if (typeof raw !== 'string' || !plainPath(raw)) return '/';
  try {
    if (!plainPath(decodeURIComponent(raw))) return '/';
    const url = new URL(raw, origin);
    if (url.origin !== origin) return '/';
    // Back to the sign-in page would only show the form again.
    return url.pathname === '/signin' ? '/' : url.pathname + url.search + url.hash;
  } catch {
    return '/';
  }
}
