# 21. Item references are `<kind>:<name>`, resolved in the referrer's view

Status: proposed

## Context

`suppress`, `replaced_by` and lookup lines all name Items. ADR 0015 fixes identity but no reference
syntax, and content in many repositories will be written against whatever is chosen.

## Decision

One reference form, `<kind>:<name>`, at most 256 bytes:

- The Kind is lower-case letters and must be a Kind goldie knows (today `article`).
- The name is compared ignoring case (0015). It is slash separated, with no empty, `.` or `..`
  segment, so it has no leading or trailing `/`. It holds only printable characters: no Unicode
  space (the plain space included), no control, format or bidi character, and no colon or backslash.
- An article whose path holds a character outside that set still loads, but it cannot be
  referenced.

A reference may cross Kinds and Scopes. It resolves in the referrer's Resolution view: a project Item
sees project then global, and a global Item sees global only. A `replaced_by` or lookup line that
doesn't resolve fails that Snapshot's load, and the last good Snapshot keeps serving. A `suppress`
entry that doesn't resolve is a warning, because a Project shouldn't fail on another team's deletion.

## Consequences

- One parser in the engine (`parseRef`, `internal/engine/governance.go`), which #7, #10 and #15
  inherit.
- Renaming an article breaks references to it at load time instead of silently.
- Cross-Source references wait for #7.
- A reference is validated in the referrer's view at load, but a reader follows it by name in the
  reader's own view. A Project that shadows the target therefore sees its own version. Later slices
  that show a reference (deprecation hints, MCP display) must say which Item a hint points at.

Rejected alternatives:

- **Global-only references.** A Project could not point a deprecation at its own replacement.
- **Bare ids.** They are ambiguous across Kinds.
- **Resolving in the reader's view at load.** A global Item would be checked against every Project,
  so one team's shadowing could break the global Snapshot.
