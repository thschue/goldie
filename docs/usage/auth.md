# Signing in

A browser signs in with an API token (`POST /api/v1/session`, ADR 0027) or with GitHub
(`GET /api/v1/session/github`, ADR 0032). Both end in the same signed session cookie. Scripts and
agents keep using bearer tokens.

## Sign in with GitHub

Set `GOLDIE_GITHUB_CLIENT_ID`, `GOLDIE_GITHUB_CLIENT_SECRET`, `GOLDIE_GITHUB_ORGS`,
`GOLDIE_PUBLIC_URL` and, for operators, `GOLDIE_GITHUB_OPERATOR_TEAMS`; see
[configuration](../reference/configuration.md). Register the OAuth app's callback URL as
`<GOLDIE_PUBLIC_URL>/api/v1/session/github/callback`.

- An active member of a listed organisation is a **reader**. An active member of a listed operator team is
  an **operator**. Anyone else is refused at `/signin?error=not_a_member`.
- The session is named `github:reader` or `github:operator`. The login is never shown, logged or put
  in a metric.
- A session ends after 30 minutes without use and 1 hour after sign-in at the latest. That hour is the
  membership re-check: a user removed from GitHub keeps their Role for up to an hour. Sign-out clears
  the cookie only. There is no refresh token and no denylist. To end every session at once, rotate
  `GOLDIE_SESSION_KEY`.
- A failed team lookup (SAML enforcement answering 403, or a 5xx) refuses the sign-in with
  `/signin?error=exchange_failed`, even for an active organisation member. It does not fall back to
  reader: a Role is never the result of an error. Fix the OAuth app's authorisation for the
  organisation (SAML SSO) and sign in again.
- goldie asks GitHub for the `read:org` scope, uses the access token only inside the callback, and
  revokes it there (`DELETE /applications/{client_id}/token`, as the OAuth app). A failed revocation
  changes nothing for the user. goldie never stores the token.

## Threat model

| Threat | What stops it |
| --- | --- |
| Login CSRF: an attacker's authorization code is delivered to a victim's browser | The `state` is random, one use, and must match the one in the signed pre-login cookie of the browser that started. The cookie is cleared on every callback. |
| Authorization code interception | PKCE `S256`: the code works only with the verifier held in the pre-login cookie. |
| Forged or replayed pre-login cookie | HMAC under `GOLDIE_SESSION_KEY` with its own domain string, a 10-minute expiry, and clearing on use. |
| Open redirect through `next` | `next` must be a same-site path: one leading slash, no `//`, no backslash, no control character, 512 bytes at most. It is signed into the cookie at the start. Anything else ends at `/signin?error=invalid_next`. |
| Forged or stolen session cookie | HMAC with the session key. A cookie signed with another key is a 401. A stolen one works until it expires (30 minutes idle, 1 hour at most), because there is no denylist. `HttpOnly`, `Secure` and `__Host-` keep script and other sites from reading or setting it. |
| CSRF on a signed-in browser | `SameSite=Strict`, the cross-origin refusal and the CSRF token of ADR 0028. |
| GitHub text, tokens or secrets leaking | Errors are fixed codes. No GitHub body, token, code, verifier, secret or login reaches a response, log or metric. |
| Brute force against the callback | Failed callbacks count against the per-address cap of failed sign-ins (429 with Retry-After). |
| The GitHub access token outliving sign-in | It is revoked in the callback and never stored. |
| A user removed from the organisation | Up to 1 hour of access, the absolute cap. |
| Host header spoofing steering the redirect URI | The redirect URI is built from `GOLDIE_PUBLIC_URL`, never from the request. |

A session cookie also authenticates `GET` on `/mcp`, `/openapi.*` and `/swagger-ui`, like a bearer
token does.

Out of scope: a logout denylist, signing-key rotation without signing everyone out, refresh tokens.
