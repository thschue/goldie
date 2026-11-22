# Example knowledge

Original example content for goldie, used by tests, local development and demos.

`global/` currently holds goldie's own agent loops as dogfooded Items:
- the `agent-loops` article;
- the `issue-worker`, `review` and `pm-audit` loops;
- the `implementer`, `security-reviewer`, `architecture-reviewer`, `ux-reviewer` and `product-owner`
  agents.

The agent Items are also goldie's own subagent definitions: `.claude/agents/` holds byte copies,
checked by `./run check` and refreshed with `./run sync-agents`. The loop Items and the article
summarise the rules; the authoritative rules are in `docs/agents/`.

They follow the content format in spec #1. The engine will validate them with `goldie validate` once
the example-content ticket lands, and that ticket also adds the remaining examples (two Sources,
project scope, decisions, skills).
