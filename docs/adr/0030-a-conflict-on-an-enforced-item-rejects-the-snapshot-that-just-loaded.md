# 30. A conflict on an enforced Item rejects the Snapshot that just loaded

Status: proposed

## Context

ADR 0004 says an enforced Item losing on Source preference is an error, and that each Source keeps
its own last good Snapshot. It does not say which Source's Snapshot the error rejects when the
conflict only appears once two Sources are seen together, nor how that stays true while Sources
change at the same time.

## Decision

This extends ADR 0004.

- Each Source is polled on its own. Before a new Snapshot replaces that Source's current one, it is
  checked against the other Sources' current Snapshots. It is rejected, and the Source keeps its last
  good Snapshot, when:
  - an enforced Item loses on preference to a published copy in the same Scope, whichever of the two
    Sources just loaded;
  - a Suppression in one Source names a published enforced Item of another, even when it names
    nothing in its own Source;
  - an Item at a narrower Scope in one Source shadows a published enforced Item at a wider Scope in
    another (ADR 0021 across Sources).
- The rejected Snapshot is always the one that just loaded, not the one already serving, so a
  Source that is fine today cannot be broken by a poll of another Source.
- The check and the swap run under one lock. Two Sources that change in the same tick, each fine
  against the other's old Snapshot, cannot both be accepted: the second to take the lock is checked
  against the first and rejected.
- The rejection is that Source's `last_error`, and names both Sources.
- A draft does not enforce and shadows nothing.

## Consequences

- Which of two conflicting Sources is rejected depends on poll order, not on preference. Both
  recover on the next poll once the conflict is removed.
- A Suppression that names nothing in its own Source is no longer a warning when another Source
  holds the Item; it applies in the merged view.
- Checks are pairwise over the loaded Sources, so a poll costs a scan of every other Source's
  Items.
