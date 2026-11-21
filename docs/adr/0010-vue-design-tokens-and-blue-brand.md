# 10. Vue with a design-token structure and goldie's own blue brand

Status: accepted

## Context

The UI needs light and dark themes, WCAG 2.1 AA and a recognisable identity, without hard-coding colours throughout components.

## Decision

The UI is a Vue SPA, embedded in the binary and talking only to the API. Styling uses a design-token system: shared scales plus a brand layer, so the brand can change without touching components. Goldie's own brand is blue (inspired by Phippy's Goldie), with its own identity. The Phippy artwork is not used unless its licence is confirmed. UI work starts only from an approved design handoff.

## Consequences

Theming and rebranding are cheap and accessibility is designed in. The cost is the discipline of tokens-only styling, and an approval gate before any UI is built.
