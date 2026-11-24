# Loop scenarios

Known situations and the writes the loops must make: the regression tests for the loop rules. A
harness PR runs the affected loops with `dry-run` and puts the `WOULD …` lines in its body. A PR
number names the case that motivated the rule.

| # | Situation | Session | Expected writes |
|---|---|---|---|
| S1 | Two delivery sessions claim the same issue at once; or `origin` is unreachable | delivery | One `scripts/claim` wins; the other exits 3 and claims the next issue. One draft PR, one `claim` marker. Unreachable `origin`: exit 2, never reported as lost |
| S2 | Session restarted with a stream at "review" (draft PR, tests and implementation pushed, no `review` marker) | delivery | Resumed at step 3 with fresh reviewers; nothing re-claimed |
| S3 | Stream of another session idle for over 2 h | delivery | New `claim` marker for this session, then resumed |
| S4 | `feat` issue, criteria clear | delivery | `test(acceptance)` commit and `tests sha=` marker, then implementer, reviewers, clean marker, `scripts/merge-pr` |
| S5 | Implementer wants to change an acceptance-test assertion | delivery | `tests request` marker → `test-author`: `tests approved=` (its own commit) or `tests rejected`; never an implementer edit |
| S6 | Implementer edited a `main` test line without approval | delivery | `scripts/merge-pr` exits 3; a blocking finding, back to the implementer |
| S7 | Review: one blocking and two `fix: now` LOWs; or only `now` LOWs (#345–#349) | delivery | Same implementer fixes all of them (`SendMessage`), a polish pass if only LOWs; only those reviewers re-check; no follow-up issue |
| S8 | Blocking finding still open after 2 fix rounds | delivery | First time: PR closed, `in-progress` and assignee removed, findings commented on the issue, which the frontier offers again. Second time on the same issue: `review reason=round-cap` marker, `needs-info reason=round-cap`; inbox "Decide" |
| S9 | Clean review, gate exits 1 (new direct dependency, verified brief, #50) | delivery | PR marked ready, `needs-maintainer`, marker lists the reasons and the brief; nothing merged |
| S10 | Clean review, `run` gains a task (#58); another PR adds `\|\| true` to an existing step | delivery | First: `review:` reason judged not significant in the marker, merged. Second: significant, `needs-maintainer` |
| S12 | Diff adds a package under `internal/` and touches `internal/api/auth.go` | delivery | `reviewer`, `architect` and `security-reviewer` from the review table |
| S13 | Implementer unsure which module owns a function | delivery | a `Consult (architect):` note with question and answer; implementer resumed; 4th consult parks `needs-info reason=spec` |
| S14 | Clean except a non-additive product question; answered by the product session or the maintainer (#180) | delivery | `review reason=decision` with `needs-info` and `question` in one command, PR marked ready; on the answer, both labels off and merge-pr on the same head |
| S26 | Clean and armed, then `main` moves and CI goes red (#195) | delivery | `red`: auto-merge disabled before the implementer fixes it; the new commits are reviewed and `scripts/merge-pr` re-runs, test lock included |
| S15 | `ci` red on `main` | delivery, product | Delivery claims nothing new, fixes `main` as a found-fix stream; product puts it first in the inbox |
| S16 | Renovate PR, green; or red after a major bump (#320, #339) | delivery | Green: nothing, it merges itself. Red: a stream fixes it on the branch, review, merge-pr (a major bump isn't a new dependency); too big: closed with the reason, migration filed |
| S17 | Implementer ends without a JSON block and with commits unpushed (#141, #153) | delivery | Nothing written from the report; the same implementer is resumed once to push, write the PR body and report; a second `blocked` parks `needs-info reason=human-check` |
| S18 | Reviewer reproduces a MEDIUM bug on `main` | delivery | Draft advisory, fix pushed to its private fork; the id to the user; nothing public |
| S19 | The milestone holds 3 epic-sized issues; 20 `ready-for-agent` issues sit outside it | product | The 3 sliced into sub-issues in the milestone with `blocked_by` chains, each with Acceptance, Pattern to follow and `area:`; the 20 stay outside, listed in the inbox with pull-in or close; none enters the milestone |
| S20 | Loop-filed follow-ups in one area (#89–#91), one pure wording polish (#329) | product | The polish closed `wontfix` "overengineered: …" (tier 1); the rest bundled into the oldest issue with a P level, duplicates closed; no milestone set |
| S28 | The milestone has no open issue; or its last issue merged and a journey spec is red (ADR 0029) | product, delivery | Journeys run in the audit worktree. Green: "RC for <milestone>" under Decide; delivery prints `work=0`. Red: each red journey a P1 `bug` in the milestone |
| S29 | A reviewer's `fix: follow-up` LOW; an architect sweep candidate | delivery, product | One `needs-triage` issue (or a `shrink:` issue) without a milestone; `scripts/frontier` doesn't print it; inbox "Outside the milestone" lists it |
| S30 | Fix round: a reviewer asks for a cap or a retry no criterion, checklist item or security finding names | delivery | No code; the marker answers the finding with evidence; the round ends |
| S31 | Implementer shipped; two tests on the branch assert the same behaviour | delivery | `test-author` commits `test(prune): …` on the branch; `tests approved=<commit>` marker; review runs on that head; `scripts/merge-pr` passes the test lock |
| S32 | A `security` issue outside the milestone; a `priority:P1` issue outside it | delivery | `scripts/frontier` prints the `security` issue, never the P1; the session claims the `security` one |
| S33 | Clean review, test lock passes, gate exits 1 (#747) | delivery | `merge-pr` marks the PR ready before the gate verdict; a `needs-maintainer` PR is never a draft. Test lock fails: stays a draft, exit 3 |
| S27 | A stream adds a git URL redactor; an engine sibling leaks the same way (#383, #386, #392) | delivery, product | Both fixed in the one PR (same job, any size); no found-fix PR. A follow-up that only matters once the URL is logged is folded into the issue that logs it (#96) |
| S25 | Frontier: a new `security` follow-up, a slice of started epic #11, and an older slice of unstarted epic #161 (#170, #101, #164) | delivery | `scripts/frontier` order: #101, then #170, then #164; the session claims #101 |
| S21 | Question the record answers (`/version` vs PC-01) | product | Tier 1: decided and filed ready; no `question` |
| S22 | UI issue whose handoff isn't approved (#21) | product | `question` to approve the handoff; inbox "Decide" |
| S23 | Architect wants a refactor that would delay a P1 issue | product | Issue stays sliced as is; refactor filed as a follow-up with its value |
| S24 | A loop finds a missing rule | any | One comment on `Harness feedback`; no harness PR; listed in the inbox top 5 |
