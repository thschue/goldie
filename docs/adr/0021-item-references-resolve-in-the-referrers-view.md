# 21. Item references are `<kind>:<name>`, resolved in the referrer's view

Status: proposed

## Context

`suppress`, `replaced_by` and lookup lines all name Items. ADR 0015 fixes identity but no reference
syntax, and content in many repositories will be written against whatever is chosen.

## Decision

One reference form, `<kind>:<name>`: lower-case Kind, name compared ignoring case (0015), the
article-id charset, no `..`. It may cross Kinds and Scopes. A reference resolves in the referrer's
Resolution view: a project Item sees project then global, and a global Item sees global only. A
`replaced_by` or lookup line that doesn't resolve fails that Snapshot's load, and the last good
Snapshot keeps serving. A `suppress` entry that doesn't resolve is a warning, because a Project
shouldn't fail on another team's deletion.

## Consequences

One parser in the engine (`parseRef`, `internal/engine/governance.go`), which #7, #10 and #15
inherit. Renaming an article breaks references to it at load time instead of silently. Cross-Source
references wait for #7.
