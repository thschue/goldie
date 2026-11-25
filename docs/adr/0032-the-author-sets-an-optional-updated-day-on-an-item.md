# 32. The author sets an optional `updated` day on an Item

Status: accepted

## Context

The path views (what is new, newest first) and an Item's "updated" field need a change time for each
Item, and the engine has none. Two sources for it were possible besides the author: git history and a
snapshot diff.

## Decision

The author sets the change time in the frontmatter, as the optional key `updated`, a day in the form
`YYYY-MM-DD`. It lives in the shared lifecycle frontmatter, so every Kind takes it: article, skill,
agent, loop and decision. A decision keeps `date` as its decision day; `updated` is separate there.

An Item without `updated` loads as before, with no change time. Any other value fails the Item with
"updated must be a day such as 2023-05-02", and the error never echoes the value. A `SKILL.md` that
carries a bad `updated` fails closed like a bad `status`: the skill is a draft and the load does not fail.

Rejected:

- **Git history.** It needs full clones, which `gitlimit.go` forbids, and it says nothing for a `dir`
  Source, which has no history.
- **Snapshot diff.** The time of the first Snapshot that held a change resets on restart and differs
  per replica (ADR 0022).

## Consequences

- The change time is exactly what the author wrote, the same on every replica and after every restart.
- Authors have to bump `updated` by hand; an Item they forget shows its old day.
- A day carries no time of day, so two edits on one day are not ordered.
