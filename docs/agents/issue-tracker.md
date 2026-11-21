# Issue tracker: GitHub

Issues, specs and PRs for goldie live on GitHub in `thschue/goldie`. Use the `gh` CLI for every
operation; inside a clone it infers the repository from the remote.

## Conventions

- **Sequencing lives in GitHub, not in a roadmap file.** Architecture decisions go in `docs/adr/`.
  Issue-specific context goes in the issue body and comments. Blocking relationships are native
  issue dependencies, and an epic's parts are native sub-issues.
- **One PR closes exactly one issue** (`Closes #<n>` in the body). The exception is a
  [found-fix PR](./label-contract.md#fix-it-now), which fixes one bug found while working and closes
  no issue. Open it as a **draft before the
  first line of code**, title it with a Conventional Commit type (`feat:`, `fix:`, `docs:`,
  `refactor:`, `test:`, `build:`, `ci:`, `chore:`, `perf:`, `style:`, `revert:`), and
  **squash-merge** it. The `lint-pr-title` check enforces the title.
- Before a PR leaves draft, work through `docs/testing-checklist.md` (added by the bootstrap issue)
  and run the built-in `/security-review` on your diff (not `/review-loop`, which is the review
  loop).

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
3. it has no `implementation-in-progress` label and no assignee;
4. every native `blocked_by` dependency is closed;
5. if `LOOP_MILESTONE` is set, it is in that milestone.

Rank the frontier in this order: issues carrying `changes-requested` (loop-backs), then `bug`, then
`security`, then `priority`, then oldest `createdAt` first.

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

## When a skill says "publish to the issue tracker"

Create a GitHub issue. When it says "fetch the relevant ticket", run `gh issue view <n> --comments`.
