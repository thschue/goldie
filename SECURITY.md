# Security policy

## Reporting a vulnerability

**Please don't open a public issue for a security problem.**

Report it privately through GitHub instead: open the repository's **Security** tab and choose
**Report a vulnerability**. The report reaches the maintainers through a private security advisory,
where we can discuss it, prepare a fix and coordinate disclosure with you.

Please include:
- the affected component (API, MCP, CLI, UI, Helm chart, image) and version or commit;
- what an attacker can achieve, and what they need first;
- a minimal reproduction, if you have one.

## What to expect

goldie is maintained on a best-effort basis; there is no formal SLA. We aim to acknowledge a report
within a few working days, keep you updated in the advisory, and credit you in the published
advisory unless you'd rather stay anonymous.

## Supported versions

Before 1.0, only the latest release gets security fixes.

## Severity

We rate reports with this scale. The automated review in this repository uses it too: findings rated
MEDIUM or worse are only ever handled in private advisories.

| Level | Meaning |
|---|---|
| CRITICAL | Remote, unauthenticated compromise or secret disclosure |
| HIGH | Authenticated privilege escalation, cross-boundary data access, or account takeover |
| MEDIUM | Exploitable with preconditions, or a weakened security control |
| LOW | Hardening, defence in depth, or information disclosure without a direct exploit |
