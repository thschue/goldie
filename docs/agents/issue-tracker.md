# Issue tracker: GitHub

Issues, specs and PRs for goldie live on GitHub in `thschue/goldie`. Use the `gh` CLI for every
operation; inside a clone it infers the repository from the remote.

## Conventions

- **Sequencing lives in GitHub, not in a roadmap file.** Architecture decisions go in `docs/adr/`.
  Issue-specific context goes in the issue body and comments. Blocking relationships are native
  issue dependencies, and an epic's parts are native sub-issues.
- **One PR closes exactly one issue** (`Closes #<n>` in the body). The exception is a
  [found-fix PR](./README.md#fix-it-now), which fixes one bug found while working and closes no
  issue. Open it as a **draft before the first line of code** (`scripts/claim` does this for the
  loops), title it with a Conventional Commit type, and **squash-merge** it.
- Before a PR leaves draft, work through `docs/testing-checklist.md`, run the built-in
  `/security-review` on your diff, and put the reasons of `./run gate origin/main HEAD` in the body.

## Recipes

```sh
# read
gh issue view <n> --comments
gh issue list --state open --label ready-for-agent --json number,title,labels,createdAt,authorAssociation

# write (use --body-file or a heredoc, never inline backticks)
gh issue create --title "fix: …" --label needs-triage --body-file body.md
gh issue comment <n> --body-file comment.md
gh issue edit <n> --add-label ready-for-agent --remove-label needs-triage

# native dependencies take the blocker's database id, not its number
blocker_id=$(gh api repos/thschue/goldie/issues/<blocker> --jq .id)
gh api --method POST repos/thschue/goldie/issues/<n>/dependencies/blocked_by -F issue_id="$blocker_id"
gh api repos/thschue/goldie/issues/<n>/dependencies/blocked_by --jq '[.[] | select(.state=="open")] | length'

# sub-issues
child_id=$(gh api repos/thschue/goldie/issues/<child> --jq .id)
gh api --method POST repos/thschue/goldie/issues/<epic>/sub_issues -F sub_issue_id="$child_id"
```

## Frontier query

The **frontier** is the set of issues an agent may start now. An issue is on the frontier when:

1. it is open and labelled `ready-for-agent`;
2. it is not an `epic`;
3. it has no `in-progress` label and no assignee;
4. every native `blocked_by` dependency is closed;
5. no remote branch `*/issue-<n>-*` exists (`scripts/claim` checks this);
6. it is in the current milestone (the open one with the lowest number) or labelled `security`
   ([order of work](./README.md#order-of-work)).

`scripts/frontier` prints it ranked by the [order of work](./README.md#order-of-work). An epic's
slices must be its native sub-issues, or they rank as standalone work.

```sh
# open issues this one blocks (critical-path count, first level)
gh api repos/thschue/goldie/issues/<n>/dependencies/blocking --jq '[.[] | select(.state=="open")] | length'
```

## `gh` hygiene

- **Label edits are not atomic.** `--add-label a --remove-label b` can half-apply. Re-read the labels
  afterwards. If `gh issue edit` or `gh pr edit` fails, use the REST endpoints
  (`gh api -X POST …/labels`, `gh api -X DELETE …/labels/<name>`).
- **Splice bodies by line**, not by exact-match replacement of long passages. Re-fetch the body
  right before editing it.
- **Search matches loosely.** `gh pr list --search "123"` also matches other numbers. Confirm the
  link through `Closes #<n>` in the PR body or the branch name.
- **Check `mergeStateStatus` twice.** `UNKNOWN` means GitHub hasn't computed it yet; re-query before
  treating the PR as clean.
- Old job logs: `gh run view --log-failed` can come back empty. Use
  `gh api repos/thschue/goldie/actions/jobs/<id>/logs` instead.

## Issue template

The product session writes every `ready-for-agent` issue in this shape; the `test-author` turns
`Acceptance` into tests and the `implementer` follows `Pattern to follow`.

```markdown
## Problem
<who is affected and what they can't do today>
## Acceptance
- [ ] <observable behaviour at a named seam: HTTP, CLI, MCP or UI>
## Pattern to follow
<existing file(s) whose approach this copies, and the module/seam>
## Notes
<edge cases, security cases, UI criteria, out of scope>
```

The lane is the `area:` label; blockers are native `blocked_by` edges.

## When a skill says "publish to the issue tracker"

Create a GitHub issue. When it says "fetch the relevant ticket", run `gh issue view <n> --comments`.
