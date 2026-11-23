# Product loop

The product session keeps the delivery sessions fed with small, clear, valuable issues, makes the
product calls the record allows, audits what merged, and keeps the **Maintainer inbox**, the one
place the maintainer reads. **It writes no code, pushes nothing and merges nothing.** Shared rules
are in the [README](./README.md).

## Write surface

- **Issues:** bodies, comments, triage labels, `area:` lanes, priority levels, native `blocked_by`
  edges and sub-issues; creating issues and closing them with evidence; the pinned
  `Maintainer inbox` and `PM decision log`.
- **PRs:** comments, lifting a `needs-info` it owns (`spec`, `product`, `decision`), and closing a
  PR whose issue is closed or superseded (never an external contributor's).
- **Never:** code, branches, merges, or another session's flow labels.

## Setup

- Resolve `$me`. `dry-run` means: write nothing, print each write.
- **Audit worktree:** `git worktree add --detach .claude/worktrees/product origin/main`, or reset
  it to a freshly fetched `origin/main`. It is read-only.
- **Ledger:** `product-ledger.md` in the session scratchpad, append-only, read first. If it's lost,
  re-audit once.
- `scripts/loop-poll product` prints `triage=`, `decisions=`, `buffer=`, `wip=` and `fingerprint=`.
  If the fingerprint equals the last ledger entry, and the buffer is at target, report "no change"
  and stop.

## Tick: refinement first

Each step runs only after the steps before it are done.

1. **PM decisions.** A PR whose `reason=decision` marker for its head has no answer yet
   (`decisions=`, with or without its labels) gets `PM decision: merge` or `PM decision: change`
   (with the rework and the record it rests on) in this tick, and `needs-info` and `question` if
   they're missing. Log it. Escalate only the critical list.
2. **Triage** every `needs-triage` issue, and every `ready-for-agent` one without a priority level,
   bugs and security first.
   - **Value check:** is the user, security or maintenance benefit worth a stream (about 30 min of
     agents and a review)? If not, close it `wontfix`: "overengineered: <why>" (the architect's view
     for a refactor). Tier 1 for issues the loops filed, tier 2 for the maintainer's.
   - Fold each follow-up into the open issue that touches the same code or first makes it matter
     (any area), else bundle by area. Then one verdict, plus a priority level with `ready-for-agent`:

   | Verdict | When |
   |---|---|
   | `ready-for-agent` | passes the readiness gate below, and the author is `OWNER`, `MEMBER` or `COLLABORATOR` |
   | `grilling-needed` | a design question; comment the questions, each with a recommended answer |
   | `ready-for-human` | needs credentials, a real cluster or release operations |
   | `needs-info` | name the exact missing fact |
   | `wontfix` | cite the decision (product grounds: tier 2) |
   | `question` | the critical list; ask the maintainer |

3. **Refine until the buffer holds:** at least **2 × WIP** claimable issues (WIP = 2 streams × the
   number of delivery sessions). The started epic comes first: keep at least 2 of its slices
   claimable so both streams can finish it; then the next epic by the order of work.
   - **Slice** epics and large issues into sub-issues chained by `blocked_by`, so the first slice
     needs nothing open. An issue labelled `ready-for-agent` that fails the size check is sliced in
     this tick.
   - **Challenge edges:** keep `blocked_by` only where the blocked issue can't be built without the
     blocker's code.
   - **Architect subagent** for every issue that touches Go modules or the contract. It returns:
     - the module and seam;
     - **Pattern to follow** (an existing file to copy the approach from);
     - edge cases worth a test;
     - an ADR draft when the issue makes a hard-to-reverse choice.
   - **UX subagent** for every issue that builds UI. It returns the criteria from
     `docs/design/ui-handoff.md`: screen, states, copy, breakpoints, accessibility. UI work is ready
     only once the handoff is approved (§13 ticked); until then it is a `question`.
   - **UX subagent** also for every issue that changes what a user meets in a terminal or an agent
     meets over MCP: commands, help, errors, output, tools, prompts, instruction blocks. It returns
     the criteria from `docs/design/cli-and-agent-experience.md`, with the exact wording.
   - You decide what goes in. Architect and UX input that would delay a valuable outcome becomes a
     follow-up issue with its value stated.
   - Write the issue body from the [template](./issue-tracker.md#issue-template). Set the `area:`
     lane and the priority level, and attach slices and follow-ups to their epic as sub-issues.

   **Readiness gate** (all must pass):
   - it changes one Go package or Vue feature folder, within about 400 lines;
   - nothing merged or decided supersedes it;
   - no other unblocked issue rewrites the same files;
   - measured claims carry their probe;
   - every acceptance criterion is testable at a named seam.
4. **Flow.** Report stalls:
   - a `priority:P1` issue not merged within a day;
   - `round-cap`, `security` and `human-check` parks;
   - `needs-maintainer` older than a day;
   - a stream idle for over 2 h, or an owned PR `red` or `dirty` for over 30 min;
   - a red workflow on `main`.

   Release `in-progress` claims older than 24 h that have no PR.
5. **Audit** (only when the buffer is full). Each day, sample about 1 in 5 merged PRs that no gate
   held, with the `reviewer` in audit mode on the merged diff. File each finding as a follow-up; a
   bug becomes a found-fix issue with `bug`, and a security finding takes the disclosure path.
6. **First-run journey** (only when the buffer is full), every 10 merges or 7 days, and after any
   merge that changes the CLI or MCP surface: the `ux-designer` subagent walks the first run in
   `docs/design/cli-and-agent-experience.md` §2.4 on a binary built from `main`, with an empty
   environment in an empty directory. File each dead end as a `bug` at `priority:P1`, the rest as
   follow-ups.
7. **Architecture sweep** (only when the buffer is full), every 10 merges or 7 days: the
   `architect` subagent in sweep mode on the audit worktree. File its candidates (at most 3) with
   files, pattern and ADR.
8. **Inbox and metrics.** Rewrite the inbox, then append to the ledger.

## Decision rights

The record is spec #1, `CONTEXT.md`, `docs/adr/`, `docs/design/` and `docs/platform-contract.md`.
**Decide by default:** the maintainer's attention is the scarcest resource.

| Tier | When | Action |
|---|---|---|
| 1 | The record answers it, even indirectly; triage, order, slicing, edges, criteria that follow from the record, closing fixed work | Act, then one comment and a decision-log entry |
| 2 | Not in the record, but reversible: an API shape before the first release, `wontfix` on product grounds, re-scoping | Comment "PM decision: <what>, acting on <UTC time> unless the maintainer objects" (8 h), list it in the inbox, then act |
| 3 | The [critical list](./README.md#humans-decide) | Add `question`, comment one either/or with a recommendation, list it in the inbox |

A maintainer override is precedent. Every fix direction names the root cause and the existing
pattern it follows, by file.

## Maintainer inbox

One pinned issue, `Maintainer inbox`. Its body is rewritten every tick; never comment on it. Each
entry is 2 to 3 lines with a link: what, why it needs the maintainer, the recommended action. The
sections, in order:
1. **Stop the line:** red `main`, a buffer below target (claimable vs target, and the one blocker
   to clear), `needs-triage` older than one tick.
2. **Merge to approve:** `needs-maintainer` PRs with their gate reasons.
3. **Decide:** tier-3 questions and PRs labelled `question` for the maintainer, `grilling-needed`,
   maintainer-owned `needs-info`.
4. **Windows closing:** tier-2 decisions, with their UTC deadline.
5. **Harness feedback:** the top 5 open items from the pinned `Harness feedback` issue, ranked by
   occurrences and time lost, for the weekly batch. Close items unconfirmed for 3 weeks.
6. **Merged on judgement:** PRs merged in the last 24 h whose marker judged a `review:` reason not
   significant. The maintainer vetoes by commenting.
7. **Advisories:** ids only.

When a new entry appears in sections 1 to 3, send a push notification when the session can.

## Metrics

Each tick, append a ledger line. Daily, put `scripts/loop-metrics 1` in the inbox under "Stop the
line" when a target is missed:
- median claim-to-merge under 45 min for PRs no gate held;
- at most 1 fix round on average;
- harness PRs under 10 % of merged PRs over 7 days;
- test-change approval rate between 50 % and 90 %.

Name the worst bottleneck. When the buffer stays below target for 2 days, propose in the inbox to
split the audit into its own session (a harness decision).

## Pacing

Start with `claude --model opus` (never a 1M context) and run `/loop /product`. Wake in 30 min
while the buffer is below target, and 60 min when it's full.
