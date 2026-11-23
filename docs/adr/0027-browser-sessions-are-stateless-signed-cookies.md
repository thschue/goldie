# 27. Browser sessions are stateless signed cookies

Status: accepted

## Context

A bearer token kept in a browser page is readable by any script in it. The server keeps no state
beyond its content and runs as replicas that share nothing, yet a browser must sign in once and
then read the API without holding the token.

## Decision

This is the first slice of the session layer ADR 0008 calls for; refresh, CSRF protection and
per-session revocation arrive with GitHub sign-in (#25).

`POST /api/v1/session` exchanges a configured API token for a session cookie. The cookie is
`HttpOnly`, `SameSite=Strict` and scoped to the whole site, and it carries the token's name and its
issue and expiry times under an HMAC. It never carries the token or its Role. The server keeps no
session store: it looks the token name up in the current configuration, then checks the HMAC, which
covers the name and the digest of the token's value. So the Role is always the configured one, and a
token removed from the configuration, or given a new value under the same name, ends its sessions at
once.

- The cookie authenticates `GET` and `HEAD` only. An unsafe method with only a cookie is refused
  (403), so a cookie cannot be used to change anything. A bearer token beats a cookie.
- Sign-in and sign-out take no bearer token; a cross-origin request to them is refused. They are
  outside the per-token rate limiter, which needs a token name. Failed sign-ins are capped per
  remote host instead (10 per minute, then 429 with Retry-After; #489).
- A session ends after a short idle time, which each use extends, and at an absolute limit from
  sign-in.
- The signing key is `GOLDIE_SESSION_KEY` (or its `_FILE` form). Without one, each process makes a
  random key, so replicas then reject each other's cookies. Replicas that should share sessions
  must share the key. The key is never derived from token digests, so a leaked digest cannot forge
  a cookie. Rotating the key signs everyone out.
- Over TLS the cookie is `Secure` with the `__Host-` prefix. On a plain-http loopback listener
  (`goldie server --dev`, local mode) a browser would drop both, so they are left off there only. That
  is decided by the configuration the composition root passes in, never by the Host header or a
  forwarded header: goldie terminates no TLS, so a server behind a TLS proxy always gets both.

## Alternatives rejected

- A server-side session store. It adds state, shared storage across replicas and cleanup, for a
  server that has none today.
- Keeping the token in browser storage. Any script in the page can read it.
- Letting the cookie authenticate every method. A forged request would then carry the credential;
  limiting it to `GET` and `HEAD` and refusing cross-origin sign-out keeps the damage small.

## Consequences

There is no per-session revocation: a session ends by removing its token from the configuration,
letting it expire, or rotating the key, which ends all of them. A session's reach is that of its
token's current Role, read on every request. The cookie is a credential (see the platform contract),
so the key is a secret and is never logged. Under ADR 0011 the session endpoint and the cookie's
behaviour are part of the contract.
