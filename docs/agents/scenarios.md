# Loop scenarios

Known situations and the writes the loops must make: the regression tests for the loop rules. A
harness PR runs the affected loops with `dry-run` and puts the `WOULD …` lines in its body. A PR
number names the case that motivated the rule.

| # | Situation | Session | Expected writes |
|---|---|---|---|
| S1 | Two delivery sessions claim the same issue at once | delivery | One `scripts/claim` wins; the other exits 3 and claims the next issue. One draft PR, one `claim` marker |
| S2 | Session restarted with a stream at "review" (draft PR, tests and implementation pushed, no `review` marker) | delivery | Resumed at step 3 with fresh reviewers; nothing re-claimed |
| S3 | Stream of another session idle for over 2 h | delivery | New `claim` marker for this session, then resumed |
| S4 | `feat` issue, criteria clear | delivery | `test(acceptance)` commit and `tests sha=` marker, then implementer, reviewers, clean marker, `scripts/merge-pr` |
| S5 | Implementer wants to change an acceptance-test assertion | delivery | `tests request` marker → `test-author`: `tests approved=` (its own commit) or `tests rejected`; never an implementer edit |
| S6 | Implementer edited a `main` test line without approval | delivery | `scripts/merge-pr` exits 3; a blocking finding, back to the implementer |
| S7 | Reviewer finds one blocking and two LOW findings | delivery | Same implementer fixes them (`SendMessage`); only that reviewer re-runs; clean, then merge |
| S8 | Blocking finding still open after 2 fix rounds | delivery | `review reason=round-cap` marker, `needs-info reason=round-cap`; inbox "Decide" |
| S9 | Clean review, gate exits 1 (new direct dependency, verified brief, #50) | delivery | `needs-maintainer`, marker lists the reasons and the brief; nothing queued |
| S10 | Clean review, `run` gains a task (#58) | delivery | Gate exits 0 with `review: check tooling changed: run`; the marker says why it isn't significant; queued |
| S11 | PR adds `\|\| true` to an existing step in `run` | delivery | Significant: `needs-maintainer` |
| S12 | Diff adds a package under `internal/` and touches `internal/api/auth.go` | delivery | `reviewer`, `architect` and `security-reviewer` from the review table; the implementer ran on `opus` |
| S13 | Implementer unsure which module owns a function | delivery | a `Consult (architect):` note with question and answer; implementer resumed; 4th consult parks `needs-info reason=spec` |
| S14 | Clean except a non-additive product question | delivery | `review reason=decision`, `needs-info reason=decision`; product answers `PM decision: merge`; merge-pr on the same head |
| S15 | `ci` red on `main` | delivery, product | Delivery claims nothing new, fixes `main` as a found-fix stream; product puts it first in the inbox |
| S16 | Renovate PR bumps a Go module | delivery | `reviewer` reads diff and notes, no local code run; clean, merge-pr; an Actions bump is held by the gate |
| S17 | Subagent report without a valid JSON block | delivery | Treated as `blocked`; no markers or labels written from it |
| S18 | Reviewer reproduces a MEDIUM bug on `main` | delivery | Draft advisory, fix pushed to its private fork; the id to the user; nothing public |
| S19 | Buffer is 3, target 8; issues #6–#34 are epic-sized | product | Epics sliced into sub-issues with `blocked_by` chains, each with Acceptance, Pattern to follow and `area:`; audit skipped this tick |
| S20 | Several LOW follow-ups in one area (#89–#91) | product | Bundled into the oldest issue as a checklist; the rest closed as duplicates |
| S25 | Frontier: a new `security` follow-up, a slice of started epic #11, and an older slice of unstarted epic #161 (#170, #101, #164) | delivery | `scripts/frontier` order: #101, then #170, then #164; the session claims #101 |
| S21 | Question the record answers (`/version` vs PC-01) | product | Tier 1: decided and filed ready; no `question` |
| S22 | UI issue whose handoff isn't approved (#21) | product | `question` to approve the handoff; inbox "Decide" |
| S23 | Architect wants a refactor that would delay a `priority` issue | product | Issue stays sliced as is; refactor filed as a follow-up with its value |
| S24 | A loop finds a missing rule | any | One comment on `Harness feedback`; no harness PR; listed in the inbox top 5 |
