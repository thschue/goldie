# 28. A session cookie authenticates unsafe methods only with a CSRF token

Status: proposed

## Context

ADR 0027 limits the session cookie to `GET` and `HEAD`, so a forged request cannot change anything.
A browser UI that writes needs the cookie to authenticate unsafe methods too, without making
cross-site request forgery possible.

## Decision

This supersedes the "`GET` and `HEAD` only" bullet of ADR 0027; the rest of it stands.

- Sign-in and `GET /api/v1/session` return a CSRF token in the `X-Goldie-CSRF` response header. It
  is an HMAC, under the session key, of the token's name, its digest and the sign-in time.
- An unsafe request with only the cookie must send that token in the `X-Goldie-CSRF` request header
  and pass `http.CrossOriginProtection` (`Origin` and `Sec-Fetch-Site`). Either failing is a 403 in
  the envelope.
- A bearer token beats the cookie and needs no CSRF token.
- `DELETE /api/v1/session` keeps its origin check only.

## Consequences

- The token is stateless, so there is no per-session revocation. Rotating the session key or the
  token's value invalidates it.
- Behind a proxy that rewrites `Host`, same-origin writes get a 403 from the origin check, so
  operators forward `Host`.
- The change is additive under ADR 0011.
