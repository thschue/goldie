# 32. A GitHub session cookie carries the Role and ends an hour after sign-in

Status: proposed

Amends ADR 0027.

## Context

ADR 0027 keeps the session cookie free of the Role: it names a token and the server reads the Role
from the configuration on every request. A GitHub sign-in has no configured token to look up. Who the
user is, and which Role their organisation and team membership earns, are known only at sign-in, and
asking GitHub on every request would put an outbound call in the path of every read.

## Decision

`GET /api/v1/session/github` and its callback sign a browser in with GitHub (OAuth authorization code,
PKCE `S256`, a random `state`, and a signed pre-login cookie that is `SameSite=Lax` and lives 10
minutes). The session cookie it ends with is the one of ADR 0027 with a different payload: the GitHub
login, the Role and the issue and expiry times, under the same HMAC key and a domain-separated MAC, so
a token cookie can never be read as a GitHub cookie or the reverse.

- The Role is signed into the cookie, so it is not re-read per request. The membership re-check is the
  absolute cap: a GitHub session ends 1 hour after sign-in at the latest, whatever its use (a token
  session keeps its 7 days). The idle limit of 30 minutes is unchanged. A user removed from the
  organisation or team keeps their Role for up to an hour.
- There is no refresh token and no denylist. The GitHub access token is used only during the callback
  and is revoked there (`DELETE /applications/{client_id}/token`); goldie never stores it. Sign-out
  clears the cookie and nothing else, as in ADR 0027, so a copied cookie stays valid until it expires.
- The principal is named `github:<role>` in the session resource, logs and metrics. The login is in the
  cookie and the rate limiter's key (so one user's use does not spend another's of the same Role), and
  nowhere else.
- The cookie authenticates what ADR 0027 and ADR 0028 say it does, and, as every session cookie
  does, `GET` on `/mcp`, `/openapi.*` and `/swagger-ui`.

## Alternatives rejected

- Asking GitHub on each request. It adds latency and an outbound dependency to every read, and a GitHub
  outage would sign everyone out.
- A server-side session store with revocation. It is the state ADR 0027 avoided, for replicas that share
  nothing.
- A longer cap with a refresh token. The refresh token is a long-lived GitHub credential to store.

## Consequences

Removing a user from GitHub takes up to an hour to reach goldie; shortening the cap is the lever, and
signing everyone out (rotating `GOLDIE_SESSION_KEY`) is the emergency stop. `docs/usage/auth.md` states
the threat model.
