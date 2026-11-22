---
name: issue-worker
description: Rework handed-back PRs, then claim unblocked ready-for-agent issues and implement each in an isolated worktree, handing a green PR to review. Never reviews or merges.
keywords: [issue worker, implement, backlog, autonomous, ready-for-agent, pull request, rework]
interval: self-paced
agents: [implementer]
skills: []
stop: "Nothing to rework and no claimable issue for two consecutive ticks, or the maintainer stops the loop."
---
Run one tick with `agent:implementer` subagents. The repository's loop spec and label contract are the
rules; this Item is the summary (`goldie lookup agent-loops`).

1. Check the state of `main`. When it's red, fix it before anything else.
2. **Rework first:** own PRs labelled `changes-requested`, whether or not they close an issue.
3. Then claim frontier issues, up to the budget, and dispatch one implementer per item, each in its
   own worktree.
4. Mark each green draft ready, so the review loop picks it up. Park what can't be done with
   `needs-info` and a reason.

Report what you reworked, claimed, shipped, parked or cleaned up, or "nothing to do".
