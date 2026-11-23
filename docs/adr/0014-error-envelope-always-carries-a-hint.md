# 14. The error envelope always carries a next-step hint

Status: accepted

## Context

Every error the API returns uses the envelope `{"error":{code,message,details}}` (CLAUDE.md). #1
(story 107 and "Errors") puts next-step hints in `details` and keeps internal details out of errors.
#47 first called `details` optional; pm-audit struck that as a misreading of #1. #47 and
`docs/testing-checklist.md` ask that every error carry a next-step hint. The spec has had `details`
required since #50, as an open object. Under ADR 0011 the shape becomes permanent at the first
release, so the choice has to be made before `0.1.0`. pm-audit settled it on PR #59: `details` is
required and every error carries a non-empty hint.

## Decision

- `Error.details` is **required**, and its type is the `ErrorDetails` schema.
- `ErrorDetails.hint` is a **required**, non-empty string: what the caller can do next. A handler
  with no specific hint gives a generic one.
- `details` holds next-step guidance only. It never echoes the request and never contains
  addresses, ports, hostnames, file paths or stack traces. A test in `internal/app` checks this
  against hostile input.
- `ErrorDetails` is **closed** (`additionalProperties: false`): any new key needs a spec change and
  review. Rejected: an **open** `ErrorDetails`, where "hints only" would rest on the leak test and
  review rather than the schema.
- Servers build the envelope from the generated `contract.ErrorEnvelope` type, not from ad-hoc maps.
- Scope: the public listener (`GOLDIE_ADDR`), which the spec describes. The admin port's errors
  (`internal/platform`) are outside the spec and this decision; `platform` cannot import `contract`.

## Consequences

- Clients can always read `error.details.hint` without a nil check, and agents always get a next
  step.
- This supersedes "`details` is optional" in #47's original wording.
- Because the schema is closed, adding a field later breaks clients that validate against an older
  spec.
- Making `hint` optional after the first release would need a new API version (ADR 0011).
- The maintainer confirms the closed choice at merge (pm-audit left it to them).
