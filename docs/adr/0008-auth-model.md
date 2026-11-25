# 8. Auth model: named tokens and GitHub, with cookie sessions

Status: accepted

## Context

Goldie must work air-gapped, in CI and for humans, without leaking GitHub access if goldie itself is compromised.

## Decision

There are two roles, `reader` and `operator`. Credentials are named static tokens with roles (for machines), or GitHub: OAuth web flow for the UI, device flow for the CLI, and the MCP authorization flow with goldie as the authorization server (GitHub Enterprise via `GOLDIE_GITHUB_URL`). Members of `GOLDIE_GITHUB_ORGS` become readers and members of `GOLDIE_GITHUB_OPERATOR_TEAMS` operators, re-checked on refresh. Goldie issues its own short-lived signed sessions plus refresh tokens; GitHub tokens never leave the server. Browser sessions live only in hardened cookies (`__Host-`, HttpOnly, Secure, SameSite=Strict) with PKCE, single-use state, an exact redirect allowlist, CSRF protection, a strict CSP and revocation. With no GitHub configured, goldie runs on tokens only. Authorisation is org-level in v1. ADR 0032 narrows the browser flow: no refresh tokens, no per-session revocation, and the membership re-check is a one-hour session cap.

## Consequences

A goldie compromise does not leak GitHub access, XSS cannot steal a token, and air-gapped use works. The cost is that goldie runs its own session layer (signing keys with rotation, refresh, revocation) and that finer-grained, per-repository visibility is deferred.
