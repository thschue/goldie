# 5. Keyword-only search

Status: accepted

## Context

Full-text, semantic or embedding search would add an index, a model or a service to operate, and its ranking is hard to explain.

## Decision

Search is keyword scoring only: an exact keyword match scores +4, a substring of a keyword +1 and a word in the name +2. Every result carries a `matched` explanation, plus `kind` and `scope`. Authors add `keywords` to Items so they are findable by the words people type.

## Consequences

Search is deterministic, explainable, dependency-free and identical in local and remote mode. The cost is recall: guidance without good keywords is hard to find, which is why Misses are surfaced to the platform team. Changing the scoring changes results for everyone, so it is a decision, not a tweak.
