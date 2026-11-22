# Example knowledge

Original example content for goldie, used by tests, local development and demos.

`global/` currently holds goldie's own agent loops as dogfooded Items:
- the `agent-loops` article;
- the `product` and `delivery` loops;
- the `product-owner`, `architect`, `ux-designer`, `test-author`, `implementer`, `reviewer` and
  `security-reviewer` agents.

The agent Items are also goldie's own subagent definitions: `.claude/agents/` holds byte copies,
checked by `./run check` and refreshed with `./run sync-agents`. The loop Items and the article
summarise the rules; the authoritative rules are in `docs/agents/`.

They follow the content format in spec #1. The engine will validate them with `goldie validate` once
the example-content ticket lands, and that ticket also adds the remaining examples (two Sources,
project scope, decisions, skills).

A Project is registered by adding a `projects/<host>/<org>/<repo>/` directory to one of goldie's
Sources, with or without articles in it, and a request for a Project that is not registered gets the
global answer and a hint that says so.
