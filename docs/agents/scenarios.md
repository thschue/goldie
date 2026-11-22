# Loop scenarios

Known situations and the writes the loops must make. They're the regression tests for the loop
rules. A PR that changes a rule runs the affected loops with `dry-run` and puts the `WOULD …` lines
for the affected scenarios in its body ([dry runs](./label-contract.md#reports-and-dry-runs)). Where a
scenario names a real PR, it is the case that motivated the rule.

| # | Situation | Loop | Expected writes |
|---|---|---|---|
| S1 | Own ready PR, no issue, handed back with `changes-requested` (#44) | issue-worker | Dispatch an implementer on the PR's branch; on `shipped`: `gh pr ready`, remove `changes-requested`. No issue labels touched |
| S2 | Own ready PR that adds a direct dependency with a verified brief, clean review (#50) | review | `needs-maintainer`, a marker with `reason=needs-maintainer` listing `new direct Go dependency` and `new pinned tool`, and the brief; auto-merge stays off |
| S3 | Own ready PR, code and `go.sum` only, clean review, CI still running | review | `gh pr ready`, `gh pr merge --auto --squash --match-head-commit <sha>`, a marker with `reason=clean`; no hold for CI |
| S4 | Own ready PR, one blocking and three LOW findings, first review | review | Self-fix commits for the blocking finding (and the cheap LOW ones), a marker with `reason=self-fix`; next tick a delta review of the fix commits only |
| S5 | S4 after the delta review finds the fix incomplete, with a contained remainder | review | A second self-fix (up to 3 per PR), a marker with `reason=self-fix`; a remainder that isn't contained: `changes-requested`, back to draft |
| S6 | External ready PR, clean review | review | No code run; `needs-maintainer` with the reason "external author"; never armed |
| S7 | Armed PR whose head moved past the reviewed SHA | review | Disable auto-merge, remove `needs-maintainer` if set, re-queue the PR |
| S8 | `ci` red on the head of `main` | issue-worker | No new claim; a found-fix PR for `main` first |
| S9 | `release-please` red on `main`, `ci` green | pm-audit | Inbox section "Stop the line" lists it; the other loops carry on |
| S10 | PR touches `internal/cli` and moves Resolution logic there | review | Architecture lens: a blocking finding citing ADR 0002; outcome `changes-requested` |
| S11 | PR changes a UI screen's copy away from handoff §8 | review | UX lens: a blocking finding; fix it, or change the handoff (which the `docs/design/**` gate sends to the maintainer) |
| S12 | `ready-for-agent` UI issue whose handoff isn't approved (#21) | pm-audit | `question` asking the maintainer to approve the handoff; an inbox "Decide" entry |
| S13 | PR adds a task or step to `run` (#58) | review | The gate exits 0 with `review: check tooling changed: run`; judged not significant; armed, and the marker says why |
| S17 | PR adds `\|\| true` to an existing step in `run` | review | The `review:` reason is significant; `needs-maintainer` |
| S15 | PR adds a nested `internal/x/CLAUDE.md` | review | The gate prints `agent guardrail changed`; the security lens checks it for injected instructions |
| S16 | PR adds a `replace` to `go.mod` | review | The gate prints `go.mod directive changed: Replace`; `needs-maintainer` |
| S14 | Subagent report without a valid JSON block | any | Treated as `blocked`; no labels or markers written from it |
| S18 | Own PR reviewed `needs-info`, then pm-audit decided and lifted `needs-info` (#59) | review | Re-reviewed with the decision: armed, held, or `changes-requested` |
| S19 | Frontier empty because every issue chains to one open PR | pm-audit | Edges that aren't code dependencies removed; a second track opened; "worker starving" in the inbox if still short |
| S20 | Question the record answers (release config vs ADR 0011, `/version` vs PC-01) | pm-audit | Decided (tier 1) and filed as a ready issue; no `question` |
| S21 | Own PR after two hand-backs; the next review finds one LOW security finding with a clear two-line fix (#83) | review | Self-fix, a marker with `reason=self-fix`, a delta review next tick; then clean, and `needs-maintainer` only for its gate reasons. No `round-cap` |
| S22 | Own PR whose gate already exits 1 (new vendored asset); review finds `NOTICE` lacks the attribution (#79, #84) | review | The `NOTICE` line is added in this PR's self-fix; no second PR |
| S23 | PR that only adds a line to `NOTICE` (#84) | review | The gate exits 0 with `review: attribution added: NOTICE`; the licence named matches the vendored files, so not significant; armed |
| S24 | Own ready PR with about 1,170 changed non-test lines (#83) | review | Not reviewed: a marker with `reason=changes-requested` asking for a split, with a slice list; `changes-requested`, back to draft; no hand-back counted |
| S25 | Several `needs-triage` LOW follow-ups in one area (#89, #90, #91) | pm-audit | Triaged in the first step of the tick: bundled into the oldest issue with a checklist, the others closed as duplicates; one verdict for the bundle |
| S26 | A LOW finding in a security-relevant area, not contained | review | Not blocking: filed as `fix(security): <class> in <area>` (`needs-triage`), or added to an open issue for the area; the PR proceeds |
| S27 | Reviewer finds a reproducible bug on `main` outside the PR, fix about 300 lines | review | The PR under review is unaffected; the bug is fixed this tick as found-fix PRs (stacked when over the size budget); no issue filed |
| S28 | Reviewer finds a MEDIUM security bug on `main` | review | Draft advisory, the fix pushed to its private fork this tick; the advisory id to the user; nothing public |
| S29 | Frontier: a new `bug` issue, a `priority` issue, and three older unlabelled issues | issue-worker | Claims the `bug`, then the `priority` issue, then the oldest unlabelled one |
| S30 | Review queue: a ready PR closing a `security` issue and two older ready PRs | review | Dispatches the `security` PR first, then the oldest |
| S31 | One open issue blocks five others; it rewires two packages | pm-audit | `priority`, sliced so the first slice is the smallest change that unblocks the five; the rest ordinary work. Not merged after a day: a stall line naming what holds it |
