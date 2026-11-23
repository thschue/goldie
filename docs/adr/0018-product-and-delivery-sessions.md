# 18. A product session and delivery streams replace the three cooperating loops

Status: accepted

## Context

ADR 0012 split the work into three self-paced sessions (issue-worker, review, pm-audit) that hand
off through labels and comment markers. In the first day this produced about 25 product PRs, but
also 11 harness PRs, five of them within three hours (#109, #112, #116, #118, #126). Most of those
patched a race between the sessions: lost or stale `in-review` locks, head drift, red after
arming, a PM decision on a reviewed PR. Each patch added a rule; the rules reached about 1,200 lines
that every tick re-read, and each patch was a gated PR for the maintainer. Meanwhile the worker
starved (one claimable issue out of about 30 `ready-for-agent`), because the original issues were
epic-sized, and review latency (poll, lock, cross-session hand-back) dominated the 27 to 112
minutes from ready to merge.

## Decision

- **Two kinds of session.** One **product session** (`/product`) shapes the work: the
  `product-owner` with `architect` and `ux-designer` subagents for refinement, and a `reviewer` for
  a sampled post-merge audit. **N delivery sessions** (`/deliver`) build it. Each runs at most two
  **streams**.
- **A stream owns one issue end to end** and never hands it to another session:
  - claim by pushing a deterministic branch (`scripts/claim`): the first push wins;
  - acceptance tests from a strict `test-author`;
  - the `implementer`;
  - a fresh-context, read-only `reviewer`, plus `architect`, `ux-designer` or `security-reviewer`
    on narrow path triggers;
  - at most two fix rounds in the same stream;
  - `scripts/merge-pr`: auto-merge, or a direct squash for a PR that is already green.
  Review stays independent because reviewers see artifacts (diff, issue, record), never the
  implementer's reasoning, and they can't edit.
- **Hub and spoke.** Subagents talk only through their orchestrator. A subagent may ask another one
  through a **consult**, logged on the PR. Agent teams and A2A aren't used: they add hidden state outside
  GitHub and erode reviewer independence.
- **Test lock.** In a test file they didn't create, implementers may only add lines. A change needs
  the `test-author`'s approval, recorded as a marker. `scripts/merge-pr` enforces it through
  `scripts/test-lock`.
- **Native tools over rules:**
  - auto-merge pinned to the reviewed head replaces the drift and red-after-arming rules. A merge
    queue isn't available to a user-owned repository, so skew between PRs is caught by CI on
    `main` and the stop-the-line rule;
  - Renovate replaces manual dependency upkeep: green updates merge themselves after the required
    checks and a 7-day release age; the delivery loop fixes red ones (amended 2026-11-23);
  - a branch push replaces claim locks.
- **The harness changes only in batches by the maintainer.** Loops record friction in the pinned
  `Harness feedback` issue and never open harness PRs.
  (A 600-line cap on `docs/agents/*.md` was dropped on 2026-11-23.)
- **One methodology.** Agents and humans follow the existing pattern (cited by file) and test first
  at the highest seam. Every agent has a stance: the architect is opinionated about clean
  architecture but argues from the value it buys; UX and engineers are customer-focused and weigh
  security and architecture.

Kept from ADR 0012: the human gates and `scripts/agent-gate` (run from `main`), the loop-guard hook,
the allowlist, agent definitions as byte copies of the example Items, and GitHub as the only state.
ADR 0013 still holds. Superseded from ADR 0012: the separate reviewer session, the labels
`in-review` and `changes-requested`, self-fix and delta reviews, and the cross-session round cap.

## Consequences

- **Faster and simpler:** handoffs inside a stream take seconds, and review capacity grows with
  each delivery session. The loop rules shrink to the shared README and two loop specs.
- **Stream ownership is per session** (`GOLDIE_SESSION`), because all sessions share one `gh`
  identity. A stream idle for over 2 h may be adopted by another session.
- **The product session can become the bottleneck.** It refines first, and its inbox says when the
  ready buffer stays below 2 × WIP.
- **Same identity:** markers and the test lock stop mistakes, not a determined agent. Branch rules,
  auto-merge and the required checks remain the real controls. A dedicated bot identity is a
  later option for the maintainer.
- **Rollback:** revert the PR that introduced this ADR, which restores the three loops.
