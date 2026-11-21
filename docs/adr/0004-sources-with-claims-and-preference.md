# 4. Sources with Claims and preference

Status: accepted

## Context

Several teams own different knowledge repositories. Without rules, any repository could publish global guidance or override another team's project.

## Decision

Goldie reads Items from several Sources, listed in preference order. Each Source has Claims (global and/or project globs) and is deny-by-default; an Item outside its Source's Claims is a validation error. Collisions within a Scope are resolved by Source preference, first wins, and the loser is reported as shadowed. Each Source keeps its own last good Snapshot, so one failing Source never takes the others down.

## Consequences

Ownership is explicit and predictable, and failures are contained. The cost is configuration: an operator must declare Claims for every Source before it publishes anything, and an enforced Item losing on preference is an error rather than a silent override.
