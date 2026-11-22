---
name: delivery
description: Turn ready issues into merged pull requests, two streams at a time. Each stream owns one issue end to end - claim, acceptance tests, implementation, fresh review, fixes, merge - and never hands it to another session.
keywords: [delivery, implement, review, stream, acceptance tests, tdd, auto-merge, pull request, ready-for-agent]
interval: self-paced
agents: [test-author, implementer, reviewer, architect, ux-designer, security-reviewer]
skills: [tdd]
stop: "No stream running and no claimable issue for two consecutive ticks, or the maintainer stops the loop."
---
Run one tick. The repository's loop rules are authoritative; this Item is the summary
(`goldie lookup agent-loops`).

1. If `main` is red, fix it first.
2. Advance every stream whose subagent returned; resume your own streams from GitHub state. A
   stream owns its PR until it merges or closes: red CI, conflicts and answers to its questions.
3. While fewer than two streams run, claim the first issue of the ranked frontier (bugs and
   priorities, then the started epic, then standalone work, then a new epic) by pushing its branch.
   The first push wins.
4. Each stream:
   - `agent:test-author` writes failing acceptance tests;
   - `agent:implementer` makes them pass, following the existing pattern;
   - `agent:reviewer` reviews with fresh eyes (with `agent:architect`, `agent:ux-designer` or
     `agent:security-reviewer` when their triggers match);
   - the same implementer fixes the findings, at most twice;
   - the merge script checks the gate and the test lock and merges the PR.
5. Subagents talk only through you, as logged consults. Implementers never change a test they
   didn't add without the test author's approval.

Report streams advanced, claims, merges, and anything held or parked.
