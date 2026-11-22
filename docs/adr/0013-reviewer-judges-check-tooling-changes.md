# 13. The reviewer judges changes to the task runner

Status: accepted

## Context

ADR 0012 gated every change to `run`, because the PR's own `run` defines what green CI means and a
PR could weaken it. Most changes to `run` only add a task or a step (generated docs in #58,
TypeScript types, the UX job), so the maintainer was asked about changes that weren't significant.

## Decision

`scripts/agent-gate` reports a change to `run` as a `review:` reason, not a hard `gate:` reason.
The reviewer holds the PR for the maintainer only when the change is significant: an existing check
removed, skipped or unable to fail, a check made less strict, new network access or downloaded
code, or a diff it can't judge. A new tool pinned in `run` stays a hard reason. The loop tooling in
`scripts/` stays a hard reason too, since it is the gate itself.

## Consequences

The maintainer sees only significant changes to the checks. The cost: one weakening the reviewer
misreads would merge. Two things limit that. The reviewer has to say in its marker why each
`review:` reason isn't significant, and the maintainer inbox lists PRs merged on judgement for an
asynchronous veto.
