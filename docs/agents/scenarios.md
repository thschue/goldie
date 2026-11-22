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
| S5 | S4 after the delta review finds the fix incomplete | review | No second self-fix: `changes-requested`, back to draft, a marker with `reason=changes-requested` |
| S6 | External ready PR, clean review | review | No code run; `needs-maintainer` with the reason "external author"; never armed |
| S7 | Armed PR whose head moved past the reviewed SHA | review | Disable auto-merge, remove `needs-maintainer` if set, re-queue the PR |
| S8 | `ci` red on the head of `main` | issue-worker | No new claim; a found-fix PR for `main` first |
| S9 | `release-please` red on `main`, `ci` green | pm-audit | Inbox section "Stop the line" lists it; the other loops carry on |
| S10 | PR touches `internal/cli` and moves Resolution logic there | review | Architecture lens: a blocking finding citing ADR 0002; outcome `changes-requested` |
| S11 | PR changes a UI screen's copy away from handoff §8 | review | UX lens: a blocking finding; fix it, or change the handoff (which the `docs/design/**` gate sends to the maintainer) |
| S12 | `ready-for-agent` UI issue whose handoff isn't approved (#21) | pm-audit | `question` asking the maintainer to approve the handoff; an inbox "Decide" entry |
| S13 | PR removes a step from `./run check` | review | The gate prints `step removed from ./run check`; `needs-maintainer` |
| S14 | Subagent report without a valid JSON block | any | Treated as `blocked`; no labels or markers written from it |
