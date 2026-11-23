# 23. Git Source URLs are operator-trusted

Status: accepted (maintainer decision, 2026-11-23, #258)

## Context

A git Source fetches from the URL in `GOLDIE_GIT_URL`, so the server makes outbound requests to a
host someone chose: the classic shape of server-side request forgery (SSRF). Who chooses decides the
risk. Today only the operator does, through the environment (PC-04), and the main case is a
self-hosted git server behind a firewall, on a private or internal address. ADR 0004 and #7 plan
Declarative Sources, whose URLs would come from content or the API, not from the operator.

Two options were weighed:

- (a) Source URLs are operator-trusted: accept any https or ssh host, private ranges included.
- (b) Block private, loopback and metadata ranges by default, with `GOLDIE_GIT_ALLOW_PRIVATE=true`
  to opt out.

(b) would break the main case by default and protect only against an operator who can already set
every other variable of the process.

## Decision

Option (a): git Source URLs are operator-trusted.

- Any https or ssh host is accepted, private and internal ranges included.
- Redirects are followed, but credentials are dropped when a redirect leaves the origin, and a
  redirect to http is refused.
- file:// is refused unless `GOLDIE_GIT_ALLOW_FILE_URL=true`, so a repository on the server's own
  filesystem is an explicit choice.
- URLs supplied by anyone other than the operator, such as future Declarative Sources (ADR 0004,
  #7), need a new decision before they ship.

The startup report logs `GOLDIE_GIT_URL` with its userinfo, query and fragment redacted, whether or
not it parses, and the token, SSH key and known hosts are Secret settings with a `_FILE` form
(PC-05, PC-06).

## Consequences

An operator can point goldie at any host the process can reach, including cloud metadata endpoints
and internal services; that is their call, as it is for every other outbound setting. The
`GOLDIE_GIT_URL` setting text states the stance, so it is visible where the URL is configured.
Credentials never follow a redirect to another origin or over plain http. Declarative Sources cannot
reuse this trust: before they ship, a new ADR decides how URLs from other parties are checked
(likely option (b) or an allow list), and supersedes this one for them.
