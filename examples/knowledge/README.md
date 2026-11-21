# Example knowledge

Original example content for goldie, used by tests, local development and demos.

`global/` currently holds goldie's own agent loops as dogfooded Items:
- the `agent-loops` article;
- the `issue-worker`, `security-review` and `pm-audit` loops;
- the `implementer`, `security-reviewer` and `product-owner` agents.

They follow the content format in spec #1. The engine will validate them with `goldie validate` once
the example-content ticket lands, and that ticket also adds the remaining examples (two Sources,
project scope, decisions, skills).
