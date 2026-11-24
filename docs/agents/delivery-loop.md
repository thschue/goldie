# Delivery loop

A delivery session turns `ready-for-agent` issues into merged PRs. It runs at most **two streams**.
A stream owns one issue from claim to merge and never hands it to another session. The session's
orchestrator only relays between subagents; it writes no code itself. Shared rules are in the
[README](./README.md).

## Tick

0. **Setup.** Resolve `$me` (on a 401, ask the user to refresh `gh`, and stop). Run
   `scripts/loop-poll deliver`. It prints `main=`, `streams=<n>/2`, `attention=`, `frontier=`
   and `work=`.
   - `main=red`: the only new work is the fix for `main`, as a found-fix stream.
   - `work=0`: report "nothing to do" and stop. The same applies with two streams running and no
     subagent returned.
   - `dry-run`: write nothing, print each write.
   - Rebuild all state from GitHub every tick; never rely on what an earlier tick remembers.
1. **Advance returned subagents**, per stream, through the [stream](#stream) steps.
2. **Own streams.** Every open PR whose latest `claim` marker names this session is its stream
   **until it merges or closes**: red CI, conflicts and answers stay this session's job. Act on its
   `stream_state` (`scripts/lib/markers.jq`):
   - `disarm` (head moved past the clean marker): `gh pr merge <n> --disable-auto` first, then
     review the new commits (step 3) and re-run `scripts/merge-pr`;
   - `red` or `dirty` after a clean review: disarm, resume the implementer (merge `origin/main`,
     fix), then step 3 for the change;
   - `dirty` or `red` on a PR held with `needs-maintainer`: the same, keeping the label. The
     maintainer approves by merging and never gets a conflicted or red PR;
   - `parked` with `needs-maintainer` after 3 tool errors (exit 2): re-run `scripts/merge-pr`
     once per tick, and remove the label when it exits 0;
   - `answered`: remove `needs-info` and `question`; `merge` finishes as clean, `change` goes to
     step 4 (not a round);
   - `active` with no live subagent: continue at the step its markers show.

   Adopt a stream idle for over 2 h ([sessions](./README.md#identity-and-sessions)).
3. **Renovate PRs** merge themselves when green. A red or conflicted one (`renovate=`) is a stream
   with no test step: the implementer fixes it on its branch, then review and `scripts/merge-pr`.
   If the fix exceeds a stream or needs a product choice, close the PR with the reason (Renovate
   skips that version) and file the migration as `needs-triage`.
4. **Claim** while fewer than 2 streams are running: the first issue `scripts/frontier` prints
   ([order of work](./README.md#order-of-work)) that shares no Go package or Vue feature folder
   with this session's other stream. Run `scripts/claim <n> <type>`:
   - it pushes the deterministic branch `<type>/issue-<n>-<slug>`, opens the draft PR, posts the
     `claim` marker, and sets `in-progress` and the assignee;
   - exit 3 means another session won: take the next issue.
5. **Dispatch** each new or resumed step in the background (`isolation: "worktree"`), then end the
   tick so the session stays responsive.
6. **Clean up:** remove a subagent's worktree once its work is pushed or its PR closed. Remove
   `in-progress` and the assignee from issues whose PR merged or closed. Kill only processes this
   tick started, by PID.

**Report:** streams advanced (step and outcome), claims, merges, PRs held or parked (with
reason), consults, test change requests, and harness feedback filed.

## Stream

1. **Acceptance tests** for `feat` and `fix`, found-fix bugs included; skipped for `docs`, `chore`,
   `ci`, `build` and pure `refactor`. For `test` issues the `test-author` makes every test-file
   change here, and step 2 runs only for a remaining non-test change.
   - The `test-author` writes failing tests for every acceptance criterion, at the highest seam,
     following the existing test patterns, and commits them (`test(acceptance): …`) on the
     local ref `tests/<branch>` without pushing: a PR never gets a red commit.
   - If that commit is missing when a stream resumes (another session adopted it), run step 1
     again.
   - Record `<!-- goldie-loop:tests sha=<commit> -->` on the PR.
   - On `needs-info` (criteria unclear or contradictory): comment the exact gap with
     `reason=spec`, swap the issue's `ready-for-agent` for `needs-info`, and close the draft.
2. **Implement.** The `implementer` gets `$me`, the issue, the branch, and the acceptance-test
   commit. Its brief is in its agent definition. For a `shrink:` issue the PR is a `refactor` with
   no step 1; `./run size` must report a negative `net`, which the reviewer checks.
   - **Prune:** once it has shipped, the `test-author` prunes the branch's tests on the pushed head
     (its agent definition says how). Record its commit as
     `<!-- goldie-loop:tests approved=<commit> -->`; only that marker lets the test lock accept a
     deleted test line. Then review that head.
3. **Review.** Always `reviewer`, plus each specialist whose paths the diff touches
   (`git diff --name-only origin/main...<head>`):

   | Reviewer | Paths |
   |---|---|
   | `architect` | a new Go package, `internal/contract/**`, `go.mod`, `.golangci.*`, `docs/adr/**` or `docs/platform-contract.md` |
   | `ux-designer` | `web/**`, `internal/ui/**`, `docs/design/**`, and what users meet in a terminal or an agent: `cmd/goldie/**`, `internal/cli/**`, `internal/mcp/**`, `internal/app/mcp*.go`, `README.md`, `docs/reference/**` |
   | `security-reviewer` (risk paths) | auth, sessions and tokens in `internal/api/`, `internal/mcp/**`, git Sources and sync in `internal/engine/`, `internal/platform/config*`, `goldie pull`, agent guardrails (`.claude/**`, `CLAUDE.md`, `docs/agents/**`, `scripts/**`, `.github/**`) |

   Start them in parallel. Each gets the PR number, the head SHA, the closing issue and the round,
   never the implementer's report. They don't re-run gates; CI is authoritative.
4. **Fix.**
   - The worst outcome wins, in this order: `needs-info`, `changes-requested`, `decision`, `clean`.
   - Send every blocking and every `fix: now` LOW finding to the **same implementer**
     (`SendMessage`; a fresh one after a restart), also when only `now` LOWs are left (a polish
     pass). Then go to step 3 with only the reviewers that had findings, on the new commits.
   - A fix round adds code only for a finding with a failing test for a user-observable defect,
     or one that names the criterion, testing-checklist item or `security-reviewer` finding it
     satisfies (those need no further justification). Any other finding is answered in the marker.
   - At most **2** fix rounds. A blocking finding still open after that: the first time, close the
     PR, remove `in-progress` and the assignee, and comment the findings on the issue, which the
     frontier offers again to a fresh stream; the second time on the same issue,
     `needs-info reason=round-cap` with the findings and a recommendation. LOWs found after a
     polish pass are dropped; `fix: follow-up` LOWs go into one `needs-triage` issue without a
     milestone.
5. **Finish.** Post one marker comment for the head (with `rounds=`), listing the findings by
   agent: fixed, or why not. Then:
   - **clean:** run `scripts/merge-pr <pr> <head>`. It checks the marker and the test lock, marks
     the PR ready, then runs `main`'s gate and merges (auto-merge, or directly when already green).
     Exit 1: the gate holds the ready PR; add `needs-maintainer` with the reasons (for a new dependency, the verified
     brief in three lines). Exit 3: the test lock failed, a blocking finding. Exit 2: a tool error;
     retry the same command on later ticks, and after 3 add `needs-maintainer`;
   - **decision:** the PR is clean except for one product question. Post the marker with the
     question, the options and a recommendation, add `needs-info` and `question` **in the same
     command**, and run `gh pr ready <pr>`: the maintainer reads a PR that is ready to merge but for
     the answer. The answer (`PM decision:`, or the maintainer's comment) is handled in step 2;
   - **needs-info** `security` or `human-check`: post it and stop the stream; it is the
     maintainer's.

## Between subagents

- **Consults.** A report may carry `consult: {to, question, options, recommendation}`.
  - Allowed routes: implementer → `architect`, `ux-designer`, or `test-author` (intent only).
  - Forward the question at once and resume the asker with the answer. Post both as one note
    comment starting with `Consult (<agent>):`.
  - Never route to or from a reviewer during review. A question for the maintainer becomes a
    `needs-triage` issue labelled `question`, linked from the PR; the stream goes on unless the PR
    depends on the answer.
  - After 3 consults on one stream, park it: `needs-info reason=spec`, because the issue wasn't
    ready.
- **Test change requests.** Post the request with `<!-- goldie-loop:tests request -->`, then send
  it to the `test-author`.
  - On approval, it commits the change. Record `<!-- goldie-loop:tests approved=<commit> -->`, then
    resume the implementer.
  - On rejection, record `<!-- goldie-loop:tests rejected -->` with the reason; the implementer
    meets the test as written.
  - A disputed criterion goes to `needs-info reason=spec`.
- **`blocked` reports** (or no JSON block): inspect the branch and worktree, resume the subagent
  once naming what's missing (push, PR body, report). A second `blocked`, or 45 min without a
  commit, file change or report after one status message, parks `needs-info reason=human-check`.

## Routing, size, found-fixes, pacing

- **Models:** the session runs on `sonnet` (`claude --model sonnet`, never a 1M context); the
  agent definitions pin each role's model and effort (`opus` only for `security-reviewer`).
- **Size:** about 400 changed lines without tests and generated code (`./run size`); over 600, a
  slice list (first slice stays, the rest is filed). Same-job fixes never count. Its `net` and
  `ratio` lines are in every PR body and review report: growth is reviewed, not assumed.
- **Found-fixes** become streams of this session when a slot is free; until then, ready drafts. A
  MEDIUM+ security bug on `main` takes the [disclosure](./README.md#disclosure-and-quoting) path in
  the same tick (draft advisory, private fork); tell the user the id.
- **Pacing:** `/loop /deliver`; wake in 2 to 5 min with work, 20 to 30 min at `work=0`. Restart
  daily (same `GOLDIE_SESSION`); streams resume from GitHub.
