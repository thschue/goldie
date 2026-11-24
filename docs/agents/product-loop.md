# Product loop

The product session turns the current milestone into small, clear, valuable issues, makes the
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
- `scripts/loop-poll product` prints `triage=`, `decisions=`, `buffer=` (claimable, in the
  milestone), `outside=` (claimable but for a milestone), `wip=` and `fingerprint=`. If the
  fingerprint equals the last ledger entry, report "no change" and stop.

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

3. **Refine the current milestone** ([order of work](./README.md#order-of-work)): its open
   issues become claimable steps, the started epic first, then the next epic by the order of work.
   Never create demand: a follow-up, sweep candidate or idea is filed without a milestone and
   listed in the inbox. An issue enters the milestone only as a `bug` in behaviour already shipped
     (tier 1); the maintainer pulls in the rest. The milestone's text is a goal for humans.
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
   - **UX subagent** for every issue that changes the UI, the CLI or MCP. It returns criteria from
     `docs/design/`, with exact wording. UI work is ready only once the handoff is approved (§13
     ticked); until then it is a `question`.
   - You decide what goes in. Architect and UX input that would delay a valuable outcome becomes a
     follow-up issue with its value stated.
   - Write the issue body from the [template](./issue-tracker.md#issue-template). Set the `area:`
     lane and the priority level, and attach slices and follow-ups to their epic as sub-issues.

   **Readiness gate** (all must pass):
   - it is one step a user can exercise on merge, within about 400 lines;
     a package nothing imports is not ready;
   - nothing merged or decided supersedes it, and no other unblocked issue rewrites its files;
   - measured claims carry their probe;
   - every acceptance criterion is testable at a named seam and needed by this slice's user;
     anything only a later issue needs (caps, digests, flags, edge-case matrices) goes there.
4. **Flow.** Report stalls:
   - a `priority:P1` issue not merged within a day;
   - `round-cap`, `security` and `human-check` parks;
   - `needs-maintainer` older than a day;
   - a stream idle for over 2 h, or an owned PR `red` or `dirty` for over 30 min;
   - a red workflow on `main`.

   Release `in-progress` claims older than 24 h that have no PR, and remove an assignee left on an
   open issue without `in-progress` and without an open PR.
5. **Audit.** Each day, sample about 1 in 5 merged PRs that no gate held, the highest `ratio`
   lines (`./run size`, in the PR body) first, with the `reviewer` in audit mode on the merged diff.
   File each finding as a follow-up without a milestone; a bug in shipped behaviour becomes a
     found-fix issue with `bug` in the milestone, and a security finding takes the disclosure path.
6. **Sweeps,** every 10 merges or 7 days: the `architect` sweeps the audit worktree for deletion
   candidates (at most 3: files, lines removed, ADR), each filed as a `shrink:` issue without a
   milestone; the `ux-designer` walks the app on `main` from a first visit, also after a UI,
   CLI or MCP change. Each dead end is a P1 `bug` in the milestone.
7. **Milestone state.** When the milestone has no open issue: run `./run ux` (the e2e journeys on
   the built binary, `web/e2e/journeys/`) in the audit worktree. Green: "RC for <milestone>" goes
   under Decide, and the loops idle until the maintainer closes it and opens the next. Red: each
   red journey is a P1 `bug` in the milestone.
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
1. **Stop the line:** red `main`, a red journey, no claimable issue while the milestone has open
   ones (the one blocker to clear), `needs-triage` older than one tick.
2. **Merge to approve:** `needs-maintainer` PRs with their gate reasons.
3. **Decide:** tier-3 questions and PRs labelled `question` for the maintainer, `grilling-needed`,
   maintainer-owned `needs-info`, "RC for <milestone>".
4. **Windows closing:** tier-2 decisions, with their UTC deadline.
5. **Outside the milestone:** `<milestone>`: open n, claimable n, outside n. The outside issues
   that are claimable but for the milestone, each with one line: pull in or close.
6. **Harness feedback:** the top 5 open items from the pinned `Harness feedback` issue, ranked by
   occurrences and time lost, for the weekly batch. Close items unconfirmed for 3 weeks.
7. **Merged on judgement:** PRs merged in the last 24 h whose marker judged a `review:` reason not
   significant. The maintainer vetoes by commenting.
8. **Advisories:** ids only.

When a new entry appears in sections 1 to 3, send a push notification when the session can.

## Metrics

Each tick, append a ledger line. Outcomes come first, in the inbox header every day:
- `./run ux` green on `main`, and days since the last release;
- net hand-written lines merged this week and the week's median test:code ratio (the `net` and
  `ratio` lines of `./run size` in each PR body); the top 3 ratios are audited first.

Flow, with `scripts/loop-metrics 1` under "Stop the line" when a target is missed:
- median claim-to-merge under 45 min for PRs no gate held;
- at most 1 fix round on average;
- harness PRs under 10 % of merged PRs over 7 days;
- test-change approval rate between 50 % and 90 %.

Name the worst bottleneck.

## Pacing

Start with `claude --model opus` (never a 1M context) and run `/loop /product`. Wake in 30 min
when something changed, and 60 min otherwise.
