# 29. The milestone is the loop's only input; one stage may subtract; growth is counted

Status: proposed

## Context

ADR 0018 made the loops self-sufficient: the product session keeps a ready buffer of at least
2 × WIP issues, every review LOW becomes a follow-up issue, and the metrics are flow metrics
(claim-to-merge, fix rounds, harness share). After 100 merged commits the shape of the output is
+39,753/−2,111 lines, one of them net-negative; tests are 55 % of all lines; #536 shipped 3 lines of
code with 133 of tests; one error sentence cost 3 issues, 3 PRs and +533 lines (#578, #593, #601);
26 of the last 40 PRs filed follow-ups; `internal/api/github` (#704, +969) has no importer outside
its tests. Meanwhile no release exists, and the signed-out `/signin` renders inside the app shell
although the mockup shows a standalone page (#728), after five UX reviews that saw diffs and
signed-in screenshots.

The mechanism, not the rules, produces this. Every stage after refinement can only add: the
test-author sizes tests from criteria text, the test lock forbids removal, fix rounds add code, and
LOWs restart the cycle as new issues. The buffer rule makes the product session manufacture demand,
and follow-ups are its cheapest supply. Test-first by a non-implementer "at the highest seam" asks
for injection points (`MergeClock`, `peerResolver`, `SyncControl`, one implementation each). Nothing
outside the loop says what done is, and nothing counts growth. Rules against over-engineering exist
in three agent definitions and fired once in 40 PRs, then were withdrawn.

## Decision

Three changes to the shape of the pipeline, each enforced by a script or a counter rather than a
sentence:

1. **The milestone is the only input.** A GitHub milestone, created and scoped by the maintainer,
   names the goal and the user journeys that define done. `scripts/frontier` and `scripts/loop-poll`
   count an issue as claimable only when it is in the current milestone (the open one with the
   lowest number) or labelled `security`; `scripts/loop-guard` denies milestone writes through the
   API. The product session refines what is in the milestone, adds an issue to it only as a step of
   a named journey or a `bug` in a shipped journey, and files everything else without a milestone,
   where it is inert until the maintainer pulls it in. The ready-buffer rule is deleted. When the
   milestone has no open issue and the journeys' specs are green on `main`, the inbox says "RC";
   the maintainer releases, closes the milestone and opens the next one.
2. **One stage may subtract.** The `test-author` prunes the branch's tests after implementation
   and before review, as the only role that deletes a test line (its commit is recorded as approved,
   so the test lock stays intact). It writes one test per criterion, with negative and security
   cases only where a criterion, a `security` label or the testing checklist names the risk. A fix
   round adds code only for a failing test of a user-observable defect or a finding that names its
   criterion, checklist item or security finding. The fix-round cap is 2; after it the PR is
   closed and the issue re-queued once with the findings, then parked. The architect's sweep
   returns deletion candidates, filed as `shrink:` issues whose PRs are net-negative.
3. **Growth is counted.** `./run size` prints the net hand-written lines and the test:code ratio of
   a change; they go into every PR body and review report, the product session audits the highest
   ratios first, and the inbox leads with outcomes (journeys green, days since release, net lines
   this week) before flow metrics.

What holds: the `security-reviewer` on risk paths, `scripts/agent-gate`, `scripts/loop-guard`
(which only gains rules), `./run check`, the architect's review, the test lock. A HIGH or MEDIUM
security finding and every testing-checklist item count as a named risk and need no further
justification; the prune pass never deletes the only test of a criterion, a checklist item or a
security case. Harness feedback must name a rule to remove or a script to simplify.

This supersedes ADR 0018's ready buffer, its follow-up filing and its metrics; the session
topology, markers, gates and test lock of ADR 0018 stand.

## Consequences

- **The loop can run dry, by design.** With no claimable issue in the milestone, delivery prints
  `work=0` and the product session lists the outside issues; the maintainer's act is to scope, not to
  review supply. The inbox's "Outside the milestone" section is where ideas wait.
- **A slice is a user-reachable journey step**, so a package nothing imports is not ready, and the
  journeys' specs (`web/e2e/journeys/`, run by `./run ux`) are the executable definition of done
  outside the loop.
- **Fewer lines per criterion.** The prune pass and the "one test per criterion" rule shrink the
  test side; the fix-round rule and the counted ratio make additions visible and reviewable.
- **Reusable kit.** The three mechanisms depend on GitHub milestones, a frontier script and a size
  counter, not on goldie: another repository takes them by copying `scripts/frontier`,
  `scripts/lib/frontier.jq`, the `size` task and the loop specs.
- **Rollback:** revert the PR that introduced this ADR; the buffer rule and the old counters return.
