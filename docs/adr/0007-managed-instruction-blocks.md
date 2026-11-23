# 7. Managed instruction blocks

Status: accepted

Amended by [26. Managed-block marker format v1](0026-managed-block-marker-format-v1.md): the marker format.

## Context

Telling agents to use goldie has to survive in files people also edit by hand, and has to be enforceable rather than a suggestion.

## Decision

Instruction clauses are written by `goldie instructions apply` into a managed marker block in `CLAUDE.md` and/or `AGENTS.md`. Content outside the block is never touched, and a hand-edited block is not overwritten without `--force`. `goldie instructions check` fails CI when the block is missing, outdated or edited. A built-in default clause exists before any clause is authored.

## Consequences

The instruction is both shared and enforced, and people keep ownership of the rest of the file. The cost is a marker format that becomes a compatibility surface: changing it strands existing blocks.
