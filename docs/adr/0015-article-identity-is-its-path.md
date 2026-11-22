# 15. Article identity is its path; frontmatter name is the title

Status: proposed

## Context

CONTEXT.md and spec #1 say an Item is identified by Kind plus name, and every Kind has a frontmatter
`name`. For skills, agents, loops and instructions the layout already puts the name in the path
(`skills/<name>/SKILL.md`, `agents/<name>.md`), and the Claude Code formats make frontmatter `name`
equal to that slug. Articles live under `articles/**`, which may nest. The example article's `name` is
a title with spaces (`Autonomous agent loops`), which is poor as a stable identifier, an API path
segment or a Resolution key. The tracer engine (#69, PR #75) identifies articles by path and looks
them up ignoring case, which the record does not describe. Whatever is chosen fixes the API
`articles/{id}` shape and the keys Resolution compares across Scopes and Sources, so it is hard to
reverse.

## Decision

An Item's identity is Kind plus name, where name is the Item's path-derived slug:

- for articles, the path under `<scope>/articles/` without `.md`, slash separated;
- for the other Kinds, the slug their layout already defines.

For articles, frontmatter `name` is the display title and is not part of the identity. For the other
Kinds, frontmatter `name` must equal the slug, and a mismatch is a validation error. Identity compares
case-insensitively, so two Items in one Snapshot whose identities differ only in case are a validation
error that names both files. Resolution, Claims and the API use this identity.

## Consequences

Identities are stable, URL-safe and visible in the file tree, so renaming a title never breaks links or
shadowing. Moving or renaming an article file changes its identity, which is a breaking change for
anyone linking to it and is shown in review as a rename. Case-only collisions fail at load instead of
resolving arbitrarily. CONTEXT.md's Item entry gains one sentence on what "name" means per Kind. The
rejected alternative, frontmatter `name` as identity for articles, would decouple identity from file
layout but needs a separate slug rule for titles and a uniqueness check across nested directories.
