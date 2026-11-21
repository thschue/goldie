# 6. On-demand delivery with an optional pull

Status: accepted

## Context

Copying agent assets (skills, agents, loops) into repositories makes them drift. Nothing, on the other hand, makes agents consult the golden path.

## Decision

Delivery is on demand by default: MCP tools, prompts and resources, and a SessionStart hook that injects the Catalog and the instruction clauses. Where files are genuinely needed (subagents, loops, script skills), `goldie pull` mirrors the resolved set into the Goldie namespace (`goldie-*` paths under `.claude`), replaced wholesale and stamped with a digest so `--check` can fail CI on staleness. Pull never writes outside the namespace, and hostile archives are rejected. Target adapters exist for the Claude format first.

## Consequences

Agents get current guidance without anything installed, and repositories only hold files they need, in a namespace goldie owns. The cost is two delivery paths to keep consistent, and a hard rule that pull can never touch files it does not own.
