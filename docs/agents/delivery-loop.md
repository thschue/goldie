# Delivery loop

A delivery session turns `ready-for-agent` issues into merged PRs. It runs at most **two streams**.
A stream owns one issue from claim to merge and never hands it to another session. The session's
orchestrator only relays between subagents; it writes no code itself. Shared rules are in the
[README](./README.md).

## Tick

0. **Setup.** Resolve `$me` (on a 401, ask the user to refresh `gh`, and stop). Run
   `scripts/loop-poll deliver`. It prints `main=`, `streams=<n>/2`, `frontier=` and `work=`.
   - `main=red`: the only new work is the fix for `main`, as a found-fix stream.
   - `work=0`: report "nothing to do" and stop. The same applies with two streams running and no
     subagent returned.
   - `dry-run`: write nothing, print each write.
   - Rebuild all state from GitHub every tick; never rely on what an earlier tick remembers.
1. **Advance returned subagents**, per stream, through the [stream](#stream) steps.
2. **Resume:** own open PRs whose latest `claim` marker names this session, with no `review`
   marker for the head and no `needs-info`. A PR with no live subagent continues at the step its
   markers show. Adopt a stream idle for over 2 h ([sessions](./README.md#identity-and-sessions)).
3. **Renovate PRs:** an open, unreviewed `renovate[bot]` PR is a stream with no test or implement
   step. The `reviewer` reads the diff and the release notes; no code from it runs locally; CI is
   authoritative.
4. **Claim** while fewer than 2 streams are running. Build the
   [frontier](./issue-tracker.md#frontier-query), own lane (`area:` matching `GOLDIE_LANE`) first,
   then any lane, in the [order of work](./README.md#order-of-work). Skip an issue that shares a Go
   package or Vue feature folder with this session's other stream. Then run
   `scripts/claim <n> <type>`:
   - it pushes the deterministic branch `<type>/issue-<n>-<slug>`, opens the draft PR, posts the
     `claim` marker, and sets `in-progress` and the assignee;
   - exit 3 means another session won: take the next issue.
5. **Dispatch** each new or resumed step in the background (`isolation: "worktree"`), then end the
   tick so the session stays responsive.
6. **Clean up:** remove the worktrees of merged or closed PRs. Remove `in-progress` from issues
   whose PR merged or closed. Kill only processes this tick started, by PID.

**Report:** streams advanced (step and outcome), claims, merges queued, PRs held or parked (with
reason), consults, test change requests, and harness feedback filed.

## Stream

1. **Acceptance tests** for `feat` and `fix`, found-fix bugs included; skipped for `docs`, `chore`,
   `ci`, `build` and pure `refactor`.
   - The `test-author` writes failing tests for every acceptance criterion, at the highest seam,
     following the existing test patterns, and pushes them as the first commit
     (`test(acceptance): …`).
   - Record `<!-- goldie-loop:tests sha=<commit> -->` on the PR.
   - On `needs-info` (criteria unclear or contradictory): comment the exact gap with
     `reason=spec`, swap the issue's `ready-for-agent` for `needs-info`, and close the draft.
2. **Implement.** The `implementer` gets `$me`, the issue, the branch, and the acceptance-test
   commit. Its brief is in its agent definition.
3. **Review.** `scripts/review-lenses origin/main <head>` names the reviewers: always `reviewer`;
   `architect`, `ux-designer` and `security-reviewer` only on their triggers. Start them in
   parallel. Each gets the PR number, the head SHA, the closing issue and the round number, never
   the implementer's report. They don't re-run gates; CI is authoritative.
4. **Fix.**
   - The worst outcome wins, in this order: `needs-info`, `changes-requested`, `decision`, `clean`.
   - On `changes-requested`, send every blocking finding, and every LOW finding with a clear fix,
     to the **same implementer** (`SendMessage`; a fresh one after a restart). Then go to step 3
     with only the reviewers that had findings.
   - At most **2** fix rounds. A blocking finding still open after that becomes
     `needs-info reason=round-cap` with the findings and a recommendation.
   - LOW findings never cause a round. The ones left are filed as one follow-up issue
     (`needs-triage`).
5. **Finish.** Post one marker comment for the head, listing the findings by agent: fixed, or why
   not. Then:
   - **clean:** run `scripts/merge-pr <pr> <head>`. It checks the marker, `main`'s gate and the test
     lock, marks the PR ready, and queues it. Exit 1 means the gate holds it: add
     `needs-maintainer` and post the reasons (for a new dependency, the verified brief in three
     lines). Exit 3 means the test lock failed: treat it as a blocking finding;
   - **decision:** the PR is clean except for one product question. Add
     `needs-info reason=decision` with the question, the options and a recommendation. When the
     product session answers `PM decision: merge`, finish as clean; on `change`, go to step 4 (it
     doesn't count as a round);
   - **needs-info** `security` or `human-check`: post it and stop the stream; it is the
     maintainer's.

## Between subagents

- **Consults.** A report may carry `consult: {to, question, options, recommendation}`.
  - Allowed routes: implementer → `architect`, `ux-designer`, or `test-author` (intent only).
  - Forward the question at once and resume the asker with the answer. Post both as one comment
    with `<!-- goldie-loop:consult to=<agent> -->`.
  - Never route to or from a reviewer during review.
  - After 3 consults on one stream, park it: `needs-info reason=spec`, because the issue wasn't
    ready.
- **Test change requests.** Post the request with `<!-- goldie-loop:tests request -->`, then send
  it to the `test-author`.
  - On approval, it commits the change. Record `<!-- goldie-loop:tests approved=<commit> -->`, then
    resume the implementer.
  - On rejection, record `<!-- goldie-loop:tests rejected -->` with the reason; the implementer
    meets the test as written.
  - A disputed criterion goes to `needs-info reason=spec`.

## Model routing

`implementer` and `test-author` run on `sonnet`. Use `opus` when the issue's cited paths match a
risk path in `scripts/review-lenses` (auth, tokens, sessions, MCP authorization, git Sources,
Secrets, archive extraction). `reviewer` and `ux-designer` run on `sonnet`; `architect` and
`security-reviewer` on `opus`.

## Size

About 400 changed lines, not counting tests and generated code (`./run size`). Over 600, the
implementer stops and reports a slice list. The stream keeps the first slice, and the product
session files the rest.

## Found-fix and security

- A found-fix PR (from any subagent) becomes a new stream of this session once a slot is free. Until
  then it waits as a ready draft.
- A MEDIUM-or-worse security bug on `main` takes the [disclosure](./README.md#disclosure-and-quoting)
  path in the same tick: draft advisory, private fork, fix pushed there. Tell the user the id.

## Pacing

Run `/loop /deliver`. Wake in 2 to 5 min while a stream is running, and in 20 to 30 min when
`work=0`. Restart the session daily (same `GOLDIE_SESSION`); streams resume from GitHub.
